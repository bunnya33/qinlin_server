package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"qinlin/internal/qinlin"
)

const mobileSchema = `
CREATE TABLE IF NOT EXISTS mobile_accounts (
  id TEXT PRIMARY KEY, secret BLOB NOT NULL, masked_phone TEXT NOT NULL,
  authorized_at INTEGER NOT NULL, checked_at INTEGER NOT NULL, expired INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS mobile_doors (
  account_id TEXT NOT NULL REFERENCES mobile_accounts(id) ON DELETE CASCADE, id TEXT NOT NULL,
  community_id TEXT NOT NULL, upstream_id TEXT NOT NULL, community TEXT NOT NULL, name TEXT NOT NULL,
  available INTEGER NOT NULL DEFAULT 1, PRIMARY KEY(account_id,id)
);
CREATE TABLE IF NOT EXISTS mobile_devices (
  id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES mobile_accounts(id) ON DELETE CASCADE,
  installation TEXT NOT NULL UNIQUE, hash TEXT NOT NULL UNIQUE, expires_at INTEGER NOT NULL,
  widget_door TEXT, widget_hash TEXT UNIQUE, widget_expires_at INTEGER
);
CREATE TABLE IF NOT EXISTS mobile_shares (
  id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES mobile_accounts(id) ON DELETE CASCADE,
  name TEXT NOT NULL, door_id TEXT NOT NULL, hash TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS mobile_share_account ON mobile_shares(account_id);
`

type MobileAccount struct {
	ID, MaskedPhone         string
	AuthorizedAt, CheckedAt int64
	Expired                 bool
	MobileSecret
}
type MobileSecret struct {
	Phone   string
	Session qinlin.Session
}
type MobilePrincipal struct {
	ID, AccountID, DoorID, Kind string
}
type MobileShare struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	DoorID  string `json:"doorId"`
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

type AdminMobileAccount struct {
	ID           string `json:"id"`
	MaskedPhone  string `json:"masked_phone"`
	AuthorizedAt int64  `json:"authorized_at"`
	CheckedAt    int64  `json:"checked_at"`
	Expired      bool   `json:"expired"`
}

type AdminMobileShare struct {
	ID            string `json:"id"`
	AccountID     string `json:"account_id"`
	Name          string `json:"name"`
	DoorID        string `json:"door_id"`
	DoorName      string `json:"door_name"`
	Community     string `json:"community"`
	DoorAvailable bool   `json:"door_available"`
	Enabled       bool   `json:"enabled"`
}

