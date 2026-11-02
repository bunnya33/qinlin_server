package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Addr, PublicURL, DB, AdminToken, DeviceID string
	AdminUsername, AdminPasswordHash          string
	Key                                       []byte
	Cooldown, UpstreamTimeout                 time.Duration
	Mock                                      bool
}

// Load accepts simple KEY=value entries; existing process variables take priority.
func Load() (Config, error) {
	values, err := ReadFile(Path())
	if err != nil && !os.IsNotExist(err) {
		return Config{}, err
	}
	get := func(key, fallback string) string {
		if v, ok := os.LookupEnv(key); ok {
			if v != "" {
				return v
			}
			return fallback
		}
		if v := values[key]; v != "" {
			return v
		}
		return fallback
	}
	return parse(get)
}

func parse(get func(string, string) string) (Config, error) {
	c := Config{Addr: get("QINLIN_ADDR", "127.0.0.1:8080"), PublicURL: strings.TrimRight(get("QINLIN_PUBLIC_URL", "http://localhost:8080"), "/"), DB: get("QINLIN_DB", "data/qinlin.db"), AdminToken: get("QINLIN_ADMIN_TOKEN", ""), DeviceID: get("QINLIN_DEVICE_ID", ""), Mock: get("QINLIN_MOCK", "") == "true", AdminUsername: get("QINLIN_ADMIN_USERNAME", ""), AdminPasswordHash: get("QINLIN_ADMIN_PASSWORD_HASH", "")}
	if strings.HasPrefix(c.AdminPasswordHash, "bcrypt:") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(c.AdminPasswordHash, "bcrypt:"))
		if err != nil {
			return c, errors.New("invalid encoded administrator password hash")
		}
		c.AdminPasswordHash = string(decoded)
	}
	if len(c.AdminToken) < 32 {
		return c, errors.New("QINLIN_ADMIN_TOKEN must contain at least 32 characters; run the init command")
	}
	var err error
	c.Key, err = base64.StdEncoding.DecodeString(get("QINLIN_ENCRYPTION_KEY", ""))
	if err != nil || len(c.Key) != 32 {
		return c, errors.New("QINLIN_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	if len(c.DeviceID) < 8 || strings.ContainsAny(c.DeviceID, "\r\n") {
		return c, errors.New("QINLIN_DEVICE_ID must be a stable identifier of at least 8 characters")
	}
	if (c.AdminUsername == "") != (c.AdminPasswordHash == "") {
		return c, errors.New("administrator username and password hash must be configured together")
	}
	if c.AdminPasswordHash != "" {
		if err := ValidateAdminHash(c.AdminUsername, c.AdminPasswordHash); err != nil {
			return c, err
		}
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("QINLIN_PUBLIC_URL must be an origin without a path or query")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return c, errors.New("QINLIN_PUBLIC_URL must use HTTP or HTTPS")
	}
	c.Cooldown, err = time.ParseDuration(get("QINLIN_COOLDOWN", "5s"))
	if err != nil || c.Cooldown < time.Second {
		return c, errors.New("QINLIN_COOLDOWN must be at least 1s")
	}
	c.UpstreamTimeout, err = time.ParseDuration(get("QINLIN_UPSTREAM_TIMEOUT", "8s"))
	if err != nil || c.UpstreamTimeout < time.Second || c.UpstreamTimeout > 30*time.Second {
		return c, errors.New("QINLIN_UPSTREAM_TIMEOUT must be between 1s and 30s")
	}
	return c, nil
}

func Init() error {
	return InitFile(Path(), nil)
}

func InitFile(path string, overrides map[string]string) error {
	secret, key, device := make([]byte, 32), make([]byte, 32), make([]byte, 16)
	for _, b := range [][]byte{secret, key, device} {
		if _, err := rand.Read(b); err != nil {
			return err
		}
	}
	values := map[string]string{"QINLIN_ADDR": "127.0.0.1:8080", "QINLIN_PUBLIC_URL": "http://localhost:8080", "QINLIN_DB": "data/qinlin.db", "QINLIN_ADMIN_TOKEN": hex.EncodeToString(secret), "QINLIN_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(key), "QINLIN_DEVICE_ID": hex.EncodeToString(device), "QINLIN_COOLDOWN": "5s", "QINLIN_UPSTREAM_TIMEOUT": "8s", "QINLIN_MOCK": "false"}
	for k, v := range overrides {
		values[k] = v
	}
	if _, err := ParseFileValues(values); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create configuration (existing files are never overwritten): %w", err)
	}
	writeErr := writeValues(f, values)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func Path() string {
	if v := os.Getenv("QINLIN_CONFIG"); v != "" {
		return v
	}
	return ".env"
}
func ReadFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("invalid configuration entry")
		}
		values[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return values, s.Err()
}
func ParseFileValues(values map[string]string) (Config, error) {
	return parse(func(k, fallback string) string {
		if v := values[k]; v != "" {
			return v
		}
		return fallback
	})
}
