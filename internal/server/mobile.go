package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"qinlin/internal/qinlin"
	"qinlin/internal/store"
)

const mobilePrefix = "/api/mobile/v1"
const mobileEnabledSetting = "mobile_enabled"

type smsChallenge struct {
	phone, installation, deviceID string
	expires                       time.Time
	attempts                      int
}

type mobileAccountView struct {
	ID            string    `json:"id"`
	MaskedPhone   string    `json:"maskedPhone"`
	AuthorizedAt  time.Time `json:"authorizedAt"`
	Authorization string    `json:"authorization"`
	WidgetDoorID  *string   `json:"widgetDoorId"`
}
type mobileSnapshot struct {
	Account mobileAccountView   `json:"account"`
	Doors   []store.Door        `json:"doors"`
	Shares  []store.MobileShare `json:"shares"`
}

func (s *Server) mobileHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST "+mobilePrefix+"/login", s.mobileLogin)
	m.HandleFunc("POST "+mobilePrefix+"/sms", s.withMobile("device", s.mobileSMS))
	m.HandleFunc("POST "+mobilePrefix+"/verify", s.withMobile("device", s.mobileVerify))
	m.HandleFunc("GET "+mobilePrefix+"/current", s.withMobile("device", s.mobileCurrent))
	m.HandleFunc("POST "+mobilePrefix+"/logout", s.withMobile("device", s.mobileLogout))
	m.HandleFunc("POST "+mobilePrefix+"/doors/{door}/open", s.withMobile("device", s.mobileOpen))
	m.HandleFunc("POST "+mobilePrefix+"/shares", s.withMobile("device", s.mobileCreateShare))
	m.HandleFunc("PATCH "+mobilePrefix+"/shares/{share}", s.withMobile("device", s.mobileUpdateShare))
	m.HandleFunc("DELETE "+mobilePrefix+"/shares/{share}", s.withMobile("device", s.mobileUpdateShare))
	m.HandleFunc("POST "+mobilePrefix+"/shares/{share}/open", s.withMobile("share", s.mobileOpen))
	m.HandleFunc("PUT "+mobilePrefix+"/widget", s.withMobile("device", s.mobileSetWidget))
	m.HandleFunc("POST "+mobilePrefix+"/widget/open", s.withMobile("widget", s.mobileOpen))
	m.HandleFunc("GET "+mobilePrefix+"/widget/status", s.withMobile("widget", s.mobileWidgetStatus))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enabled, err := s.mobileEnabled()
		if err != nil {
			storeError(w, err)
			return
		}
		if !enabled {
			writeError(w, http.StatusServiceUnavailable, "mobile_disabled")
			return
		}
		m.ServeHTTP(w, r)
	})
}

func (s *Server) mobileEnabled() (bool, error) {
	enabled := true
	err := s.store.Setting(mobileEnabledSetting, &enabled)
	return enabled, err
}

func (s *Server) adminMobileOverview(w http.ResponseWriter, _ *http.Request, _ principal) {
	enabled, err := s.mobileEnabled()
	if err != nil {
		storeError(w, err)
		return
	}
	accounts, shares, err := s.store.AdminMobileOverview()
	if err != nil {
		storeError(w, err)
		return
	}
	write(w, 200, map[string]any{"accounts": accounts, "shares": shares, "enabled": enabled})
}

func (s *Server) setMobileSettings(w http.ResponseWriter, r *http.Request, _ principal) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeError(w, 400, "invalid_mobile_setting")
		return
	}
	if err := s.store.SaveSetting(mobileEnabledSetting, *body.Enabled); err != nil {
		storeError(w, err)
		return
	}
	write(w, 200, map[string]bool{"enabled": *body.Enabled})
}

func (s *Server) mobileAccountID(phone string) string {
	return s.mobileAccountKey("mobile-owner:" + phone)
}

func (s *Server) mobileServerAccountID() string {
	return s.mobileAccountKey("mobile-server-owner:" + s.cfg.AdminUsername)
}