func (s *Store) AdminMobileOverview() ([]AdminMobileAccount, []AdminMobileShare, error) {
	rows, err := s.db.Query(`
		SELECT a.id, a.masked_phone, a.authorized_at, a.checked_at, a.expired,
			COALESCE(m.id,''), COALESCE(m.name,''), COALESCE(m.door_id,''),
			COALESCE(d.name,''), COALESCE(d.community,''), COALESCE(d.available,0),
			COALESCE(m.enabled,0)
		FROM mobile_accounts a
		LEFT JOIN mobile_shares m ON m.account_id=a.id
		LEFT JOIN mobile_doors d ON d.account_id=a.id AND d.id=m.door_id
		WHERE a.authorized_at>0
		ORDER BY a.authorized_at DESC, a.id, m.rowid DESC`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	accounts := []AdminMobileAccount{}
	shares := []AdminMobileShare{}
	for rows.Next() {
		var a AdminMobileAccount
		var share AdminMobileShare
		if err := rows.Scan(&a.ID, &a.MaskedPhone, &a.AuthorizedAt, &a.CheckedAt, &a.Expired,
			&share.ID, &share.Name, &share.DoorID, &share.DoorName, &share.Community,
			&share.DoorAvailable, &share.Enabled); err != nil {
			return nil, nil, err
		}
		if len(accounts) == 0 || accounts[len(accounts)-1].ID != a.ID {
			accounts = append(accounts, a)
		}
		if share.ID != "" {
			share.AccountID = a.ID
			shares = append(shares, share)
		}
	}
	return accounts, shares, rows.Err()
}

func (s *Store) MobileAccount(id string) (MobileAccount, error) {
	a := MobileAccount{ID: id}
	var secret []byte
	err := s.db.QueryRow("SELECT secret,masked_phone,authorized_at,checked_at,expired FROM mobile_accounts WHERE id=?", id).
		Scan(&secret, &a.MaskedPhone, &a.AuthorizedAt, &a.CheckedAt, &a.Expired)
	if err != nil {
		return a, err
	}
	raw, err := s.unseal(secret, "mobile-account:"+id)
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(raw, &a.MobileSecret)
	return a, err
}

func (s *Store) StartMobileSession(accountID, installation string) (MobilePrincipal, string, error) {
	p := MobilePrincipal{ID: Random(), AccountID: accountID, Kind: "device"}
	token := "qm_" + Random()
	secret, err := s.seal([]byte("{}"), "mobile-account:"+accountID)
	if err != nil {
		return p, "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return p, "", err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO mobile_accounts(id,secret,masked_phone,authorized_at,checked_at)
		VALUES(?,?,'',0,0) ON CONFLICT(id) DO NOTHING`, accountID, secret); err != nil {
		return p, "", err
	}
	now := time.Now()
	if _, err = tx.Exec("DELETE FROM mobile_devices WHERE expires_at<=?", now.UnixMilli()); err != nil {
		return p, "", err
	}
	if err = tx.QueryRow(`INSERT INTO mobile_devices(id,account_id,installation,hash,expires_at)
		VALUES(?,?,?,?,?) ON CONFLICT(installation) DO UPDATE SET id=excluded.id,account_id=excluded.account_id,
		hash=excluded.hash,expires_at=excluded.expires_at,widget_hash=NULL,widget_expires_at=NULL,
		widget_door=CASE WHEN mobile_devices.account_id=excluded.account_id THEN mobile_devices.widget_door END
		RETURNING COALESCE(widget_door,'')`, p.ID, accountID, hash(installation), hash(token),
		now.Add(90*24*time.Hour).UnixMilli()).Scan(&p.DoorID); err != nil {
		return p, "", err
	}
	return p, token, tx.Commit()
}

func (s *Store) BindMobilePhone(p MobilePrincipal, legacyID, phone string, session qinlin.Session, doors []qinlin.Door) error {
	raw, err := json.Marshal(MobileSecret{Phone: phone, Session: session})
	if err != nil {
		return err
	}
	secret, err := s.seal(raw, "mobile-account:"+p.AccountID)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err = tx.Exec(`UPDATE mobile_accounts SET secret=?,masked_phone=?,authorized_at=?,checked_at=?,expired=0 WHERE id=?`,
		secret, phone[:3]+"••••"+phone[7:], now, now, p.AccountID); err != nil {
		return err
	}
	if err = replaceMobileDoors(tx, p.AccountID, doors); err != nil {
		return err
	}
	if legacyID != "" {
		// Only migrate a legacy phone account after the owner verifies its SMS code.
		if _, err = tx.Exec("UPDATE mobile_shares SET account_id=? WHERE account_id=?", p.AccountID, legacyID); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM mobile_accounts WHERE id=?", legacyID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RefreshMobileDoors(accountID string, doors []qinlin.Door) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = replaceMobileDoors(tx, accountID, doors); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE mobile_accounts SET checked_at=? WHERE id=?", time.Now().UnixMilli(), accountID); err != nil {
		return err
	}
	return tx.Commit()
}
func replaceMobileDoors(tx *sql.Tx, accountID string, doors []qinlin.Door) error {
	if _, err := tx.Exec("UPDATE mobile_doors SET available=0 WHERE account_id=?", accountID); err != nil {
		return err
	}
	for _, door := range doors {
		_, err := tx.Exec(`INSERT INTO mobile_doors(account_id,id,community_id,upstream_id,community,name) VALUES(?,?,?,?,?,?)
			ON CONFLICT(account_id,id) DO UPDATE SET community=excluded.community,name=excluded.name,available=1`,
			accountID, doorID(door), door.CommunityID, door.DoorID, door.CommunityName, door.Name)
		if err != nil {
			return err
		}
	}
	_, err := tx.Exec(`UPDATE mobile_devices SET widget_door=NULL,widget_hash=NULL,widget_expires_at=NULL
		WHERE account_id=? AND widget_door IN (SELECT id FROM mobile_doors WHERE account_id=? AND available=0)`, accountID, accountID)
	return err
}

func (s *Store) ExpireMobileAccount(accountID string) error {
	_, err := s.db.Exec("UPDATE mobile_accounts SET expired=1 WHERE id=?", accountID)
	return err
}
func (s *Store) MobileDoors(accountID string) ([]Door, error) {
	rows, err := s.db.Query("SELECT id,name,community,available FROM mobile_doors WHERE account_id=? ORDER BY available DESC,community,name,id", accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	doors := []Door{}
	for rows.Next() {
		var door Door
		if err := rows.Scan(&door.ID, &door.Name, &door.Community, &door.Available); err != nil {
			return nil, err
		}
		door.Enabled = door.Available
		doors = append(doors, door)
	}
	return doors, rows.Err()
}
func (s *Store) MobileDoor(accountID, id string) (qinlin.Door, error) {
	var door qinlin.Door
	err := s.db.QueryRow("SELECT community_id,upstream_id FROM mobile_doors WHERE account_id=? AND id=? AND available=1", accountID, id).
		Scan(&door.CommunityID, &door.DoorID)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrForbidden
	}
	return door, err
}

func (s *Store) AuthenticateMobile(token, kind, shareID string) (MobilePrincipal, error) {
	return authenticateMobile(s.db, token, kind, shareID)
}

func authenticateMobile(q interface{ QueryRow(string, ...any) *sql.Row }, token, kind, shareID string) (MobilePrincipal, error) {
	p := MobilePrincipal{Kind: kind}
	now := time.Now().UnixMilli()
	var row *sql.Row
	switch kind {
	case "device":
		row = q.QueryRow("SELECT id,account_id,COALESCE(widget_door,'') FROM mobile_devices WHERE hash=? AND expires_at>?", hash(token), now)
	case "widget":
		row = q.QueryRow("SELECT id,account_id,widget_door FROM mobile_devices WHERE widget_hash=? AND widget_expires_at>? AND expires_at>?", hash(token), now, now)
	case "share":
		row = q.QueryRow("SELECT id,account_id,door_id FROM mobile_shares WHERE id=? AND hash=? AND enabled=1", shareID, hash(token))
	default:
		return p, sql.ErrNoRows
	}
	err := row.Scan(&p.ID, &p.AccountID, &p.DoorID)
	return p, err
}
func (s *Store) RevokeMobileDevice(p MobilePrincipal) error {
	_, err := s.db.Exec("DELETE FROM mobile_devices WHERE id=? AND account_id=?", p.ID, p.AccountID)
	return err
}
func (s *Store) SetMobileWidget(p MobilePrincipal, door string) (string, int64, error) {
	if door == "" {
		_, err := s.db.Exec("UPDATE mobile_devices SET widget_door=NULL,widget_hash=NULL,widget_expires_at=NULL WHERE id=?", p.ID)
		return "", 0, err
	}
	token := "qw_" + Random()
	expires := time.Now().Add(7 * 24 * time.Hour).UnixMilli()
	_, err := s.db.Exec("UPDATE mobile_devices SET widget_door=?,widget_hash=?,widget_expires_at=? WHERE id=?", door, hash(token), expires, p.ID)
	return token, expires, err
}
func (s *Store) MobileShares(accountID string) ([]MobileShare, error) {
	rows, err := s.db.Query("SELECT id,name,door_id,enabled FROM mobile_shares WHERE account_id=? ORDER BY rowid DESC", accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shares := []MobileShare{}
	for rows.Next() {
		var share MobileShare
		if err := rows.Scan(&share.ID, &share.Name, &share.DoorID, &share.Enabled); err != nil {
			return nil, err
		}
		shares = append(shares, share)
	}
	return shares, rows.Err()
}
func (s *Store) CreateMobileShare(accountID, name, door string) (MobileShare, string, error) {
	share := MobileShare{ID: Random(), Name: name, DoorID: door, Enabled: true}
	token := "qs_" + Random()
	_, err := s.db.Exec("INSERT INTO mobile_shares(id,account_id,name,door_id,hash) VALUES(?,?,?,?,?)", share.ID, accountID, name, door, hash(token))
	return share, token, err
}
func (s *Store) UpdateMobileShare(accountID, id string, enabled *bool) error {
	var res sql.Result
	var err error
	switch {
	case enabled == nil:
		res, err = s.db.Exec("DELETE FROM mobile_shares WHERE account_id=? AND id=?", accountID, id)
	case *enabled:
		res, err = s.db.Exec(`UPDATE mobile_shares SET enabled=1 WHERE account_id=? AND id=? AND
			EXISTS (SELECT 1 FROM mobile_doors d WHERE d.account_id=mobile_shares.account_id AND d.id=mobile_shares.door_id AND d.available=1)`, accountID, id)
	default:
		res, err = s.db.Exec("UPDATE mobile_shares SET enabled=0 WHERE account_id=? AND id=?", accountID, id)
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrForbidden
	}
	return err
}

func (s *Store) ReserveMobile(p MobilePrincipal, token, id, door string, cooldown time.Duration) (Operation, bool, error) {
	return s.reserve("mobile:"+p.AccountID, id, door, cooldown, func(tx *sql.Tx) error {
		latest, err := authenticateMobile(tx, token, p.Kind, p.ID)
		if err != nil {
			return err
		}
		if latest.ID != p.ID || latest.AccountID != p.AccountID || (p.Kind != "device" && latest.DoorID != door) {
			return sql.ErrNoRows
		}
		return nil
	})
}
