package config

import (
	"bytes"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestOriginAndKeyValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("QINLIN_ADMIN_TOKEN", strings.Repeat("x", 32))
	t.Setenv("QINLIN_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	t.Setenv("QINLIN_DEVICE_ID", "stable-device-id")
	t.Setenv("QINLIN_COOLDOWN", "5s")
	t.Setenv("QINLIN_UPSTREAM_TIMEOUT", "8s")
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"http://localhost:8080", true},
		{"http://192.168.1.2:8080", true},
		{"http://203.0.113.10:8080", true},
		{"http://door.example.com", true},
		{"http://[2001:db8::1]:8080", true},
		{"https://door.example.com", true},
		{"ftp://door.example.com", false},
		{"file://door.example.com", false},
		{"https://door.example.com/path", false},
		{"https://user:pass@door.example.com", false},
		{"https://door.example.com?token=bad", false},
		{"https://door.example.com#fragment", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			t.Setenv("QINLIN_PUBLIC_URL", tc.url)
			_, err := Load()
			if (err == nil) != tc.valid {
				t.Fatal(tc.url, err)
			}
		})
	}
	t.Setenv("QINLIN_PUBLIC_URL", "https://door.example.com")
	t.Setenv("QINLIN_ENCRYPTION_KEY", "weak")
	if _, err := Load(); err == nil {
		t.Fatal("weak key accepted")
	}
}
func TestInitNeverOverwritesSecrets(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(".env")
	if err != nil {
		t.Fatal(err)
	}
	if err = Init(); err == nil {
		t.Fatal("existing secrets overwritten")
	}
	after, err := os.ReadFile(".env")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("secrets changed", err)
	}
}
