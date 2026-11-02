package server

import (
	"bytes"
	"context"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"qinlin/internal/config"
	"qinlin/internal/qinlin"
	"qinlin/internal/store"
)

type testAPI struct {
	qinlin.Mock
	calls                              atomic.Int32
	discoveries                        atomic.Int32
	discoveryStarted, discoveryRelease chan struct{}
	fail                               string
	discoveryFailure                   string
	started, release                   chan struct{}
}

func (f *testAPI) Doors(ctx context.Context, s qinlin.Session) ([]qinlin.Door, error) {
	f.discoveries.Add(1)
	if f.discoveryStarted != nil {
		close(f.discoveryStarted)
		select {
		case <-f.discoveryRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.discoveryFailure != "" {
		return nil, &qinlin.Error{Kind: f.discoveryFailure}
	}
	return f.Mock.Doors(ctx, s)
}

func (f *testAPI) Open(ctx context.Context, s qinlin.Session, d qinlin.Door) error {
	f.calls.Add(1)
	if f.started != nil {
		close(f.started)
		<-f.release
	}
	if f.fail != "" {
		return &qinlin.Error{Kind: f.fail}
	}
	return nil
}

type setup struct {
	app           *Server
	handler       http.Handler
	db            *store.Store
	fake          *testAPI
	admin, secret string
	doors         []store.Door
	logs          *bytes.Buffer
}

func newSetup(t *testing.T) *setup {
	t.Helper()
	cfg := config.Config{PublicURL: "http://localhost:8080", Key: bytes.Repeat([]byte{9}, 32), AdminToken: strings.Repeat("a", 64), Cooldown: time.Second, UpstreamTimeout: time.Second}
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"), cfg.Key, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.ReplaceAccount(qinlin.Session{SessionID: "private-upstream-session", Owner: "one"}, []qinlin.Door{{CommunityID: "1", DoorID: "11", Name: "Front"}, {CommunityID: "1", DoorID: "22", Name: "Side"}}); err != nil {
		t.Fatal(err)
	}
	doors, err := db.Doors()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range doors {
		if err = db.EnableDoor(d.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	_, secret, err := db.CreateToken("iPhone", doors[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	fake := &testAPI{}
	logs := &bytes.Buffer{}
	app := New(cfg, db, fake, slog.New(slog.NewTextHandler(logs, nil)))
	return &setup{app: app, handler: app.Handler(), db: db, fake: fake, admin: cfg.AdminToken, secret: secret, doors: doors, logs: logs}
}
func (x *setup) request(method, path, body, token, id string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	if id != "" {
		r.Header.Set("Idempotency-Key", id)
	}
	w := httptest.NewRecorder()
	x.handler.ServeHTTP(w, r)
	return w
}
func TestOpenValidationAndScope(t *testing.T) {
	x := newSetup(t)
	path := "/api/v1/doors/" + x.doors[0].ID + "/open"
	for _, tc := range []struct {
		name, method, path, body, token string
		status                          int
	}{
		{"anonymous", "POST", path, `{}`, "", 401}, {"GET", "GET", path, "", x.secret, 405}, {"HEAD", "HEAD", path, "", x.secret, 405},
		{"malformed", "POST", path, `{`, x.secret, 400}, {"null", "POST", path, `null`, x.secret, 400}, {"array", "POST", path, `[]`, x.secret, 400}, {"unknown field", "POST", path, `{"door":"something"}`, x.secret, 400}, {"trailing object", "POST", path, `{} {}`, x.secret, 400},
		{"wrong door", "POST", "/api/v1/doors/" + x.doors[1].ID + "/open", `{}`, x.secret, 403}, {"unknown door", "POST", "/api/v1/doors/unknown-door/open", `{}`, x.admin, 403},
		{"query token", "POST", path + "?token=secret", `{}`, x.secret, 400}, {"shortcut admin access", "GET", "/api/v1/tokens", "", x.secret, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := x.request(tc.method, tc.path, tc.body, tc.token, "")
			if w.Code != tc.status {
				t.Fatalf("got %d: %s", w.Code, w.Body)
			}
		})
	}
	if x.fake.calls.Load() != 0 {
		t.Fatal("invalid request dispatched")
	}
	w := x.request("POST", path, `{}`, x.secret, "same-request-0001")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = x.request("POST", path, `{}`, x.secret, "same-request-0001")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"replayed":true`) {
		t.Fatal(w.Code, w.Body)
	}
	if x.fake.calls.Load() != 1 {
		t.Fatal("duplicate dispatched")
	}
	w = x.request("POST", path, `{}`, x.secret, "new-request-0002")
	if w.Code != 429 || x.fake.calls.Load() != 1 {
		t.Fatal(w.Code, w.Body)
	}
	for _, secret := range []string{x.secret, x.admin, "private-upstream-session"} {
		if strings.Contains(x.logs.String(), secret) || strings.Contains(w.Body.String(), secret) {
			t.Fatal("credential leaked")
		}
	}
}
func TestCookieCSRFAndLogout(t *testing.T) {
	x := newSetup(t)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"token":"`+x.admin+`"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost:8080")
	w := httptest.NewRecorder()
	x.handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie protection missing")
	}
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &login)
	if login.CSRF == "" {
		t.Fatal("missing CSRF")
	}
	path := "/api/v1/doors/" + x.doors[0].ID + "/open"
	for _, tc := range []struct {
		origin, csrf string
		status       int
	}{{"", "", 403}, {"https://evil.example", login.CSRF, 403}, {"http://localhost:8080", "wrong", 403}, {"http://localhost:8080", login.CSRF, 200}} {
		r = httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.AddCookie(cookies[0])
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-CSRF-Token", tc.csrf)
		w = httptest.NewRecorder()
		x.handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("got %d: %s", w.Code, w.Body)
		}
	}
	if x.fake.calls.Load() != 1 {
		t.Fatal("CSRF bypass")
	}
	r = httptest.NewRequest("POST", "/api/v1/auth/logout", strings.NewReader(`{}`))
	r.AddCookie(cookies[0])
	r.Header.Set("Origin", "http://localhost:8080")
	r.Header.Set("X-CSRF-Token", login.CSRF)
	w = httptest.NewRecorder()
	x.handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("GET", "/api/v1/status", nil)
	r.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	x.handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("logout did not revoke session")
	}
}
func TestUnknownAndExpiredNeverReplay(t *testing.T) {
	for _, kind := range []string{"unavailable", "protocol", "expired", "denied"} {
		t.Run(kind, func(t *testing.T) {
			x := newSetup(t)
			x.fake.fail = kind
			path := "/api/v1/doors/" + x.doors[0].ID + "/open"
			first := x.request("POST", path, `{}`, x.secret, "failure-request-1")
			second := x.request("POST", path, `{}`, x.secret, "failure-request-1")
			if first.Code < 400 || second.Code != first.Code || x.fake.calls.Load() != 1 {
				t.Fatal(first.Code, second.Code, x.fake.calls.Load())
			}
			if kind == "expired" {
				a, _ := x.db.Account()
				if a.State != "expired" {
					t.Fatal(a.State)
				}
			}
		})
	}
}
func TestConcurrentDuplicate(t *testing.T) {
	x := newSetup(t)
	x.fake.started = make(chan struct{})
	x.fake.release = make(chan struct{})
	path := "/api/v1/doors/" + x.doors[0].ID + "/open"
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := x.request("POST", path, `{}`, x.secret, "parallel-request-1")
		if w.Code != 200 {
			t.Errorf("first status %d", w.Code)
		}
	}()
	<-x.fake.started
	w := x.request("POST", path, `{}`, x.secret, "parallel-request-1")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	close(x.fake.release)
	wg.Wait()
	if x.fake.calls.Load() != 1 {
		t.Fatal("duplicate dispatch")
	}
}
func TestHealthReadOnlyAndRevocation(t *testing.T) {
	x := newSetup(t)
	for _, path := range []string{"/", "/healthz", "/app.js", "/app.css", "/request-id.js"} {
		w := x.request("GET", path, "", "", "")
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache protection missing")
		}
	}
	if x.fake.calls.Load() != 0 {
		t.Fatal("read side effect")
	}
	if err := x.db.EnableDoor(x.doors[0].ID, false); err != nil {
		t.Fatal(err)
	}
	w := x.request("POST", "/api/v1/doors/"+x.doors[0].ID+"/open", `{}`, x.secret, "")
	if w.Code != 401 || x.fake.calls.Load() != 0 {
		t.Fatal(w.Code)
	}
}
func TestShortcutOverActualHTTP(t *testing.T) {
	x := newSetup(t)
	server := httptest.NewServer(x.handler)
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/doors/"+x.doors[0].ID+"/open", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+x.secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"ok":true`)) || x.fake.calls.Load() != 1 {
		t.Fatal(resp.StatusCode, string(b))
	}
}

func TestIncompleteReLoginBlocksPreviousSession(t *testing.T) {
	x := newSetup(t)
	x.fake.discoveryFailure = "unavailable"
	w := x.request("POST", "/api/v1/account/login", `{"phone":"13800138000","code":"123456"}`, x.admin, "")
	if w.Code != 502 {
		t.Fatal(w.Code, w.Body)
	}
	a, err := x.db.Account()
	if err != nil || a.State != "relogin_required" {
		t.Fatal(a.State, err)
	}
	w = x.request("POST", "/api/v1/doors/"+x.doors[0].ID+"/open", `{}`, x.secret, "")
	if w.Code != 409 || x.fake.calls.Load() != 0 {
		t.Fatal("old account still dispatched", w.Code, x.fake.calls.Load())
	}
}

func TestPasswordLoginAndCredentialRotation(t *testing.T) {
	x := newSetup(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("old-admin-password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{PublicURL: "http://localhost:8080", AdminToken: x.admin, AdminUsername: "admin_old", AdminPasswordHash: string(hash)}
	login := func(handler http.Handler, username, password, token string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"username": username, "password": password, "token": token})
		r := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", cfg.PublicURL)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	handler := New(cfg, x.db, x.fake, slog.New(slog.NewTextHandler(x.logs, nil))).Handler()
	if w := login(handler, "admin_old", "old-admin-password", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := login(handler, "admin_old", "incorrect-password", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := login(handler, "another_admin", "old-admin-password", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte("new-admin-password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AdminUsername = "admin_new"
	cfg.AdminPasswordHash = string(newHash)
	cfg.AdminToken = strings.Repeat("b", 64)
	handler = New(cfg, x.db, x.fake, slog.New(slog.NewTextHandler(x.logs, nil))).Handler()
	if w := login(handler, "admin_old", "old-admin-password", ""); w.Code != 401 {
		t.Fatal("old password survived rotation")
	}
	if w := login(handler, "", "", x.admin); w.Code != 401 {
		t.Fatal("old management token survived rotation")
	}
	if w := login(handler, "admin_new", "new-admin-password", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if _, err = x.db.Authenticate(x.secret); err != nil {
		t.Fatal("management rotation revoked the door token")
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(page.Body.String(), `id="admin-username"`) || strings.Contains(page.Body.String(), "new-admin-password") {
		t.Fatal("password login page invalid")
	}
}

func TestHTTPOriginLoginAndOpen(t *testing.T) {
	x := newSetup(t)
	origin := "http://203.0.113.10:8080"
	app := New(config.Config{PublicURL: origin, AdminToken: x.admin, Cooldown: time.Second, UpstreamTimeout: time.Second}, x.db, x.fake, slog.New(slog.NewTextHandler(x.logs, nil)))
	handler := app.Handler()
	pageRequest := httptest.NewRequest("GET", origin+"/", nil)
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if !strings.Contains(pageResponse.Body.String(), `name="qinlin-service-url" content="`+origin+`"`) || !strings.Contains(pageResponse.Body.String(), `id="service-link" href="`+origin+`"`) {
		t.Fatal("page must provide its actual configured service address")
	}
	r := httptest.NewRequest("POST", origin+"/api/v1/auth/login", strings.NewReader(`{"token":"`+x.admin+`"}`))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("HTTP session cookie cannot be used")
	}
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HTTP must not force HTTPS")
	}
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", origin+"/api/v1/doors/"+x.doors[0].ID+"/open", strings.NewReader(`{}`))
	r.AddCookie(cookies[0])
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", login.CSRF)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || x.fake.calls.Load() != 1 {
		t.Fatal(w.Code, w.Body)
	}
}
