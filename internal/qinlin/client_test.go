package qinlin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProtocolGoldenVectors(t *testing.T) {
	if got := encryptPhone("13800138000"); got != "38C1CCB03057298251BF91A0E6EA1BF3" {
		t.Fatal(got)
	}
	fields := map[string]any{"appChannel": 1, "communityId": 42, "doorControlId": 99, "nonce": "12345", "timestamp": "1700000000000", "token": "test-session", "version": "5.2.6"}
	if got := digest(encode(fields) + "&key=qiAnlPinP"); got != "ECD8D869F8C943DDFF483EA1258F149A" {
		t.Fatal(got)
	}
	if got := encode(map[string]any{"token": "a:b *~+"}); got != "token=a:b+*%7E%2B" {
		t.Fatal(got)
	}
}
func TestResponseClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		opening          bool
	}{
		{"accepted", `{"code":0,"message":"success"}`, "", true},
		{"sms_without_data", `{"code":0,"message":"success"}`, "", false},
		{"contradiction", `{"code":0,"success":false,"message":"success"}`, "rejected", true},
		{"failed_code_true_flag", `{"code":500,"success":true}`, "rejected", true},
		{"http200_not_business_success", `{"message":"success"}`, "protocol", true},
		{"broad_code", `{"code":201,"message":"success"}`, "rejected", true},
		{"unknown_success_shape", `{"success":true}`, "protocol", true},
		{"different_message", `{"code":0,"message":"opened"}`, "protocol", true},
		{"expired", `{"code":401,"message":"请重新登录"}`, "expired", true},
		{"denied", `{"code":123,"message":"没有权限"}`, "denied", true},
		{"empty", "", "protocol", true},
		{"null", "null", "protocol", true},
		{"trailing_json", `{"code":0,"message":"success"}{}`, "protocol", true},
		{"invalid_flag", `{"code":0,"success":"true","message":"success"}`, "protocol", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse([]byte(tc.body), tc.opening)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || Kind(err) != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}
func TestOpenWireAndNoRedirectReplay(t *testing.T) {
	var calls, targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/open/doorcontrol/v2/open" {
			t.Errorf("unexpected request")
		}
		if r.URL.Query().Get("sessionId") != "super-private-session" || r.URL.Query().Get("communityId") != "42" || r.URL.Query().Get("doorControlId") != "99" {
			t.Errorf("incorrect fields")
		}
		if r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("local credentials leaked upstream")
		}
		if r.Header.Get("openid") != "stable-device" {
			t.Errorf("incorrect device")
		}
		w.Header().Set("Location", target.URL)
		w.WriteHeader(307)
	}))
	defer upstream.Close()
	c := New("stable-device", time.Second)
	c.base = upstream.URL
	err := c.Open(context.Background(), Session{SessionID: "super-private-session"}, Door{CommunityID: "42", DoorID: "99"})
	if err == nil || strings.Contains(err.Error(), "super-private-session") {
		t.Fatalf("unsafe error: %v", err)
	}
	if calls.Load() != 1 || targetCalls.Load() != 0 {
		t.Fatalf("calls=%d target=%d", calls.Load(), targetCalls.Load())
	}
}
func TestOpenTimeoutNotRetried(t *testing.T) {
	var calls atomic.Int32
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(80 * time.Millisecond)
		w.Write([]byte(`{"code":0,"message":"success"}`))
	}))
	defer u.Close()
	c := New("device", 15*time.Millisecond)
	c.base = u.URL
	if err := c.Open(context.Background(), Session{SessionID: "private"}, Door{CommunityID: "1", DoorID: "2"}); err == nil || Kind(err) != "unavailable" {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("request replayed")
	}
}

func TestFullAccountWireFlow(t *testing.T) {
	var sms, login, communities, discovery, opening atomic.Int32
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Error("all upstream requests must use POST")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sms":
			sms.Add(1)
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["mobile"] != "13800138000" || r.Header.Get("appid") == "" || r.Header.Get("sign") == "" {
				t.Error("incorrect SMS wire fields")
			}
			w.Write([]byte(`{"code":0,"message":"success"}`))
		case "/app/v1/login":
			login.Add(1)
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["mobile"] != "38C1CCB03057298251BF91A0E6EA1BF3" || body["smsCode"] != "123456" || body["sign"] == nil {
				t.Error("incorrect login fields")
			}
			if r.URL.Query().Get("sessionId") != "" {
				t.Error("login has stale session")
			}
			w.Write([]byte(`{"code":0,"data":{"sessionId":"session-from-login","userId":42}}`))
		case "/app/user/v2/communityInfo":
			communities.Add(1)
			if r.URL.Query().Get("sessionId") != "session-from-login" {
				t.Error("wrong session")
			}
			w.Write([]byte(`{"code":0,"data":"{\"rows\":[{\"communityId\":123,\"communityName\":\"Home\"}]}"}`))
		case "/app/user/v2/queryUserDoorByCacheNew":
			discovery.Add(1)
			if r.ParseMultipartForm(8192) != nil || r.FormValue("communityId") != "123" || r.FormValue("sign") == "" {
				t.Error("incorrect multipart fields")
			}
			w.Write([]byte(`{"code":0,"data":{"doors":[{"doorControlId":456,"doorControlName":"Front"}]}}`))
		case "/open/doorcontrol/v2/open":
			opening.Add(1)
			if r.URL.Query().Get("doorControlId") != "456" || r.URL.Query().Get("communityId") != "123" || r.URL.Query().Get("sessionId") != "session-from-login" {
				t.Error("incorrect open fields")
			}
			w.Write([]byte(`{"code":0,"message":"success"}`))
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer u.Close()
	c := New("device", time.Second)
	c.base = u.URL
	c.sms = u.URL + "/sms"
	ctx := context.Background()
	if err := c.SendSMS(ctx, "13800138000"); err != nil {
		t.Fatal(err)
	}
	s, err := c.Login(ctx, "13800138000", "123456")
	if err != nil {
		t.Fatal(err)
	}
	doors, err := c.Doors(ctx, s)
	if err != nil || len(doors) != 1 || doors[0].Name != "Front" || doors[0].CommunityName != "Home" {
		t.Fatal(doors, err)
	}
	if err = c.Open(ctx, s, doors[0]); err != nil {
		t.Fatal(err)
	}
	if sms.Load() != 1 || login.Load() != 1 || communities.Load() != 1 || discovery.Load() != 1 || opening.Load() != 1 {
		t.Fatal("unexpected call count")
	}
}
func FuzzParse(f *testing.F) {
	for _, s := range []string{`{"code":0,"message":"success"}`, `null`, `{"success":false}`, `[]`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) { parse(b, true) })
}
