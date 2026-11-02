package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"qinlin/internal/qinlin"
)

func TestAccountCheckScheduleAndPreferences(t *testing.T) {
	x := newSetup(t)
	status, err := x.app.accountCheckStatus()
	if err != nil || status.IntervalMinutes != 15 {
		t.Fatal(status, err)
	}
	x.app.checkAccountIfDue(context.Background(), time.Now())
	if x.fake.discoveries.Load() != 0 {
		t.Fatal("queried before the interval elapsed")
	}
	for _, body := range []string{`{}`, `{"interval_minutes":null}`, `{"interval_minutes":1}`, `{"interval_minutes":1441}`, `{"interval_minutes":5.5}`} {
		if w := x.request("PUT", "/api/v1/account/check", body, x.admin, ""); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := x.request("PUT", "/api/v1/account/check", `{"interval_minutes":5}`, x.secret, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := x.request("PUT", "/api/v1/account/check", `{"interval_minutes":5}`, x.admin, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var interval int
	if err = x.db.Setting("account_check_interval", &interval); err != nil || interval != 5 {
		t.Fatal(interval, err)
	}
	x.app.checkAccountIfDue(context.Background(), time.Now().Add(6*time.Minute))
	if x.fake.discoveries.Load() != 1 || x.fake.calls.Load() != 0 {
		t.Fatal("check did not use discovery exclusively")
	}
	x.fake.discoveryFailure = "expired"
	if err = x.app.checkAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err = x.app.accountCheckStatus()
	if err != nil || status.Code != "expired" || !status.Paused || status.NextCheckAt != 0 {
		t.Fatal(status, err)
	}
	before := x.fake.discoveries.Load()
	x.app.checkAccountIfDue(context.Background(), time.Now().Add(48*time.Hour))
	if before != x.fake.discoveries.Load() {
		t.Fatal("continued querying an expired session")
	}
}

func TestAccountCheckNetworkFailureIsNotExpiry(t *testing.T) {
	x := newSetup(t)
	x.fake.discoveryFailure = "unavailable"
	w := x.request("POST", "/api/v1/account/check", `{}`, x.admin, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var body struct {
		Check checkStatus `json:"check"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	a, err := x.db.Account()
	if err != nil || a.State != "unknown" || a.Session.SessionID == "" || body.Check.Code != "unavailable" || body.Check.Paused {
		t.Fatal(a.State, body, err)
	}
	doors, err := x.db.Doors()
	if err != nil || len(doors) != 2 || !doors[0].Available {
		t.Fatal(doors, err)
	}
	if w = x.request("PUT", "/api/v1/account/check", `{"interval_minutes":0}`, x.admin, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	before := x.fake.discoveries.Load()
	x.app.checkAccountIfDue(context.Background(), time.Now().Add(48*time.Hour))
	if before != x.fake.discoveries.Load() {
		t.Fatal("disabled checker still queried upstream")
	}
}

func TestOldCheckCannotOverwriteNewAccount(t *testing.T) {
	x := newSetup(t)
	x.fake.discoveryStarted = make(chan struct{})
	x.fake.discoveryRelease = make(chan struct{})
	x.fake.discoveryFailure = "expired"
	done := make(chan error, 1)
	go func() { done <- x.app.checkAccount(context.Background()) }()
	<-x.fake.discoveryStarted
	// Network discovery must not hold the account lock, blocking a new login or a door request.
	if !x.app.accountMu.TryLock() {
		t.Fatal("checker holds the account lock during network IO")
	}
	err := x.db.ReplaceAccount(qinlin.Session{SessionID: "new-session", Owner: "new-owner"}, nil)
	x.app.accountMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	close(x.fake.discoveryRelease)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	a, err := x.db.Account()
	if err != nil || a.State != "valid" || a.Session.SessionID != "new-session" {
		t.Fatal(a.State, err)
	}
	status, err := x.app.accountCheckStatus()
	if err != nil || status.Code == "expired" {
		t.Fatal(status, err)
	}
}

func TestUpdateEndpointsRequireAdministrator(t *testing.T) {
	x := newSetup(t)
	for _, path := range []string{"/api/v1/updates", "/api/v1/account/check"} {
		if w := x.request("GET", path, "", x.secret, ""); w.Code != 403 {
			t.Fatal(path, w.Code)
		}
		if w := x.request("GET", path, "", "", ""); w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	for _, path := range []string{"/api/v1/updates/check", "/api/v1/updates/download", "/api/v1/updates/restart"} {
		if w := x.request("POST", path, `{}`, x.secret, ""); w.Code != 403 {
			t.Fatal(path, w.Code)
		}
	}
	if w := x.request("POST", "/api/v1/updates/restart", `{}`, x.admin, ""); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	if x.app.restarting.Load() {
		t.Fatal("unsupported update locked the service")
	}
}
