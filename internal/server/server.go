package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"qinlin/internal/config"
	"qinlin/internal/qinlin"
	"qinlin/internal/store"
	"qinlin/web"
)

type browserSession struct {
	CSRF          string
	Created, Last time.Time
}
type budget struct {
	Count int
	Until time.Time
}
type principal struct {
	Scope, DoorID, CSRF string
	Admin               bool
}
type Server struct {
	cfg       config.Config
	store     *store.Store
	api       qinlin.API
	log       *slog.Logger
	accountMu sync.RWMutex
	mu        sync.Mutex
	sessions  map[string]browserSession
	budgets   map[string]budget
}

var phonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
var codePattern = regexp.MustCompile(`^[0-9]{4,8}$`)
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`)
var indexTemplate = template.Must(template.ParseFS(web.Files, "index.html"))

func New(cfg config.Config, db *store.Store, api qinlin.API, log *slog.Logger) *Server {
	return &Server{cfg: cfg, store: db, api: api, log: log, sessions: map[string]browserSession{}, budgets: map[string]budget{}}
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if s.store.Ping() != nil {
			writeError(w, 503, "storage_unavailable")
			return
		}
		write(w, 200, map[string]any{"ok": true})
	})
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := indexTemplate.Execute(w, struct {
			PublicURL     string
			PasswordLogin bool
		}{s.cfg.PublicURL, s.cfg.AdminPasswordHash != ""}); err != nil {
			s.log.Error("page rendering failed")
		}
	})
	m.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) { s.asset(w, "app.js", "text/javascript; charset=utf-8") })
	m.HandleFunc("GET /request-id.js", func(w http.ResponseWriter, r *http.Request) {
		s.asset(w, "request-id.js", "text/javascript; charset=utf-8")
	})
	m.HandleFunc("GET /app.css", func(w http.ResponseWriter, r *http.Request) { s.asset(w, "app.css", "text/css; charset=utf-8") })
	m.HandleFunc("POST /api/v1/auth/login", s.login)
	m.HandleFunc("GET /api/v1/auth/session", s.withAuth(true, s.authSession))
	m.HandleFunc("POST /api/v1/auth/logout", s.withAuth(true, s.logout))
	m.HandleFunc("GET /api/v1/status", s.withAuth(true, s.status))
	m.HandleFunc("POST /api/v1/account/sms", s.withAuth(true, s.sms))
	m.HandleFunc("POST /api/v1/account/login", s.withAuth(true, s.accountLogin))
	m.HandleFunc("DELETE /api/v1/account", s.withAuth(true, s.disconnect))
	m.HandleFunc("POST /api/v1/doors/refresh", s.withAuth(true, s.refresh))
	m.HandleFunc("GET /api/v1/doors", s.withAuth(false, s.doors))
	m.HandleFunc("PATCH /api/v1/doors/{door}", s.withAuth(true, s.enableDoor))
	m.HandleFunc("POST /api/v1/doors/{door}/open", s.withAuth(false, s.openDoor))
	m.HandleFunc("GET /api/v1/tokens", s.withAuth(true, s.tokens))
	m.HandleFunc("POST /api/v1/tokens", s.withAuth(true, s.createToken))
	m.HandleFunc("DELETE /api/v1/tokens/{token}", s.withAuth(true, s.revokeToken))
	m.HandleFunc("GET /api/v1/operations", s.withAuth(true, s.operations))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if strings.HasPrefix(s.cfg.PublicURL, "https://") {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.RawQuery != "" {
			writeError(w, 400, "query_not_supported")
			return
		}
		defer func() {
			if recover() != nil {
				s.log.Error("request panic; inspect local service state")
				writeError(w, 500, "internal_error")
			}
		}()
		m.ServeHTTP(w, r)
	})
}
func (s *Server) asset(w http.ResponseWriter, name, kind string) {
	b, err := web.Files.ReadFile(name)
	if err != nil {
		writeError(w, 500, "asset_unavailable")
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Write(b)
}
func equal(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (s *Server) allow(key string, limit int, duration time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if len(s.budgets) > 1000 {
		for k, v := range s.budgets {
			if now.After(v.Until) {
				delete(s.budgets, k)
			}
		}
		if len(s.budgets) > 5000 {
			return false
		}
	}
	b := s.budgets[key]
	if now.After(b.Until) {
		b = budget{Until: now.Add(duration)}
	}
	if b.Count >= limit {
		return false
	}
	b.Count++
	s.budgets[key] = b
	return true
}
func ip(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.cfg.PublicURL {
		writeError(w, 403, "invalid_origin")
		return
	}
	if !s.allow("login:"+ip(r), 10, time.Minute) || !s.allow("login:all", 30, time.Minute) {
		writeError(w, 429, "rate_limited")
		return
	}
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Token != "" {
		if body.Username != "" || body.Password != "" || !equal(body.Token, s.cfg.AdminToken) {
			writeError(w, 401, "invalid_token")
			return
		}
	} else {
		if s.cfg.AdminPasswordHash == "" || len(body.Password) > 72 {
			writeError(w, 401, "invalid_credentials")
			return
		}
		passwordErr := bcrypt.CompareHashAndPassword([]byte(s.cfg.AdminPasswordHash), []byte(body.Password))
		if !equal(body.Username, s.cfg.AdminUsername) || passwordErr != nil {
			writeError(w, 401, "invalid_credentials")
			return
		}
	}
	id, csrf := store.Random(), store.Random()
	now := time.Now()
	s.mu.Lock()
	for k, v := range s.sessions {
		if now.Sub(v.Created) > 12*time.Hour || now.Sub(v.Last) > time.Hour {
			delete(s.sessions, k)
		}
	}
	if len(s.sessions) >= 20 {
		s.mu.Unlock()
		writeError(w, 429, "too_many_sessions")
		return
	}
	s.sessions[id] = browserSession{CSRF: csrf, Created: now, Last: now}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "qinlin_session", Value: id, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	write(w, 200, map[string]any{"ok": true, "csrf_token": csrf})
}
func (s *Server) authenticate(r *http.Request) (principal, bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		if !strings.HasPrefix(h, "Bearer ") {
			return principal{}, false
		}
		secret := strings.TrimPrefix(h, "Bearer ")
		if len(secret) > 200 {
			return principal{}, false
		}
		if equal(secret, s.cfg.AdminToken) {
			return principal{Scope: "admin", Admin: true}, true
		}
		t, err := s.store.Authenticate(secret)
		if err != nil {
			return principal{}, false
		}
		return principal{Scope: t.ID, DoorID: t.DoorID}, true
	}
	cookie, err := r.Cookie("qinlin_session")
	if err != nil {
		return principal{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[cookie.Value]
	now := time.Now()
	if !ok {
		return principal{}, false
	}
	if now.Sub(v.Created) > 12*time.Hour || now.Sub(v.Last) > time.Hour {
		delete(s.sessions, cookie.Value)
		return principal{}, false
	}
	v.Last = now
	s.sessions[cookie.Value] = v
	return principal{Scope: "admin", Admin: true, CSRF: v.CSRF}, true
}
func (s *Server) withAuth(adminOnly bool, next func(http.ResponseWriter, *http.Request, principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allow("api:"+ip(r), 120, time.Minute) {
			writeError(w, 429, "rate_limited")
			return
		}
		p, ok := s.authenticate(r)
		if !ok {
			writeError(w, 401, "authentication_required")
			return
		}
		if adminOnly && !p.Admin {
			writeError(w, 403, "admin_required")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != s.cfg.PublicURL {
				writeError(w, 403, "invalid_origin")
				return
			}
			if p.CSRF != "" && (origin != s.cfg.PublicURL || !equal(r.Header.Get("X-CSRF-Token"), p.CSRF)) {
				writeError(w, 403, "csrf_required")
				return
			}
		}
		next(w, r, p)
	}
}
func (s *Server) authSession(w http.ResponseWriter, r *http.Request, p principal) {
	write(w, 200, map[string]any{"ok": true, "csrf_token": p.CSRF})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request, p principal) {
	if c, err := r.Cookie("qinlin_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "qinlin_session", Value: "", Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	write(w, 200, map[string]any{"ok": true})
}
func (s *Server) status(w http.ResponseWriter, r *http.Request, p principal) {
	s.accountMu.RLock()
	defer s.accountMu.RUnlock()
	a, err := s.store.Account()
	if err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	write(w, 200, map[string]any{"ok": true, "mock": s.cfg.Mock, "account": map[string]any{"state": a.State, "linked_at": a.LinkedAt, "verified_at": a.VerifiedAt}, "public_url": s.cfg.PublicURL})
}
func (s *Server) sms(w http.ResponseWriter, r *http.Request, p principal) {
	var body struct {
		Phone string `json:"phone"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !phonePattern.MatchString(body.Phone) {
		writeError(w, 400, "invalid_phone")
		return
	}
	if !s.allow("sms", 1, time.Minute) || !s.allow("sms:hour", 5, time.Hour) {
		writeError(w, 429, "sms_rate_limited")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.UpstreamTimeout)
	defer cancel()
	if err := s.api.SendSMS(ctx, body.Phone); err != nil {
		upstreamError(w, err)
		return
	}
	write(w, 200, map[string]any{"ok": true, "mock": s.cfg.Mock})
}
func (s *Server) accountLogin(w http.ResponseWriter, r *http.Request, p principal) {
	var body struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !phonePattern.MatchString(body.Phone) || !codePattern.MatchString(body.Code) {
		writeError(w, 400, "invalid_login_fields")
		return
	}
	if !s.allow("account_login", 5, time.Minute) {
		writeError(w, 429, "rate_limited")
		return
	}
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	// Each individual network call has its own hard timeout. The deadline also
	// bounds discovery across multiple communities.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	session, err := s.api.Login(ctx, body.Phone, body.Code)
	if err != nil {
		upstreamError(w, err)
		return
	}
	fingerprint := hmac.New(sha256.New, s.cfg.Key)
	fingerprint.Write([]byte("account-owner:" + body.Phone))
	session.Owner = hex.EncodeToString(fingerprint.Sum(nil))
	doors, err := s.api.Doors(ctx, session)
	if err != nil {
		// A successful re-login may invalidate the previous session. Do not let
		// that old session/permission snapshot dispatch while discovery is incomplete.
		if s.store.SetAccountState("relogin_required") != nil {
			s.log.Error("account state persistence failed")
		}
		upstreamError(w, err)
		return
	}
	if err = s.store.ReplaceAccount(session, doors); err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	write(w, 200, map[string]any{"ok": true, "door_count": len(doors)})
}
func (s *Server) disconnect(w http.ResponseWriter, r *http.Request, p principal) {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	if s.store.Disconnect() != nil {
		writeError(w, 500, "storage_error")
		return
	}
	write(w, 200, map[string]any{"ok": true})
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request, p principal) {
	if !s.allow("refresh", 3, time.Minute) {
		writeError(w, 429, "rate_limited")
		return
	}
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	a, err := s.store.Account()
	if err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	if a.Session.SessionID == "" || (a.State != "valid" && a.State != "unknown") {
		writeError(w, 409, "login_required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	doors, err := s.api.Doors(ctx, a.Session)
	if err != nil {
		state := "unknown"
		if qinlin.Kind(err) == "expired" {
			state = "expired"
		}
		if s.store.SetAccountState(state) != nil {
			s.log.Error("account state persistence failed")
		}
		upstreamError(w, err)
		return
	}
	if err = s.store.Refresh(doors); err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	write(w, 200, map[string]any{"ok": true, "door_count": len(doors)})
}
func (s *Server) doors(w http.ResponseWriter, r *http.Request, p principal) {
	doors, err := s.store.Doors()
	if err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	if !p.Admin {
		filtered := []store.Door{}
		for _, d := range doors {
			if d.ID == p.DoorID && d.Available && d.Enabled {
				filtered = append(filtered, d)
			}
		}
		doors = filtered
	}
	write(w, 200, map[string]any{"ok": true, "doors": doors})
}
func (s *Server) enableDoor(w http.ResponseWriter, r *http.Request, p principal) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeError(w, 400, "enabled_required")
		return
	}
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	if err := s.store.EnableDoor(r.PathValue("door"), *body.Enabled); err != nil {
		storeError(w, err)
		return
	}
	write(w, 200, map[string]any{"ok": true})
}
func (s *Server) tokens(w http.ResponseWriter, r *http.Request, p principal) {
	tokens, err := s.store.Tokens()
	if err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	write(w, 200, map[string]any{"ok": true, "tokens": tokens})
}
func (s *Server) createToken(w http.ResponseWriter, r *http.Request, p principal) {
	var body struct {
		Name   string `json:"name"`
		DoorID string `json:"door_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 100 || body.DoorID == "" {
		writeError(w, 400, "invalid_token_fields")
		return
	}
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	t, secret, err := s.store.CreateToken(body.Name, body.DoorID)
	if err != nil {
		storeError(w, err)
		return
	}
	write(w, 201, map[string]any{"ok": true, "token": t, "secret": secret, "open_url": s.cfg.PublicURL + "/api/v1/doors/" + t.DoorID + "/open"})
}
func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request, p principal) {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	if s.store.RevokeToken(r.PathValue("token")) != nil {
		writeError(w, 500, "storage_error")
		return
	}
	write(w, 200, map[string]any{"ok": true})
}
func (s *Server) operations(w http.ResponseWriter, r *http.Request, p principal) {
	ops, err := s.store.Operations()
	if err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	write(w, 200, map[string]any{"ok": true, "operations": ops})
}

func (s *Server) openDoor(w http.ResponseWriter, r *http.Request, p principal) {
	// Require a JSON object, even though the target is already in the path.
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	door := r.PathValue("door")
	if !p.Admin && p.DoorID != door {
		writeError(w, 403, "door_not_allowed")
		return
	}
	id := r.Header.Get("Idempotency-Key")
	if id == "" {
		id = store.Random()
	}
	if !idPattern.MatchString(id) {
		writeError(w, 400, "invalid_request_id")
		return
	}
	s.accountMu.RLock()
	defer s.accountMu.RUnlock()
	// Recheck token revocation after acquiring the account lock, so a completed
	// revoke/login/disable action cannot be followed by a waiting old request.
	if !p.Admin {
		latest, ok := s.authenticate(r)
		if !ok || latest.Scope != p.Scope {
			writeError(w, 401, "authentication_required")
			return
		}
	}
	op, fresh, err := s.store.Reserve(p.Scope, id, door, s.cfg.Cooldown)
	if err != nil {
		storeError(w, err)
		return
	}
	if !fresh {
		s.operationResult(w, op, true)
		return
	}
	d, doorErr := s.store.Door(door)
	a, accountErr := s.store.Account()
	if doorErr != nil || accountErr != nil || a.Session.SessionID == "" {
		op.State, op.Code = "rejected", "storage_error"
	} else {
		// Once reserved, finish independently of the phone connection. A lost
		// HTTP response must not cancel/retry an already dispatched command.
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.UpstreamTimeout)
		err = s.api.Open(ctx, a.Session, d.Upstream)
		cancel()
		op.State, op.Code = "accepted", "upstream_accepted"
		if err != nil {
			op.State, op.Code = "unknown", qinlin.Kind(err)
			if op.Code == "expired" || op.Code == "denied" || op.Code == "rejected" || op.Code == "rate_limited" {
				op.State = "rejected"
			}
			if op.Code == "expired" {
				if s.store.SetAccountState("expired") != nil {
					s.log.Error("account state persistence failed")
				}
			}
		}
	}
	op.UpdatedAt = time.Now().UnixMilli()
	if err = s.store.Finish(p.Scope, op); err != nil {
		op.State, op.Code = "unknown", "storage_error"
		s.log.Error("operation result persistence failed", "request_id", op.ID)
	}
	s.log.Info("door request completed", "request_id", op.ID, "state", op.State, "code", op.Code)
	s.operationResult(w, op, false)
}
func (s *Server) operationResult(w http.ResponseWriter, op store.Operation, replayed bool) {
	status := 200
	if op.State == "pending" {
		status = 202
	}
	if op.State == "unknown" {
		status = 502
	}
	if op.State == "rejected" {
		status = 409
	}
	message := "亲邻服务端已接受请求，请确认门的实际状态。"
	if s.cfg.Mock {
		message = "模拟请求成功，未向真实门禁发送命令。"
	}
	if op.State == "unknown" {
		message = "结果不确定，请先检查门的实际状态；服务不会自动重试。"
	}
	if op.State == "rejected" {
		message = "请求被拒绝，请检查登录状态和门禁权限。"
	}
	if op.State == "pending" {
		message = "请求正在处理，请勿再次发送新的开门请求。"
	}
	write(w, status, map[string]any{"ok": op.State == "accepted", "mock": s.cfg.Mock, "replayed": replayed, "operation": op, "message": message})
}
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		writeError(w, 415, "json_required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	// Decode into RawMessage first so JSON null cannot become an empty action.
	var raw json.RawMessage
	if err = d.Decode(&raw); err != nil || len(raw) == 0 || raw[0] != '{' {
		writeError(w, 400, "invalid_json")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		writeError(w, 400, "invalid_json")
		return false
	}
	x := json.NewDecoder(strings.NewReader(string(raw)))
	x.DisallowUnknownFields()
	if x.Decode(out) != nil {
		writeError(w, 400, "invalid_json")
		return false
	}
	return true
}
func upstreamError(w http.ResponseWriter, err error) {
	kind := qinlin.Kind(err)
	status := 502
	if kind == "expired" || kind == "denied" || kind == "rejected" {
		status = 409
	}
	if kind == "rate_limited" {
		status = 429
	}
	writeError(w, status, "upstream_"+kind)
}
func storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrConflict):
		writeError(w, 409, "request_id_conflict")
	case errors.Is(err, store.ErrCooldown):
		w.Header().Set("Retry-After", "5")
		writeError(w, 429, "door_cooldown")
	case errors.Is(err, store.ErrForbidden):
		writeError(w, 403, "door_not_allowed")
	case errors.Is(err, store.ErrNoSession):
		writeError(w, 409, "login_required")
	default:
		writeError(w, 500, "storage_error")
	}
}
func writeError(w http.ResponseWriter, status int, code string) {
	write(w, status, map[string]any{"ok": false, "code": code})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
