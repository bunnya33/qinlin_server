package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gofrs/flock"
	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,31}$`)

func ValidateAdminHash(username, hash string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("administrator username must be 3-32 letters, digits, dots, underscores or hyphens")
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || cost < 10 || cost > 14 {
		return errors.New("administrator password must use a bcrypt hash with cost 10-14")
	}
	return nil
}
func SetAdmin(path, username, password string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("管理员账号应为 3～32 位字母、数字、点、下划线或连字符")
	}
	if len(password) < 12 || len(password) > 72 || strings.ContainsAny(password, "\r\n\x00") {
		return errors.New("管理员密码应为 12～72 字节，不含换行或空字符")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	token, err := randomHex(32)
	if err != nil {
		return err
	}
	// Encoding the bcrypt hash avoids shell/Compose expansion of its '$' signs.
	return Update(path, map[string]string{"QINLIN_ADMIN_USERNAME": username, "QINLIN_ADMIN_PASSWORD_HASH": "bcrypt:" + base64.StdEncoding.EncodeToString(hash), "QINLIN_ADMIN_TOKEN": token})
}
func ResetAdmin(path string) (string, string, error) {
	suffix, err := randomHex(4)
	if err != nil {
		return "", "", err
	}
	password, err := randomHex(18)
	if err != nil {
		return "", "", err
	}
	username := "admin_" + suffix
	return username, password, SetAdmin(path, username, password)
}
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Update validates a complete candidate, then replaces the file atomically.
// Credentials are never passed through a shell or stored as plaintext.
func Update(path string, changes map[string]string) error {
	lock := flock.New(path + ".edit.lock")
	if err := lock.Lock(); err != nil {
		return err
	}
	defer lock.Close()
	values, err := ReadFile(path)
	if err != nil {
		return err
	}
	for k, v := range changes {
		if strings.ContainsAny(k, "\r\n=") || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid configuration value")
		}
		values[k] = v
	}
	if _, err = ParseFileValues(values); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".qinlin-config-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = preserveMetadata(f, info); err != nil {
		f.Close()
		return err
	}
	if err = writeValues(f, values); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp, path)
}
func writeValues(w io.Writer, values map[string]string) error {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := fmt.Fprintf(w, "%s=%s\n", k, values[k]); err != nil {
			return err
		}
	}
	return nil
}

func SetPort(path string, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("端口范围应为 1～65535")
	}
	values, err := ReadFile(path)
	if err != nil {
		return err
	}
	cfg, err := ParseFileValues(values)
	if err != nil {
		return err
	}
	host, current, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return errors.New("当前监听地址格式不正确")
	}
	newPort := strconv.Itoa(port)
	if current == newPort {
		return nil
	}
	addr := net.JoinHostPort(host, newPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.New("新端口已被占用或当前用户无法绑定该端口")
	}
	if err = listener.Close(); err != nil {
		return err
	}
	u, err := url.Parse(cfg.PublicURL)
	if err != nil {
		return err
	}
	u.Host = net.JoinHostPort(u.Hostname(), newPort)
	return Update(path, map[string]string{"QINLIN_ADDR": addr, "QINLIN_PUBLIC_URL": u.String()})
}
