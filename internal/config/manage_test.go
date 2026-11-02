package config

import (
	"bytes"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAdminChangeAndResetPreserveBusinessConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.env")
	if err := InitFile(path, map[string]string{"QINLIN_DB": "/var/lib/qinlin/business.db"}); err != nil {
		t.Fatal(err)
	}
	original, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	password := "A-valid-admin-password-2026"
	if err = SetAdmin(path, "test_admin", password); err != nil {
		t.Fatal(err)
	}
	changed, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changedConfig, err := ParseFileValues(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changed["QINLIN_ADMIN_USERNAME"] != "test_admin" || bcrypt.CompareHashAndPassword([]byte(changedConfig.AdminPasswordHash), []byte(password)) != nil {
		t.Fatal("new credentials do not authenticate")
	}
	if changed["QINLIN_ADMIN_TOKEN"] == original["QINLIN_ADMIN_TOKEN"] {
		t.Fatal("old management token was retained")
	}
	for _, key := range []string{"QINLIN_ENCRYPTION_KEY", "QINLIN_DEVICE_ID", "QINLIN_DB", "QINLIN_PUBLIC_URL"} {
		if changed[key] != original[key] {
			t.Fatal("business configuration changed", key)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(password)) {
		t.Fatal("password written in plaintext")
	}
	username, resetPassword, err := ResetAdmin(path)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resetConfig, err := ParseFileValues(reset)
	if err != nil {
		t.Fatal(err)
	}
	if reset["QINLIN_ADMIN_USERNAME"] != username || len(resetPassword) < 32 || bcrypt.CompareHashAndPassword([]byte(resetConfig.AdminPasswordHash), []byte(resetPassword)) != nil {
		t.Fatal("reset credentials unusable")
	}
	if bcrypt.CompareHashAndPassword([]byte(resetConfig.AdminPasswordHash), []byte(password)) == nil {
		t.Fatal("old password still works")
	}
	for _, key := range []string{"QINLIN_ENCRYPTION_KEY", "QINLIN_DEVICE_ID", "QINLIN_DB"} {
		if reset[key] != original[key] {
			t.Fatal("reset changed business configuration")
		}
	}
}
func TestInvalidAdminUpdateDoesNotChangeConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.env")
	if err := InitFile(path, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	for _, tc := range []struct{ username, password string }{{"bad user", "Strong-password-2026"}, {"admin", "short"}, {"admin", strings.Repeat("x", 73)}} {
		if err := SetAdmin(path, tc.username, tc.password); err == nil {
			t.Fatal("invalid credentials accepted")
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("failed update modified configuration")
		}
	}
}
func TestPortUpdateValidatesOccupancyAndPreservesKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.env")
	if err := InitFile(path, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := ReadFile(path)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = SetPort(path, port); err == nil {
		listener.Close()
		t.Fatal("occupied port accepted")
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err = SetPort(path, port); err != nil {
		t.Fatal(err)
	}
	after, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(after["QINLIN_PUBLIC_URL"])
	if u.Port() != strconv.Itoa(port) || after["QINLIN_ADDR"] != "127.0.0.1:"+strconv.Itoa(port) {
		t.Fatal("origin and listen port differ")
	}
	for _, key := range []string{"QINLIN_ENCRYPTION_KEY", "QINLIN_ADMIN_TOKEN", "QINLIN_DEVICE_ID", "QINLIN_DB"} {
		if after[key] != before[key] {
			t.Fatal("port edit changed a secret")
		}
	}
	for _, invalid := range []int{0, -1, 65536} {
		if SetPort(path, invalid) == nil {
			t.Fatal("invalid port accepted")
		}
	}
}
func TestLoadDoesNotReuseOldFileValues(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err = Update(".env", map[string]string{"QINLIN_PUBLIC_URL": "http://203.0.113.10:9080"}); err != nil {
		t.Fatal(err)
	}
	second, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if second.PublicURL == first.PublicURL || second.PublicURL != "http://203.0.113.10:9080" {
		t.Fatal("previous Load polluted the environment")
	}
}