func (s *Server) mobileAccountKey(value string) string {
	h := hmac.New(sha256.New, s.cfg.Key)
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Server) mobileLogin(w http.ResponseWriter, r *http.Request) {
	if !s.allow("login:"+ip(r), 10, time.Minute) || !s.allow("login:all", 30, time.Minute) {
		writeError(w, 429, "rate_limited")
		return
	}
	var body struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		Installation string `json:"installation"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !idPattern.MatchString(body.Installation) {
		writeError(w, 400, "invalid_device")
		return
	}
	if s.cfg.AdminPasswordHash == "" {
		writeError(w, 409, "server_account_unconfigured")
		return
	}
	if !s.validAdminPassword(body.Username, body.Password) {
		writeError(w, 401, "invalid_credentials")
		return
	}
	accountID := s.mobileServerAccountID()
	defer s.lockMobileAccount(accountID)()
	p, token, err := s.store.StartMobileSession(accountID, body.Installation)
	if err != nil {
		storeError(w, err)
		return
	}
	s.mobileReply(w, p, 200, map[string]any{"session": token})
}

func (s *Server) lockMobileAccount(id string) func() {
	index := sha256.Sum256([]byte(id))
	lock := &s.mobileLocks[int(index[0])%len(s.mobileLocks)]
	lock.Lock()
	return lock.Unlock
}

func (s *Server) mobilePrincipal(r *http.Request, kind string) (store.MobilePrincipal, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || len(token) != 46 {
		return store.MobilePrincipal{}, sql.ErrNoRows
	}
	p, err := s.store.AuthenticateMobile(token, kind, r.PathValue("share"))
	if err == nil && kind == "device" && (s.cfg.AdminPasswordHash == "" || p.AccountID != s.mobileServerAccountID()) {
		return store.MobilePrincipal{}, sql.ErrNoRows
	}
	return p, err
}

func writeMobileAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 401, "authentication_required")
	} else {
		storeError(w, err)
	}
}

func (s *Server) withMobile(kind string, next func(http.ResponseWriter, *http.Request, store.MobilePrincipal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.mobilePrincipal(r, kind)
		if err != nil {
			writeMobileAuthError(w, err)
			return
		}
		defer s.lockMobileAccount(p.AccountID)()
		p, err = s.mobilePrincipal(r, kind)
		if err != nil {
			writeMobileAuthError(w, err)
			return
		}
		next(w, r, p)
	}
}

func (s *Server) mobileSMS(w http.ResponseWriter, r *http.Request, p store.MobilePrincipal) {
	var body struct {
		Phone        string `json:"phone"`
		Installation string `json:"installation"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !idPattern.MatchString(body.Installation) {
		writeError(w, 400, "invalid_device")
		return
	}
	a, err := s.store.MobileAccount(p.AccountID)
	if err != nil {
		storeError(w, err)
		return
	}
	if body.Phone == "" {
		body.Phone = a.Phone
	} else if a.Phone != "" {
		writeError(w, 409, "phone_already_bound")
		return
	}
	if !phonePattern.MatchString(body.Phone) {
		writeError(w, 400, "invalid_phone")
		return
	}
	accountID := s.mobileAccountID(body.Phone)
	if !s.allow("mobile-sms:global", 100, time.Hour) ||
		!s.allow("mobile-sms:ip:"+ip(r), 20, time.Hour) ||
		!s.allow("mobile-sms:device:"+body.Installation, 5, time.Hour) ||
		!s.allow("mobile-sms:hour:"+accountID, 5, time.Hour) ||
		!s.allow("mobile-sms:minute:"+accountID, 1, time.Minute) {
		writeError(w, 429, "sms_rate_limited")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.UpstreamTimeout)
	defer cancel()
	if err := s.api.SendSMS(ctx, body.Phone); err != nil {
		upstreamError(w, err)
		return
	}
	challenge := store.Random()
	now := time.Now()
	s.mu.Lock()
	for key, pending := range s.mobileChallenges {
		if !pending.expires.After(now) || (pending.deviceID == p.ID) {
			delete(s.mobileChallenges, key)
		}
	}
	s.mobileChallenges[challenge] = smsChallenge{body.Phone, body.Installation, p.ID, now.Add(5 * time.Minute), 0}
	s.mu.Unlock()
	write(w, 200, map[string]any{"challenge": challenge})
}

