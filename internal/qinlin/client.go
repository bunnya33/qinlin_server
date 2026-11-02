// Protocol adapted from QinlinOpen and cross-checked with qinlin-remote-open.
// See THIRD_PARTY_NOTICES.md for pinned references and MIT notices.
package qinlin

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5" // Required by the upstream wire protocol, not used for local authentication.
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Session struct {
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id,omitempty"`
	// Owner is a local HMAC fingerprint, never sent to the upstream service.
	Owner string `json:"owner,omitempty"`
}
type Door struct {
	CommunityID   string `json:"community_id"`
	CommunityName string `json:"community_name"`
	DoorID        string `json:"upstream_door_id"`
	Name          string `json:"name"`
}
type API interface {
	SendSMS(context.Context, string) error
	Login(context.Context, string, string) (Session, error)
	Doors(context.Context, Session) ([]Door, error)
	Open(context.Context, Session, Door) error
}

// Error contains only a local category, never the upstream URL/body/message.
type Error struct{ Kind string }

func (e *Error) Error() string { return "qinlin: " + e.Kind }
func Kind(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return "unavailable"
}

type Client struct {
	http              *http.Client
	base, sms, device string
}

func New(device string, timeout time.Duration) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	// A dedicated, non-reused connection prevents Transport replay on stale
	// connections. No Idempotency-Key is forwarded and redirects are never followed.
	tr.DisableKeepAlives = true
	return &Client{http: &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: "https://mobileapi3.qinlinkeji.com/api", sms: "https://gateway2.qinlinkeji.com/member/sms/sendSecurityCode", device: device}
}

