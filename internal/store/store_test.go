package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"qinlin/internal/qinlin"
)

func fixture(t *testing.T) (*Store, string, []byte, []Door) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	key := bytes.Repeat([]byte{7}, 32)
	s, err := Open(path, key, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.ReplaceAccount(qinlin.Session{SessionID: "secret-test-session", Owner: "account-one"}, []qinlin.Door{{CommunityID: "1", DoorID: "11", Name: "A"}, {CommunityID: "1", DoorID: "22", Name: "B"}}); err != nil {
		t.Fatal(err)
	}
	doors, err := s.Doors()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range doors {
		if err = s.EnableDoor(d.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	return s, path, key, doors
}
func TestReserveDurabilityAndConflict(t *testing.T) {
	s, path, key, doors := fixture(t)
	op, fresh, err := s.Reserve("admin", "request-0001", doors[0].ID, time.Second)
	if err != nil || !fresh {
		t.Fatal(op, fresh, err)
	}
	if _, fresh, err = s.Reserve("admin", "request-0001", doors[0].ID, time.Second); err != nil || fresh {
		t.Fatal("duplicate reserved twice", err)
	}
	if _, _, err = s.Reserve("admin", "request-0001", doors[1].ID, time.Second); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, _, err = s.Reserve("admin", "request-0002", doors[0].ID, time.Second); !errors.Is(err, ErrCooldown) {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, key, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	op, fresh, err = reopened.Reserve("admin", "request-0001", doors[0].ID, time.Second)
	if err != nil || fresh || op.State != "unknown" || op.Code != "server_restarted" {
		t.Fatal(op, fresh, err)
	}
}
func TestConcurrentDoorCooldown(t *testing.T) {
	s, _, _, doors := fixture(t)
	var dispatched atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, fresh, err := s.Reserve("admin", Random(), doors[0].ID, time.Second)
			if err == nil && fresh {
				dispatched.Add(1)
			} else if !errors.Is(err, ErrCooldown) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if dispatched.Load() != 1 {
		t.Fatalf("dispatched %d requests", dispatched.Load())
	}
}
func TestEncryptedSessionAndKeyCheck(t *testing.T) {
	s, path, key, _ := fixture(t)
	token, secret, err := s.CreateToken("phone", mustDoor(t, s))
	if err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err = s.db.QueryRow("SELECT secret FROM account").Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("secret-test-session")) {
		t.Fatal("session stored as plaintext")
	}
	var hashed string
	if err = s.db.QueryRow("SELECT hash FROM tokens WHERE id=?", token.ID).Scan(&hashed); err != nil {
		t.Fatal(err)
	}
	if hashed == secret {
		t.Fatal("plaintext token")
	}
	s.Close()
	bad := append([]byte{}, key...)
	bad[0]++
	if db, err := Open(path, bad, false); err == nil {
		db.Close()
		t.Fatal("wrong encryption key accepted")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(path + suffix)
		if err == nil && (bytes.Contains(b, []byte("secret-test-session")) || bytes.Contains(b, []byte(secret))) {
			t.Fatal("plaintext secret on disk")
		}
	}
}
func mustDoor(t *testing.T, s *Store) string {
	t.Helper()
	doors, err := s.Doors()
	if err != nil || len(doors) == 0 {
		t.Fatal(err)
	}
	return doors[0].ID
}
func TestAccountAndPermissionChanges(t *testing.T) {
	s, _, _, doors := fixture(t)
	_, secret, err := s.CreateToken("phone", doors[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	keys := []qinlin.Door{{CommunityID: "1", DoorID: "11", Name: "A"}, {CommunityID: "1", DoorID: "22", Name: "B"}}
	if err = s.ReplaceAccount(qinlin.Session{SessionID: "new", Owner: "account-one"}, keys); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(secret); err != nil {
		t.Fatal("same account re-login revoked shortcut", err)
	}
	if err = s.Refresh(keys[1:]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(secret); err == nil {
		t.Fatal("removed permission retained token")
	}
	if err = s.ReplaceAccount(qinlin.Session{SessionID: "other", Owner: "account-two"}, keys); err != nil {
		t.Fatal(err)
	}
	all, err := s.Doors()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range all {
		if d.Enabled {
			t.Fatal("new account inherited allowlist")
		}
	}
}
func TestSingleProcessAndModeSwitch(t *testing.T) {
	s, path, key, doors := fixture(t)
	_, secret, err := s.CreateToken("phone", doors[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Open(path, key, false); err == nil {
		second.Close()
		t.Fatal("second process allowed")
	}
	s.Close()
	mock, err := Open(path, key, true)
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	a, err := mock.Account()
	if err != nil || a.State != "disconnected" {
		t.Fatal(a.State, err)
	}
	if _, err = mock.Authenticate(secret); err == nil {
		t.Fatal("real token survived mode switch")
	}
}
