package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"qinlin/internal/version"
)

var ErrBusy = errors.New("update_busy")
var ErrUnsupported = errors.New("update_not_managed")
var ErrNotReady = errors.New("update_not_ready")

type Preferences struct {
	AutoCheck    bool `json:"auto_check"`
	AutoDownload bool `json:"auto_download"`
}
type ApplyResult struct {
	State string `json:"state"`
	Tag   string `json:"tag"`
	At    int64  `json:"at"`
}
type Status struct {
	Current        version.Info `json:"current"`
	Platform       string       `json:"platform"`
	Managed        bool         `json:"managed"`
	State          string       `json:"state"`
	Latest         string       `json:"latest"`
	Available      bool         `json:"available"`
	ReleaseURL     string       `json:"release_url"`
	Notes          string       `json:"notes"`
	CheckedAt      int64        `json:"checked_at"`
	Downloaded     int64        `json:"downloaded"`
	Total          int64        `json:"total"`
	Ready          bool         `json:"ready"`
	PendingVersion string       `json:"pending_version"`
	Code           string       `json:"code"`
	Preferences    Preferences  `json:"preferences"`
}
type Manager struct {
	mu        sync.Mutex
	dir       string
	managed   bool
	source    Source
	status    Status
	release   Release
	working   bool
	ctx       context.Context
	restartAt int64
}

func New(dir string, managed bool) (*Manager, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	m := &Manager{dir: dir, managed: managed && runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"), source: NewSource(), ctx: context.Background()}
	m.status.State = "idle"
	m.status.Preferences = Preferences{AutoCheck: m.managed, AutoDownload: m.managed}
	if err := ReadJSON(filepath.Join(dir, "preferences.json"), &m.status.Preferences); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return m, nil
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	var applied ApplyResult
	appliedErr := ReadJSON(filepath.Join(m.dir, "result.json"), &applied)
	if m.status.State == "restarting" && appliedErr == nil && applied.At >= m.restartAt && (applied.State == "update_invalid" || applied.State == "rolled_back") {
		m.status.State = applied.State
	}
	status := m.status
	status.Current = version.Current()
	status.Managed = m.managed
	status.Platform = runtime.GOOS + "/" + runtime.GOARCH
	var pending Candidate
	if ReadJSON(filepath.Join(m.dir, "pending.json"), &pending) == nil && pending.Valid() && Newer(pending.Tag, version.Version) {
		status.Ready = true
		status.PendingVersion = pending.Tag
		if !m.working && status.State != "restarting" {
			status.State = "ready"
		}
	}
	if !m.working && appliedErr == nil && applied.At > status.CheckedAt && !status.Ready {
		status.State = applied.State
		status.PendingVersion = applied.Tag
	}
	return status
}
func (m *Manager) SetPreferences(p Preferences) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := WriteJSON(filepath.Join(m.dir, "preferences.json"), p); err != nil {
		return err
	}
	m.status.Preferences = p
	return nil
}
func (m *Manager) begin(state string) error {
	if m.working || m.status.State == "restarting" {
		return ErrBusy
	}
	m.working = true
	m.status.State = state
	m.status.Code = ""
	return nil
}
func (m *Manager) BeginCheck() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin("checking"); err != nil {
		return err
	}
	go m.check()
	return nil
}
func (m *Manager) check() {
	m.mu.Lock()
	ctx := m.ctx
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	release, err := m.source.Latest(ctx)
	m.mu.Lock()
	m.status.CheckedAt = time.Now().UnixMilli()
	m.working = false
	if err != nil {
		m.status.State = "error"
		m.status.Code = "update_check_failed"
		if errors.Is(err, ErrNoRelease) {
			m.status.Code = "update_not_published"
		}
		m.mu.Unlock()
		return
	}
	m.release = release
	m.status.Latest = release.Tag
	m.status.Notes = release.Notes
	m.status.ReleaseURL = release.URL()
	m.status.Available = Newer(release.Tag, version.Version)
	m.status.State = "current"
	if m.status.Available {
		m.status.State = "available"
	}
	auto := m.status.Available && m.status.Preferences.AutoDownload && m.managed
	var pending Candidate
	if ReadJSON(filepath.Join(m.dir, "pending.json"), &pending) == nil && pending.Valid() && pending.Tag == release.Tag && Verify(pending.Path(m.dir), pending) == nil {
		auto = false
		m.status.State = "ready"
	}
	m.mu.Unlock()
	if auto {
		_ = m.BeginDownload()
	}
}
func (m *Manager) BeginDownload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.managed {
		return ErrUnsupported
	}
	if !Newer(m.release.Tag, version.Version) {
		return ErrNotReady
	}
	if err := m.begin("downloading"); err != nil {
		return err
	}
	m.status.Downloaded = 0
	m.status.Total = 0
	go m.download(m.release)
	return nil
}

type progressWriter struct{ m *Manager }

func (p progressWriter) Write(b []byte) (int, error) {
	p.m.mu.Lock()
	p.m.status.Downloaded += int64(len(b))
	p.m.mu.Unlock()
	return len(b), nil
}
func (m *Manager) download(r Release) {
	m.mu.Lock()
	ctx := m.ctx
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	err := m.stage(ctx, r)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.working = false
	if err != nil {
		m.status.State = "error"
		m.status.Code = "update_download_failed"
	} else {
		m.status.State = "ready"
		m.status.Code = ""
	}
}
func (m *Manager) stage(ctx context.Context, r Release) error {
	name := "qinlin-server_" + runtime.GOOS + "_" + runtime.GOARCH
	digest, err := m.source.Checksum(ctx, r, name)
	if err != nil {
		return err
	}
	body, size, err := m.source.Download(ctx, r, name)
	if err != nil {
		return err
	}
	defer body.Close()
	m.mu.Lock()
	m.status.Total = size
	m.mu.Unlock()
	f, err := os.CreateTemp(m.dir, ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h, progressWriter{m}), io.LimitReader(body, MaxBinarySize+1))
	if err == nil && (n != size || hex.EncodeToString(h.Sum(nil)) != digest) {
		err = errors.New("release checksum mismatch")
	}
	if err == nil {
		err = f.Chmod(0700)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	candidate := Candidate{Tag: r.Tag, SHA256: digest, Size: n}
	if err = replaceFile(f.Name(), candidate.Path(m.dir)); err != nil {
		return err
	}
	return WriteJSON(filepath.Join(m.dir, "pending.json"), candidate)
}
func (m *Manager) RequestRestart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.managed {
		return ErrUnsupported
	}
	if m.working || m.status.State == "restarting" {
		return ErrBusy
	}
	var pending Candidate
	if ReadJSON(filepath.Join(m.dir, "pending.json"), &pending) != nil || !pending.Valid() || !Newer(pending.Tag, version.Version) {
		return ErrNotReady
	}
	if err := Verify(pending.Path(m.dir), pending); err != nil {
		return ErrNotReady
	}
	if err := WriteJSON(filepath.Join(m.dir, "restart.json"), pending); err != nil {
		return err
	}
	m.status.State = "restarting"
	m.restartAt = time.Now().UnixMilli()
	return nil
}
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.mu.Lock()
			due := m.managed && m.status.Preferences.AutoCheck && !m.working && time.Since(time.UnixMilli(m.status.CheckedAt)) >= 6*time.Hour
			m.mu.Unlock()
			if due {
				_ = m.BeginCheck()
			}
		}
	}
}