func (s *Server) mobileVerify(w http.ResponseWriter, r *http.Request, p store.MobilePrincipal) {
	var body struct {
		Challenge    string `json:"challenge"`
		Installation string `json:"installation"`
		Code         string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Code) != 6 || !codePattern.MatchString(body.Code) {
		writeError(w, 400, "invalid_login_fields")
		return
	}
	if !s.allow("mobile-verify:ip:"+ip(r), 60, time.Minute) ||
		!s.allow("mobile-verify:device:"+body.Installation, 10, time.Minute) {
		writeError(w, 429, "rate_limited")
		return
	}
	s.mu.Lock()
	pending, exists := s.mobileChallenges[body.Challenge]
	valid := exists && pending.deviceID == p.ID && pending.installation == body.Installation &&
		pending.expires.After(time.Now()) && pending.attempts < 5
	if valid {
		pending.attempts++
		s.mobileChallenges[body.Challenge] = pending
	}
	s.mu.Unlock()
	if !valid {
		writeError(w, 409, "sms_challenge_required")
		return
	}
	a, err := s.store.MobileAccount(p.AccountID)
	if err != nil {
		storeError(w, err)
		return
	}
	// A second device may already have bound a different number.
	if a.Phone != "" && a.Phone != pending.phone {
		writeError(w, 409, "phone_already_bound")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	session, err := s.api.Login(ctx, pending.phone, body.Code)
	if err != nil {
		upstreamError(w, err)
		return
	}
	s.mu.Lock()
	delete(s.mobileChallenges, body.Challenge)
	s.mu.Unlock()
	doors, err := s.api.Doors(ctx, session)
	if err != nil {
		if expireErr := s.store.ExpireMobileAccount(p.AccountID); expireErr != nil {
			s.log.Error("mobile account state persistence failed")
		}
		upstreamError(w, err)
		return
	}
	var legacyID string
	if a.Phone == "" {
		legacyID = s.mobileAccountID(pending.phone)
	}
	if err := s.store.BindMobilePhone(p, legacyID, pending.phone, session, doors); err != nil {
		storeError(w, err)
		return
	}
	s.mobileReply(w, p, 200, map[string]any{})
}

func (s *Server) mobileReply(w http.ResponseWriter, p store.MobilePrincipal, status int, response map[string]any) {
	a, err := s.store.MobileAccount(p.AccountID)
	if err != nil {
		storeError(w, err)
		return
	}
	response["mock"] = s.cfg.Mock
	if a.Phone == "" {
		response["snapshot"] = nil
		write(w, status, response)
		return
	}
	doors, err := s.store.MobileDoors(p.AccountID)
	if err != nil {
		storeError(w, err)
		return
	}
	shares, err := s.store.MobileShares(p.AccountID)
	if err != nil {
		storeError(w, err)
		return
	}
	for i := range shares {
		shares[i].URL = s.mobileShareURL(shares[i].ID)
	}
	view := mobileAccountView{
		ID: a.ID, MaskedPhone: a.MaskedPhone, AuthorizedAt: time.UnixMilli(a.AuthorizedAt).UTC(), Authorization: "valid",
	}
	if a.Expired {
		view.Authorization = "expired"
	}
	for _, door := range doors {
		if door.ID == p.DoorID && door.Available {
			view.WidgetDoorID = &p.DoorID
			break
		}
	}
	response["snapshot"] = mobileSnapshot{view, doors, shares}
	write(w, status, response)
}

func (s *Server) mobileRefresh(ctx context.Context, a store.MobileAccount) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	doors, err := s.api.Doors(ctx, a.Session)
	if err != nil {
		if qinlin.Kind(err) == "expired" {
			if err := s.store.ExpireMobileAccount(a.ID); err != nil {
				return err
			}
		}
		return err
	}
	return s.store.RefreshMobileDoors(a.ID, doors)
}

func (s *Server) mobileCurrent(w http.ResponseWriter, r *http.Request, p store.MobilePrincipal) {
	a, err := s.store.MobileAccount(p.AccountID)
	if err != nil {
		storeError(w, err)
		return
	}
	if a.Phone != "" && !a.Expired && time.Since(time.UnixMilli(a.CheckedAt)) >= time.Minute {
		// Keep the last snapshot during a transient upstream outage. Opening a
		// door independently refreshes the permission list.
		if err := s.mobileRefresh(r.Context(), a); err != nil {
			var upstream *qinlin.Error
			if !errors.As(err, &upstream) {
				s.log.Warn("mobile permission refresh failed")
			}
		}
	}
	s.mobileReply(w, p, 200, map[string]any{"mock": s.cfg.Mock})
}

func (s *Server) mobileLogout(w http.ResponseWriter, _ *http.Request, p store.MobilePrincipal) {
	if err := s.store.RevokeMobileDevice(p); err != nil {
		storeError(w, err)
		return
	}
	write(w, 200, map[string]bool{"ok": true})
}

func (s *Server) mobileWidgetStatus(w http.ResponseWriter, r *http.Request, p store.MobilePrincipal) {
	a, err := s.store.MobileAccount(p.AccountID)
	if err != nil {
		storeError(w, err)
		return
	}
	expired := a.Expired
	if !expired && time.Since(time.UnixMilli(a.CheckedAt)) >= time.Minute {
		if !s.allow("mobile-status:"+p.AccountID, 6, time.Minute) {
			writeError(w, 429, "rate_limited")
			return
		}
		if err := s.mobileRefresh(r.Context(), a); err != nil {
			if qinlin.Kind(err) != "expired" {
				writeError(w, 503, "status_unavailable")
				return
			}
			expired = true
		}
	}
	if expired {
		write(w, 200, map[string]string{"authorization": "expired"})
		return
	}
	if _, err := s.store.MobileDoor(p.AccountID, p.DoorID); err != nil {
		storeError(w, err)
		return
	}
	write(w, 200, map[string]string{"authorization": "valid"})
}

