package server

import (
	"errors"
	"net/http"
	"runtime"
	"time"

	"qinlin/internal/update"
	"qinlin/internal/version"
)

func (s *Server) SetUpdater(manager *update.Manager) { s.updater = manager }
func (s *Server) isRestarting() bool {
	if !s.restarting.Load() {
		return false
	}
	if s.updater != nil && s.updater.Status().State != "restarting" {
		s.restarting.Store(false)
		return false
	}
	return true
}
func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request, p principal) {
	status := update.Status{Current: version.Current(), Platform: runtime.GOOS + "/" + runtime.GOARCH, State: "idle"}
	if s.updater != nil {
		status = s.updater.Status()
	}
	write(w, 200, map[string]any{"ok": true, "update": status})
}
func updateError(w http.ResponseWriter, err error) {
	code := "update_error"
	switch {
	case errors.Is(err, update.ErrBusy):
		code = "update_busy"
	case errors.Is(err, update.ErrNotReady):
		code = "update_not_ready"
	case errors.Is(err, update.ErrUnsupported):
		code = "update_not_managed"
	}
	writeError(w, 409, code)
}
func (s *Server) updateAction(w http.ResponseWriter, r *http.Request, p principal) {
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	if s.updater == nil {
		updateError(w, update.ErrUnsupported)
		return
	}
	var err error
	switch r.PathValue("action") {
	case "check":
		if !s.allow("updates_check", 2, time.Minute) {
			writeError(w, 429, "rate_limited")
			return
		}
		err = s.updater.BeginCheck()
	case "download":
		err = s.updater.BeginDownload()
	case "restart":
		if !s.checkMu.TryLock() {
			writeError(w, 409, "account_check_busy")
			return
		}
		defer s.checkMu.Unlock()
		if !s.accountMu.TryLock() {
			writeError(w, 409, "account_check_busy")
			return
		}
		defer s.accountMu.Unlock()
		err = s.updater.RequestRestart()
		if err == nil {
			s.restarting.Store(true)
		}
	default:
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		updateError(w, err)
		return
	}
	write(w, 202, map[string]any{"ok": true, "update": s.updater.Status()})
}
func (s *Server) setUpdatePreferences(w http.ResponseWriter, r *http.Request, p principal) {
	var body struct {
		AutoCheck    *bool `json:"auto_check"`
		AutoDownload *bool `json:"auto_download"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AutoCheck == nil || body.AutoDownload == nil {
		writeError(w, 400, "invalid_update_settings")
		return
	}
	if s.updater == nil {
		updateError(w, update.ErrUnsupported)
		return
	}
	if err := s.updater.SetPreferences(update.Preferences{AutoCheck: *body.AutoCheck, AutoDownload: *body.AutoDownload}); err != nil {
		writeError(w, 500, "storage_error")
		return
	}
	s.getUpdates(w, r, p)
}
