package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
	"qinlin/internal/qinlin"
)

var ErrConflict = errors.New("request_id_conflict")
var ErrCooldown = errors.New("door_cooldown")
var ErrForbidden = errors.New("door_not_allowed")
var ErrNoSession = errors.New("login_required")

type Store struct {
	db   *sql.DB
	aead cipher.AEAD
	lock *flock.Flock
}
type Account struct {
	Session    qinlin.Session
	State      string `json:"state"`
	LinkedAt   int64  `json:"linked_at"`
	VerifiedAt int64  `json:"verified_at"`
}
type Door struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Community string      `json:"community"`
	Enabled   bool        `json:"enabled"`
	Available bool        `json:"available"`
	Upstream  qinlin.Door `json:"-"`
}
type Token struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	DoorID    string `json:"door_id"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Revoked   bool   `json:"revoked"`
}
type Operation struct {
	ID        string `json:"request_id"`
	DoorID    string `json:"door_id"`
	State     string `json:"state"`
	Code      string `json:"code"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func Open(path string, key []byte, mock bool) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	// Recovery is only safe when no other service instance can be dispatching.
	lock := flock.New(path + ".lock")
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errors.New("database is already in use by another qinlin process")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(e error) (*Store, error) { db.Close(); lock.Close(); return nil, e }
	block, err := aes.NewCipher(key)
	if err != nil {
		return fail(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fail(err)
	}
	s := &Store{db: db, aead: aead, lock: lock}
	_, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS account (id INTEGER PRIMARY KEY CHECK(id=1), secret BLOB NOT NULL, state TEXT NOT NULL, linked_at INTEGER NOT NULL, verified_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS doors (id TEXT PRIMARY KEY, community_id TEXT NOT NULL, upstream_id TEXT NOT NULL, community TEXT NOT NULL, name TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 0, available INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS tokens (id TEXT PRIMARY KEY, hash TEXT UNIQUE NOT NULL, name TEXT NOT NULL, door_id TEXT NOT NULL REFERENCES doors(id), created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS operations (scope TEXT NOT NULL, request_id TEXT NOT NULL, door_id TEXT NOT NULL, state TEXT NOT NULL, code TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY(scope,request_id));
CREATE INDEX IF NOT EXISTS operation_door_time ON operations(door_id,created_at);
CREATE INDEX IF NOT EXISTS operation_time ON operations(created_at);`)
	if err != nil {
		return fail(err)
	}
	var sentinel []byte
	err = db.QueryRow("SELECT value FROM meta WHERE key='key_check'").Scan(&sentinel)
	if errors.Is(err, sql.ErrNoRows) {
		sealed, e := s.seal([]byte("qinlin-key-check-v1"), "key-check")
		if e != nil {
			return fail(e)
		}
		_, err = db.Exec("INSERT INTO meta(key,value) VALUES('key_check',?)", sealed)
	} else if err == nil {
		plain, e := s.unseal(sentinel, "key-check")
		if e != nil || string(plain) != "qinlin-key-check-v1" {
			return fail(errors.New("encryption key does not match database"))
		}
	}
	if err != nil {
		return fail(err)
	}
	// A mode switch must never reuse demo sessions or demo shortcut credentials.
	mode := fmt.Sprint(mock)
	var oldMode string
	err = db.QueryRow("SELECT value FROM meta WHERE key='mock'").Scan(&oldMode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	if oldMode != "" && oldMode != mode {
		if err = s.Disconnect(); err != nil {
			return fail(err)
		}
		if _, err = db.Exec("UPDATE tokens SET revoked=1"); err != nil {
			return fail(err)
		}
	}
	if _, err = db.Exec("INSERT INTO meta(key,value) VALUES('mock',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", mode); err != nil {
		return fail(err)
	}
	if _, err = db.Exec("UPDATE operations SET state='unknown',code='server_restarted',updated_at=? WHERE state='pending'", time.Now().UnixMilli()); err != nil {
		return fail(err)
	}
	if err = s.Prune(); err != nil {
		return fail(err)
	}
	return s, nil
}
func (s *Store) Close() error { return errors.Join(s.db.Close(), s.lock.Close()) }
func (s *Store) Ping() error  { return s.db.Ping() }
func (s *Store) Prune() error {
	_, err := s.db.Exec("DELETE FROM operations WHERE created_at<? AND state!='pending'", time.Now().Add(-7*24*time.Hour).UnixMilli())
	return err
}
func (s *Store) seal(plain []byte, purpose string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(purpose)), nil
}
func (s *Store) unseal(b []byte, purpose string) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(b) < n {
		return nil, errors.New("invalid ciphertext")
	}
	return s.aead.Open(nil, b[:n], b[n:], []byte(purpose))
}
func (s *Store) Account() (Account, error) {
	var a Account
	var secret []byte
	err := s.db.QueryRow("SELECT secret,state,linked_at,verified_at FROM account WHERE id=1").Scan(&secret, &a.State, &a.LinkedAt, &a.VerifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		a.State = "disconnected"
		return a, nil
	}
	if err != nil {
		return a, err
	}
	plain, err := s.unseal(secret, "account")
	if err != nil {
		return a, errors.New("cannot decrypt account")
	}
	err = json.Unmarshal(plain, &a.Session)
	return a, err
}

// ReplaceAccount commits login and the complete permission snapshot together.
func (s *Store) ReplaceAccount(session qinlin.Session, doors []qinlin.Door) error {
	old, err := s.Account()
	if err != nil {
		return err
	}
	sameOwner := session.Owner != "" && old.Session.Owner == session.Owner
	b, err := json.Marshal(session)
	if err != nil {
		return err
	}
	secret, err := s.seal(b, "account")
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err = tx.Exec("DELETE FROM meta WHERE key='setting:account_check_result'"); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO account(id,secret,state,linked_at,verified_at) VALUES(1,?,'valid',?,?) ON CONFLICT(id) DO UPDATE SET secret=excluded.secret,state='valid',linked_at=excluded.linked_at,verified_at=excluded.verified_at", secret, now, now); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE doors SET available=0"); err != nil {
		return err
	}
	// Normal re-login retains shortcuts for the same account. Switching accounts
	// requires a fresh explicit allowlist and revokes every old credential.
	if !sameOwner {
		if _, err = tx.Exec("UPDATE doors SET enabled=0"); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE tokens SET revoked=1"); err != nil {
			return err
		}
	}
	if err = upsertDoors(tx, doors); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE doors SET enabled=0 WHERE available=0"); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE tokens SET revoked=1 WHERE door_id IN (SELECT id FROM doors WHERE available=0)"); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Refresh(doors []qinlin.Door) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE doors SET available=0"); err != nil {
		return err
	}
	if err = upsertDoors(tx, doors); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE doors SET enabled=0 WHERE available=0"); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE tokens SET revoked=1 WHERE door_id IN (SELECT id FROM doors WHERE available=0)"); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE account SET state='valid',verified_at=? WHERE id=1", time.Now().UnixMilli()); err != nil {
		return err
	}
	result, _ := json.Marshal(map[string]any{"checked_at": time.Now().UnixMilli(), "code": "valid"})
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES('setting:account_check_result',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", result); err != nil {
		return err
	}
	return tx.Commit()
}
func upsertDoors(tx *sql.Tx, doors []qinlin.Door) error {
	for _, d := range doors {
		h := sha256.Sum256([]byte(d.CommunityID + "\x00" + d.DoorID))
		id := hex.EncodeToString(h[:16])
		if _, err := tx.Exec("INSERT INTO doors(id,community_id,upstream_id,community,name,available) VALUES(?,?,?,?,?,1) ON CONFLICT(id) DO UPDATE SET community=excluded.community,name=excluded.name,available=1", id, d.CommunityID, d.DoorID, d.CommunityName, d.Name); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) SetAccountState(state string) error {
	_, err := s.db.Exec("UPDATE account SET state=? WHERE id=1", state)
	return err
}
func (s *Store) Disconnect() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"DELETE FROM account", "DELETE FROM meta WHERE key='setting:account_check_result'", "UPDATE doors SET enabled=0,available=0", "UPDATE tokens SET revoked=1"} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Doors() ([]Door, error) {
	rows, err := s.db.Query("SELECT id,name,community,enabled,available,community_id,upstream_id FROM doors ORDER BY community,name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Door{}
	for rows.Next() {
		var d Door
		if err = rows.Scan(&d.ID, &d.Name, &d.Community, &d.Enabled, &d.Available, &d.Upstream.CommunityID, &d.Upstream.DoorID); err != nil {
			return nil, err
		}
		d.Upstream.Name = d.Name
		d.Upstream.CommunityName = d.Community
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) Door(id string) (Door, error) {
	var d Door
	err := s.db.QueryRow("SELECT id,name,community,enabled,available,community_id,upstream_id FROM doors WHERE id=?", id).Scan(&d.ID, &d.Name, &d.Community, &d.Enabled, &d.Available, &d.Upstream.CommunityID, &d.Upstream.DoorID)
	return d, err
}
func (s *Store) EnableDoor(id string, enabled bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec("UPDATE doors SET enabled=? WHERE id=? AND available=1", enabled, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrForbidden
	}
	if !enabled {
		if _, err = tx.Exec("UPDATE tokens SET revoked=1 WHERE door_id=?", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func Random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (s *Store) CreateToken(name, door string) (Token, string, error) {
	t := Token{ID: Random(), Name: name, DoorID: door, CreatedAt: time.Now().UnixMilli(), ExpiresAt: time.Now().Add(90 * 24 * time.Hour).UnixMilli()}
	secret := "ql_" + Random()
	res, err := s.db.Exec("INSERT INTO tokens(id,hash,name,door_id,created_at,expires_at) SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM doors WHERE id=? AND enabled=1 AND available=1)", t.ID, hash(secret), t.Name, t.DoorID, t.CreatedAt, t.ExpiresAt, door)
	if err != nil {
		return t, "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return t, "", err
	}
	if n != 1 {
		return t, "", ErrForbidden
	}
	return t, secret, nil
}
func (s *Store) Authenticate(secret string) (Token, error) {
	var t Token
	err := s.db.QueryRow("SELECT id,name,door_id,created_at,expires_at,revoked FROM tokens WHERE hash=? AND revoked=0 AND expires_at>?", hash(secret), time.Now().UnixMilli()).Scan(&t.ID, &t.Name, &t.DoorID, &t.CreatedAt, &t.ExpiresAt, &t.Revoked)
	return t, err
}
func (s *Store) Tokens() ([]Token, error) {
	rows, err := s.db.Query("SELECT id,name,door_id,created_at,expires_at,revoked FROM tokens ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		if err = rows.Scan(&t.ID, &t.Name, &t.DoorID, &t.CreatedAt, &t.ExpiresAt, &t.Revoked); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) RevokeToken(id string) error {
	_, err := s.db.Exec("UPDATE tokens SET revoked=1 WHERE id=?", id)
	return err
}

// Reserve persists a command before it can be dispatched. All checks are in
// one transaction, so competing request IDs cannot bypass the door cooldown.
func (s *Store) Reserve(scope, id, door string, cooldown time.Duration) (Operation, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Operation{}, false, err
	}
	defer tx.Rollback()
	var op Operation
	err = tx.QueryRow("SELECT request_id,door_id,state,code,created_at,updated_at FROM operations WHERE scope=? AND request_id=?", scope, id).Scan(&op.ID, &op.DoorID, &op.State, &op.Code, &op.CreatedAt, &op.UpdatedAt)
	if err == nil {
		if op.DoorID != door {
			return op, false, ErrConflict
		}
		return op, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return op, false, err
	}
	var enabled, available bool
	if err = tx.QueryRow("SELECT enabled,available FROM doors WHERE id=?", door).Scan(&enabled, &available); errors.Is(err, sql.ErrNoRows) {
		return op, false, ErrForbidden
	} else if err != nil {
		return op, false, err
	}
	if !enabled || !available {
		return op, false, ErrForbidden
	}
	var state string
	if err = tx.QueryRow("SELECT state FROM account WHERE id=1").Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return op, false, ErrNoSession
	} else if err != nil {
		return op, false, err
	}
	if state != "valid" && state != "unknown" {
		return op, false, ErrNoSession
	}
	var busy int
	now := time.Now().UnixMilli()
	if err = tx.QueryRow("SELECT COUNT(*) FROM operations WHERE door_id=? AND (state='pending' OR updated_at>?)", door, now-cooldown.Milliseconds()).Scan(&busy); err != nil {
		return op, false, err
	}
	if busy > 0 {
		return op, false, ErrCooldown
	}
	op = Operation{ID: id, DoorID: door, State: "pending", Code: "processing", CreatedAt: now, UpdatedAt: now}
	if _, err = tx.Exec("INSERT INTO operations(scope,request_id,door_id,state,code,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", scope, id, door, op.State, op.Code, now, now); err != nil {
		return op, false, err
	}
	if err = tx.Commit(); err != nil {
		return op, false, err
	}
	return op, true, nil
}
func (s *Store) Finish(scope string, op Operation) error {
	_, err := s.db.Exec("UPDATE operations SET state=?,code=?,updated_at=? WHERE scope=? AND request_id=? AND state='pending'", op.State, op.Code, op.UpdatedAt, scope, op.ID)
	return err
}
func (s *Store) Operations() ([]Operation, error) {
	rows, err := s.db.Query("SELECT request_id,door_id,state,code,created_at,updated_at FROM operations ORDER BY created_at DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		var op Operation
		if err = rows.Scan(&op.ID, &op.DoorID, &op.State, &op.Code, &op.CreatedAt, &op.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}