func (s *Server) mobileAuthorized(w http.ResponseWriter, r *http.Request, accountID string) (store.MobileAccount, bool) {
	a, err := s.store.MobileAccount(accountID)
	if err != nil {
		storeError(w, err)
		return a, false
	}
	if a.Phone == "" || a.Expired {
		storeError(w, store.ErrNoSession)
		return a, false
	}
	if err := s.mobileRefresh(r.Context(), a); err != nil {
		var upstream *qinlin.Error
		if errors.As(err, &upstream) {
			upstreamError(w, err)
		} else {
			storeError(w, err)
		}
		return a, false
	}
	return a, true
}

func (s *Server) mobileCreateShare(w http.ResponseWriter, r *http.Request, p store.MobilePrincipal) {
	var body struct {
		Name   string `json:"name"`
		DoorID string `json:"doorId"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > 24 {
		writeError(w, 400, "invalid_share_name")
		return
	}
	if _, ok := s.mobileAuthorized(w, r, p.AccountID); !ok {
		return
	}
	if _, err := s.store.MobileDoor(p.AccountID, body.DoorID); err != nil {
		storeError(w, err)
		return
	}
	share, token, err := s.store.CreateMobileShare(p.AccountID, name, body.DoorID)
	if err != nil {
		storeError(w, err)
		return
	}
	share.URL = s.mobileShareURL(share.ID)
	s.mobileReply(w, p, 201, map[string]any{"issued": map[string]any{"share": share, "credential": token, "url": share.URL}})
}

func (s *Server) mobileShareURL(id string) string {
	return s.cfg.PublicURL + mobilePrefix + "/shares/" + id + "/open"
}

func (s *Server) mobileUpdateShare(w http.ResponseWriter, r *http.Request, p store.MobilePrincipal) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if r.Method == http.MethodPatch {
		if !decode(w, r, &body) {
			return
		}
		if body.Enabled == nil {
			writeError(w, 400, "invalid_share_state")
			return
		}
	}
	if err := s.store.UpdateMobileShare(p.AccountID, r.PathValue("share"), body.Enabled); err != nil {
		storeError(w, err)
		return
	}
	s.mobileReply(w, p, 200, map[string]any{})
}

func (s *Server) mobileSetWidget(w http.ResponseWriter, r *http.Request, p store.MobilePrincipal) {
	var body struct {
		DoorID string `json:"doorId"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.DoorID != "" {
		if _, ok := s.mobileAuthorized(w, r, p.AccountID); !ok {
			return
		}
		if _, err := s.store.MobileDoor(p.AccountID, body.DoorID); err != nil {
			storeError(w, err)
			return
		}
	}
	token, expiry, err := s.store.SetMobileWidget(p, body.DoorID)
	if err != nil {
		storeError(w, err)
		return
	}
	p.DoorID = body.DoorID
	s.mobileReply(w, p, 200, map[string]any{"widgetToken": token, "widgetExpiresAt": expiry})
}

func (s *Server) mobileOpen(w http.ResponseWriter, r *http.Request, p store.MobilePrincipal) {
	var body struct{}
	if !decode(w, r, &body) {
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
	doorID := r.PathValue("door")
	if doorID == "" {
		doorID = p.DoorID
	}
	if !s.allow("mobile-open:"+p.AccountID, 40, time.Minute) {
		writeError(w, 429, "rate_limited")
		return
	}
	s.accountMu.RLock()
	defer s.accountMu.RUnlock()
	if s.isRestarting() {
		writeError(w, 503, "service_restarting")
		return
	}
	a, ok := s.mobileAuthorized(w, r, p.AccountID)
	if !ok {
		return
	}
	door, err := s.store.MobileDoor(p.AccountID, doorID)
	if err != nil {
		storeError(w, err)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	op, fresh, err := s.store.ReserveMobile(p, token, id, doorID, s.cfg.Cooldown)
	if err != nil {
		writeMobileAuthError(w, err)
		return
	}
	if !fresh {
		s.operationResult(w, op, true)
		return
	}
	s.dispatchOpen(&op, a.Session, door, func() error { return s.store.ExpireMobileAccount(p.AccountID) })
	s.completeOpen(w, "mobile:"+p.AccountID, op)
}
