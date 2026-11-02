package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"qinlin/internal/qinlin"
)

type checkResult struct {
	CheckedAt int64  `json:"checked_at"`
	Code      string `json:"code"`
}

type checkStatus struct {
	IntervalMinutes int    `json:"interval_minutes"`
	CheckedAt       int64  `json:"checked_at"`
	NextCheckAt     int64  `json:"next_check_at"`
	Code            string `json:"code"`
	Checking        bool   `json:"checking"`
	Paused          bool   `json:"paused"`
}

func (s *Server) accountCheckStatus() (checkStatus, error) {
	status := checkStatus{IntervalMinutes: 15}
	if err := s.store.Setting("account_check_interval", &status.IntervalMinutes); err != nil {
		return status, err
	}
	var result checkResult
	if err := s.store.Setting("account_check_result", &result); err != nil {
		return status, err
	}
	a, err := s.store.Account()
	if err != nil {
		return status, err
	}
	// A newly linked account must not inherit a previous account's check result.
	if result.CheckedAt >= a.LinkedAt {
		status.CheckedAt, status.Code = result.CheckedAt, result.Code
	}
	status.Paused = a.State == "expired" || a.State == "disconnected" || a.State == "relogin_required"
	if status.IntervalMinutes > 0 && !status.Paused {
		last := max(status.CheckedAt, a.VerifiedAt)
		status.NextCheckAt = last + int64(status.IntervalMinutes)*int64(time.Minute/time.Millisecond)
	}
	s.checkStateMu.Lock()
	status.Checking = s.checking
	s.checkStateMu.Unlock()
	return status, nil
}

func (s *Server) getAccountCheck(w http.ResponseWriter, r *http.Request, p principal) {
	status, err := s.accountCheckStatus()
	if err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	write(w, 200, map[string]any{"ok": true, "check": status})
}

func (s *Server) setAccountCheck(w http.ResponseWriter, r *http.Request, p principal) {
	var body struct {
		IntervalMinutes *int `json:"interval_minutes"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.IntervalMinutes == nil || (*body.IntervalMinutes != 0 && (*body.IntervalMinutes < 5 || *body.IntervalMinutes > 1440)) {
		writeError(w, 400, "invalid_check_interval")
		return
	}
	if err := s.store.SaveSetting("account_check_interval", *body.IntervalMinutes); err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	s.getAccountCheck(w, r, p)
}

var errCheckBusy = errors.New("account_check_busy")

// checkAccount only discovers permissions. It never logs in, sends SMS, or opens a door.
func (s *Server) checkAccount(ctx context.Context) error {
	if !s.checkMu.TryLock() {
		return errCheckBusy
	}
	defer s.checkMu.Unlock()
	// Do not delay a door command or apply a result to a replaced account.
	if !s.accountMu.TryRLock() {
		return errCheckBusy
	}
	a, err := s.store.Account()
	s.accountMu.RUnlock()
	if err != nil {
		return err
	}
	if a.Session.SessionID == "" || a.State == "expired" || a.State == "relogin_required" {
		return nil
	}
	s.checkStateMu.Lock()
	s.checking = true
	s.checkStateMu.Unlock()
	defer func() { s.checkStateMu.Lock(); s.checking = false; s.checkStateMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	doors, upstreamErr := s.api.Doors(ctx, a.Session)
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	latest, err := s.store.Account()
	if err != nil {
		return err
	}
	if latest.Session.SessionID != a.Session.SessionID || latest.Session.Owner != a.Session.Owner || latest.LinkedAt != a.LinkedAt || latest.State == "expired" || latest.State == "relogin_required" {
		return nil
	}
	code := "valid"
	if upstreamErr == nil {
		if err = s.store.Refresh(doors); err != nil {
			return err
		}
	} else {
		code = qinlin.Kind(upstreamErr)
		state := "unknown"
		if code == "expired" {
			state = "expired"
		}
		if err = s.store.SetAccountState(state); err != nil {
			return err
		}
	}
	return s.store.SaveSetting("account_check_result", checkResult{CheckedAt: time.Now().UnixMilli(), Code: code})
}

func (s *Server) checkAccountNow(w http.ResponseWriter, r *http.Request, p principal) {
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	if !s.allow("account_check", 2, time.Minute) {
		writeError(w, 429, "rate_limited")
		return
	}
	if err := s.checkAccount(r.Context()); err != nil {
		if errors.Is(err, errCheckBusy) {
			writeError(w, 409, "account_check_busy")
		} else {
			writeError(w, 500, "storage_error")
		}
		return
	}
	s.getAccountCheck(w, r, p)
}

func (s *Server) checkAccountIfDue(ctx context.Context, now time.Time) {
	status, err := s.accountCheckStatus()
	if err != nil {
		s.log.Error("account check settings unavailable")
		return
	}
	if status.NextCheckAt == 0 || now.UnixMilli() < status.NextCheckAt {
		return
	}
	if err = s.checkAccount(ctx); err != nil && !errors.Is(err, errCheckBusy) {
		s.log.Error("account check persistence failed")
	}
}

// RunBackground must be joined before the store is closed.
func (s *Server) RunBackground(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			s.checkAccountIfDue(ctx, now)
		}
	}
}
