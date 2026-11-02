package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// Setting leaves the caller's defaults intact when a value has never been saved.
func (s *Store) Setting(key string, value any) error {
	var data []byte
	err := s.db.QueryRow("SELECT value FROM meta WHERE key=?", "setting:"+key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (s *Store) SaveSetting(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "setting:"+key, data)
	return err
}