func (c *Client) SendSMS(ctx context.Context, phone string) error {
	ts, n := strconv.FormatInt(time.Now().UnixMilli(), 10), digits(4)
	headers := map[string]string{"appid": "gbDjIZQSOpCMX49P", "version": "v2", "timestamp": ts, "nonce": n}
	headers["sign"] = digest("appid=gbDjIZQSOpCMX49P&mobile=" + phone + "&nonce=" + n + "&timestamp=" + ts + "&version=v2&appsecret=OKFoQ9MmNXQtcyXROo4PnaFkfDPTHuDg")
	body, _ := json.Marshal(map[string]string{"mobile": phone})
	_, err := c.request(ctx, c.sms, "application/json; charset=utf-8", body, headers, false)
	return err
}
func (c *Client) Login(ctx context.Context, phone, code string) (Session, error) {
	data, err := c.jsonPost(ctx, "app/v1/login", "", map[string]any{"mobile": encryptPhone(phone), "smsCode": code, "appChannel": 1})
	if err != nil {
		return Session{}, err
	}
	s := Session{SessionID: find(data, "sessionId", "sessionID", "token"), UserID: find(data, "userId", "uid")}
	if s.SessionID == "" {
		return s, &Error{"protocol"}
	}
	return s, nil
}
func (c *Client) Doors(ctx context.Context, s Session) ([]Door, error) {
	data, err := c.jsonPost(ctx, "app/user/v2/communityInfo", s.SessionID, map[string]any{"method": "communityInfo"})
	if err != nil {
		return nil, err
	}
	communities, seen, result := objects(data, "communityId"), map[string]bool{}, []Door{}
	if len(communities) == 0 && !isEmptyCollection(data) {
		return nil, &Error{"protocol"}
	}
	for _, community := range communities {
		id := value(community["communityId"])
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		name := first(community, "communityName", "name", "communityShortName", "projectName")
		fields := sign(map[string]any{"communityId": numeric(id)}, s.SessionID)
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		for k, v := range fields {
			if err := writer.WriteField(k, fmt.Sprint(v)); err != nil {
				return nil, &Error{"protocol"}
			}
		}
		if err := writer.Close(); err != nil {
			return nil, &Error{"protocol"}
		}
		data, err := c.request(ctx, c.base+"/app/user/v2/queryUserDoorByCacheNew?"+encode(map[string]any{"sessionId": s.SessionID}), writer.FormDataContentType(), buf.Bytes(), nil, false)
		if err != nil {
			return nil, err
		}
		items := objects(data, "doorControlId")
		if len(items) == 0 && !isEmptyCollection(data) {
			return nil, &Error{"protocol"}
		}
		for _, item := range items {
			doorID := value(item["doorControlId"])
			if doorID == "" {
				return nil, &Error{"protocol"}
			}
			doorName := first(item, "doorControlName", "doorName", "name", "deviceName", "remarkName")
			if doorName == "" {
				doorName = "门禁 " + doorID
			}
			communityName := first(item, "communityName", "projectName")
			if communityName == "" {
				communityName = name
			}
			result = append(result, Door{CommunityID: id, CommunityName: communityName, DoorID: doorID, Name: doorName})
		}
	}
	return result, nil
}
func (c *Client) Open(ctx context.Context, s Session, d Door) error {
	fields := sign(map[string]any{"appChannel": 1, "doorControlId": numeric(d.DoorID), "communityId": numeric(d.CommunityID)}, s.SessionID)
	fields["sessionId"] = s.SessionID
	_, err := c.request(ctx, c.base+"/open/doorcontrol/v2/open?"+encode(fields), "application/json; charset=utf-8", nil, nil, true)
	return err
}
func (c *Client) jsonPost(ctx context.Context, endpoint, session string, fields map[string]any) (any, error) {
	body, _ := json.Marshal(sign(fields, session))
	return c.request(ctx, c.base+"/"+endpoint+"?"+encode(map[string]any{"sessionId": session}), "application/json; charset=utf-8", body, nil, false)
}
func (c *Client) request(ctx context.Context, endpoint, contentType string, body []byte, headers map[string]string, opening bool) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &Error{"protocol"}
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", contentType)
	for k, v := range map[string]string{"openid": c.device, "User-Agent": "Dart/3.8 (dart:io)", "qversioncode": "3146", "qchannel": "google", "qplatform": "0", "qvendor": "generic"} {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{"unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, &Error{"expired"}
	}
	if resp.StatusCode == 403 {
		return nil, &Error{"denied"}
	}
	if resp.StatusCode == 429 {
		return nil, &Error{"rate_limited"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{"unavailable"}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(b) > 1<<20 {
		return nil, &Error{"protocol"}
	}
	return parse(b, opening)
}

func parse(b []byte, opening bool) (any, error) {
	var env map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil || env == nil {
		return nil, &Error{"protocol"}
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, &Error{"protocol"}
	}
	code, hasCode := env["code"]
	codeOK := value(code) == "0" || value(code) == "200"
	success, hasSuccess := env["success"]
	if hasSuccess {
		if _, ok := success.(bool); !ok {
			return nil, &Error{"protocol"}
		}
	}
	message := strings.TrimSpace(value(env["message"]))
	// Reject contradictory envelopes and all unrecognised business error codes.
	if (hasCode && !codeOK) || (hasSuccess && success != true) {
		msg := strings.ToLower(message)
		if strings.Contains(msg, "session") && (strings.Contains(msg, "expired") || strings.Contains(msg, "invalid")) || strings.Contains(msg, "未登录") || strings.Contains(msg, "登录失效") || strings.Contains(msg, "登录已失效") || strings.Contains(msg, "重新登录") || strings.Contains(msg, "登录过期") {
			return nil, &Error{"expired"}
		}
		if strings.Contains(msg, "无权限") || strings.Contains(msg, "没有权限") || strings.Contains(msg, "权限不足") {
			return nil, &Error{"denied"}
		}
		return nil, &Error{"rejected"}
	}
	if !hasCode && !hasSuccess {
		return nil, &Error{"protocol"}
	}
	if opening {
		// Pinned Python reference accepts precisely code=0, message=success.
		// Other envelopes remain unknown until confirmed with a real account.
		if value(code) != "0" || message != "success" {
			return nil, &Error{"protocol"}
		}
	}
	return normalize(env["data"]), nil
}

func sign(fields map[string]any, token string) map[string]any {
	v := map[string]any{}
	for k, x := range fields {
		v[k] = x
	}
	v["nonce"], v["timestamp"], v["token"], v["version"] = digits(5), time.Now().UnixMilli(), token, "5.2.6"
	sig := digest(encode(v) + "&key=qiAnlPinP")
	delete(v, "token")
	v["sign"] = sig
	return v
}
func encode(v map[string]any) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(v))
	escape := func(s string) string {
		s = url.QueryEscape(s)
		s = strings.ReplaceAll(s, "%3A", ":")
		s = strings.ReplaceAll(s, "%2A", "*")
		return strings.ReplaceAll(s, "~", "%7E")
	}
	for _, k := range keys {
		parts = append(parts, escape(k)+"="+escape(fmt.Sprint(v[k])))
	}
	return strings.Join(parts, "&")
}
func digest(s string) string {
	b := md5.Sum([]byte(s))
	return strings.ToUpper(hex.EncodeToString(b[:]))
}
func encryptPhone(phone string) string {
	k, _ := hex.DecodeString("FBC213C4C7BEEBD2AA4EDBF0F681C41B")
	block, _ := aes.NewCipher(k)
	b := []byte(phone)
	pad := aes.BlockSize - len(b)%aes.BlockSize
	b = append(b, bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(b))
	for i := 0; i < len(b); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], b[i:i+aes.BlockSize])
	}
	return strings.ToUpper(hex.EncodeToString(out))
}
func digits(n int) string {
	b := make([]byte, n)
	for i := range b {
		x, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			panic("secure random unavailable")
		}
		b[i] = '0' + byte(x.Int64())
	}
	return string(b)
}
func numeric(s string) any {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return s
}
func value(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		return ""
	}
}
func normalize(v any) any {
	if s, ok := v.(string); ok {
		var out any
		d := json.NewDecoder(strings.NewReader(s))
		d.UseNumber()
		if d.Decode(&out) == nil {
			return out
		}
	}
	return v
}
func objects(root any, key string) []map[string]any {
	var out []map[string]any
	var visit func(any, int)
	visit = func(v any, depth int) {
		if depth > 32 {
			return
		}
		switch x := normalize(v).(type) {
		case map[string]any:
			if value(x[key]) != "" {
				out = append(out, x)
			}
			for _, v := range x {
				visit(v, depth+1)
			}
		case []any:
			for _, v := range x {
				visit(v, depth+1)
			}
		}
	}
	visit(root, 0)
	return out
}
func first(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := value(m[k]); s != "" {
			return s
		}
	}
	return ""
}
func find(root any, keys ...string) string {
	for _, k := range keys {
		if items := objects(root, k); len(items) > 0 {
			return value(items[0][k])
		}
	}
	return ""
}
func isEmptyCollection(v any) bool {
	switch x := normalize(v).(type) {
	case []any:
		return len(x) == 0
	case map[string]any:
		for _, v := range x {
			if isEmptyCollection(v) {
				return true
			}
		}
	}
	return false
}
