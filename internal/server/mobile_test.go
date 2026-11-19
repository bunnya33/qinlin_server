package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"qinlin/internal/qinlin"
)

func enableMobileTest(t *testing.T, x *setup) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	x.app.cfg.Mobile = true
	x.app.cfg.AdminUsername = "test-admin"
	x.app.cfg.AdminPasswordHash = string(hash)
	x.handler = x.app.Handler()
}

func TestMobileBindingRenewalAndRevocation(t *testing.T) {
	x := newSetup(t)
	enableMobileTest(t, x)
	x.app.cfg.Mock = true

	type reply struct {
		Challenge     string
		Session       string
		Snapshot      mobileSnapshot
		WidgetToken   string
		Authorization string
		Issued        struct {
			Credential string
			URL        string
		}
	}
	request := func(method, path, body, token, id string, status int) reply {
		t.Helper()
		w := x.request(method, mobilePrefix+path, body, token, id)
		if w.Code != status {
			t.Fatalf("%s %s yielded %d, want %d (%s)", method, path, w.Code, status, w.Body)
		}
		var result reply
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	auth := func(phone, device, session string) reply {
		t.Helper()
		body := map[string]string{"installation": device}
		if phone != "" {
			body["phone"] = phone
		}
		raw, _ := json.Marshal(body)
		sms := request("POST", "/sms", string(raw), session, "", 200)
		if sms.Challenge == "" {
			t.Fatal("missing SMS challenge")
		}
		body = map[string]string{"challenge": sms.Challenge, "installation": device, "code": "000000"}
		raw, _ = json.Marshal(body)
		request("POST", "/verify", string(raw), session, "", 409)
		body["code"] = "123456"
		raw, _ = json.Marshal(body)
		result := request("POST", "/verify", string(raw), session, "", 200)
		request("POST", "/verify", string(raw), session, "", 409)
		return result
	}

	const phone = "13800138001"
	const installation = "installation-alpha"
	login := func(device string) reply {
		t.Helper()
		return request("POST", "/login", `{"installation":"`+device+`","username":"test-admin","password":"test-password"}`, "", "", 200)
	}
	request("POST", "/login", `{"installation":"installation-alpha","username":"test-admin","password":"wrong"}`, "", "", 401)
	request("POST", "/sms", `{"installation":"installation-alpha","phone":"13800138001"}`, "", "", 401)
	request("POST", "/verify", `{}`, "", "", 401)
	first := login(installation)
	if first.Session == "" || first.Snapshot.Account.ID != "" {
		t.Fatal("server login must yield a session before binding")
	}
	request("POST", "/doors/fake/open", `{}`, first.Session, "", 409)
	binding := auth(phone, installation, first.Session)
	first.Snapshot = binding.Snapshot
	second := login("installation-beta")
	if first.Snapshot.Account.ID == "" || first.Snapshot.Account.ID != second.Snapshot.Account.ID || len(second.Snapshot.Doors) != 2 {
		t.Fatal("a new device did not restore the server-side binding")
	}
	request("POST", "/sms", `{"installation":"installation-beta","phone":"13800138002"}`, second.Session, "", 409)
	request("GET", "/current", "", x.admin, "", 401)
	if x.request("GET", "/api/v1/status", "", first.Session, "").Code != 401 {
		t.Fatal("mobile credential can use the admin API")
	}

	door := first.Snapshot.Doors[0].ID
	widget := request("PUT", "/widget", `{"doorId":"`+door+`"}`, first.Session, "", 200)
	share := request("POST", "/shares", `{"name":"家人","doorId":"`+door+`"}`, first.Session, "", 201)
	if widget.WidgetToken == "" || share.Issued.Credential == "" || len(share.Snapshot.Shares) != 1 {
		t.Fatal("missing scoped credentials")
	}
	sharePath := "/shares/" + share.Snapshot.Shares[0].ID
	if share.Issued.URL != x.app.cfg.PublicURL+mobilePrefix+sharePath+"/open" {
		t.Fatal("unexpected share URL")
	}
	if current := request("GET", "/current", "", second.Session, "", 200); len(current.Snapshot.Shares) != 1 || current.Snapshot.Account.WidgetDoorID != nil {
		t.Fatal("devices should share the server account while keeping per-device widget selection")
	} else if current.Snapshot.Shares[0].URL != share.Issued.URL {
		t.Fatal("existing shares must expose the original link")
	}
	request("GET", "/current", "", widget.WidgetToken, "", 401)
	request("POST", sharePath+"/open", `{}`, widget.WidgetToken, "", 401)
	request("POST", "/widget/open", `{}`, share.Issued.Credential, "", 401)
	request("GET", "/widget/status", "", first.Session, "", 401)
	if status := request("GET", "/widget/status", "", widget.WidgetToken, "", 200); status.Authorization != "valid" {
		t.Fatal("valid authorization missing from widget status")
	}
	request("POST", "/doors/"+door+"/open", `{}`, first.Session, "open-request-one", 200)
	request("POST", "/widget/open", `{}`, widget.WidgetToken, "open-request-one", 200)
	request("POST", "/widget/open", `{}`, widget.WidgetToken, "open-request-two", 429)
	if x.fake.calls.Load() != 1 {
		t.Fatal("app and widget bypassed shared request deduplication or cooldown")
	}

	request("PATCH", sharePath, `{"enabled":false}`, first.Session, "", 200)
	request("POST", sharePath+"/open", `{}`, share.Issued.Credential, "", 401)
	if err := x.db.ExpireMobileAccount(first.Snapshot.Account.ID); err != nil {
		t.Fatal(err)
	}
	if status := request("GET", "/widget/status", "", widget.WidgetToken, "", 200); status.Authorization != "expired" {
		t.Fatal("widget status did not report expired authorization")
	}
	request("POST", "/widget/open", `{}`, widget.WidgetToken, "open-request-expired", 409)
	delete(x.app.budgets, "mobile-sms:minute:"+x.app.mobileAccountID(phone))
	time.Sleep(time.Millisecond) // SQLite stores the authorization time to millisecond precision.
	renewed := auth("", installation, first.Session)
	if !renewed.Snapshot.Account.AuthorizedAt.After(first.Snapshot.Account.AuthorizedAt) ||
		renewed.Snapshot.Account.WidgetDoorID == nil || *renewed.Snapshot.Account.WidgetDoorID != door ||
		len(renewed.Snapshot.Shares) != 1 || renewed.Snapshot.Shares[0].Enabled {
		t.Fatal("renewal did not retain the widget selection and paused share")
	}
	if status := request("GET", "/widget/status", "", widget.WidgetToken, "", 200); status.Authorization != "valid" {
		t.Fatal("widget status did not reflect renewal")
	}
	request("PATCH", sharePath, `{"enabled":true}`, first.Session, "", 200)
	request("POST", sharePath+"/open", `{}`, share.Issued.Credential, "open-request-one", 200)
	request("DELETE", sharePath, "", first.Session, "", 200)
	request("POST", sharePath+"/open", `{}`, share.Issued.Credential, "", 401)
	request("POST", "/logout", `{}`, first.Session, "", 200)
	request("POST", "/widget/open", `{}`, widget.WidgetToken, "", 401)
	request("GET", "/current", "", first.Session, "", 401)
	request("GET", "/current", "", second.Session, "", 200)
	if strings.Contains(x.logs.String(), widget.WidgetToken) || strings.Contains(x.logs.String(), share.Issued.Credential) {
		t.Fatal("credential leaked to logs")
	}
}

func TestMobileRequiresOptIn(t *testing.T) {
	x := newSetup(t)
	w := x.request("GET", mobilePrefix+"/current", "", "", "")
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestMobileDoorRemovalStopsDispatch(t *testing.T) {
	x := newSetup(t)
	enableMobileTest(t, x)
	accountID := x.app.mobileServerAccountID()
	// A door saved during login disappears from the next upstream lookup.
	session, err := x.fake.Login(t.Context(), "13800138001", "123456")
	if err != nil {
		t.Fatal(err)
	}
	p, token, err := x.db.StartMobileSession(accountID, "install-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := x.db.BindMobilePhone(p, "", "13800138001", session, []qinlin.Door{{CommunityID: "old", DoorID: "removed"}}); err != nil {
		t.Fatal(err)
	}
	doors, err := x.db.MobileDoors(accountID)
	if err != nil || len(doors) != 1 {
		t.Fatal(doors, err)
	}
	widget, _, err := x.db.SetMobileWidget(p, doors[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	w := x.request("POST", mobilePrefix+"/widget/open", `{}`, widget, "open-removed-door")
	if w.Code != 403 || x.fake.calls.Load() != 0 {
		t.Fatal(w.Code, w.Body)
	}
	w = x.request("GET", mobilePrefix+"/current", "", token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"widgetDoorId":null`) {
		t.Fatal(w.Code, w.Body)
	}
	w = x.request("POST", mobilePrefix+"/widget/open", `{}`, widget, "open-removed-door-again")
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
}
