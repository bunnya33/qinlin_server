package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testManager(t *testing.T, corrupt bool) *Manager {
	t.Helper()
	m, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	m.managed = true
	data := "test release executable"
	sum := sha256.Sum256([]byte(data))
	name := "qinlin-server_" + runtime.GOOS + "_" + runtime.GOARCH
	manifest := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	release := Release{Tag: "v9.0.0", Assets: []Asset{{Name: name, Size: int64(len(data))}, {Name: "SHA256SUMS", Size: int64(len(manifest))}}}
	m.source.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.Path {
		case "/repos/" + Repository + "/releases/latest":
			b, _ := json.Marshal(release)
			body = string(b)
		case "/" + Repository + "/releases/download/v9.0.0/SHA256SUMS":
			body = manifest
		case "/" + Repository + "/releases/download/v9.0.0/" + name:
			body = data
			if corrupt {
				body = "corrupted file contents"
			}
		default:
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Header: make(http.Header)}, nil
	})}
	return m
}
func awaitState(t *testing.T, m *Manager, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.Status().State == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("wanted %s; got %+v", state, m.Status())
}
func TestCheckDownloadAndExplicitRestart(t *testing.T) {
	m := testManager(t, false)
	if err := m.SetPreferences(Preferences{AutoCheck: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	awaitState(t, m, "available")
	if _, err := os.Stat(filepath.Join(m.dir, "restart.json")); !os.IsNotExist(err) {
		t.Fatal("checking requested restart")
	}
	if err := m.BeginDownload(); err != nil {
		t.Fatal(err)
	}
	awaitState(t, m, "ready")
	status := m.Status()
	if !status.Ready || status.Downloaded != status.Total || status.PendingVersion != "v9.0.0" {
		t.Fatal(status)
	}
	var pending Candidate
	if err := ReadJSON(filepath.Join(m.dir, "pending.json"), &pending); err != nil {
		t.Fatal(err)
	}
	if err := Verify(pending.Path(m.dir), pending); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.dir, "restart.json")); !os.IsNotExist(err) {
		t.Fatal("download automatically requested restart")
	}
	if err := m.RequestRestart(); err != nil {
		t.Fatal(err)
	}
	var request Candidate
	if err := ReadJSON(filepath.Join(m.dir, "restart.json"), &request); err != nil || request != pending {
		t.Fatal(request, err)
	}
	if err := m.BeginCheck(); err != ErrBusy {
		t.Fatal("restarting accepted another task", err)
	}
}
func TestAutomaticDownloadNeverAutomaticallyRestarts(t *testing.T) {
	m := testManager(t, false)
	if err := m.SetPreferences(Preferences{AutoCheck: true, AutoDownload: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	awaitState(t, m, "ready")
	if _, err := os.Stat(filepath.Join(m.dir, "restart.json")); !os.IsNotExist(err) {
		t.Fatal("automatic download restarted the server")
	}
	reloaded, err := New(m.dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Status().Preferences.AutoDownload || !reloaded.Status().Ready {
		t.Fatal("pending update or settings lost on restart")
	}
}
func TestCorruptDownloadCannotReplaceRunningOrPendingFiles(t *testing.T) {
	m := testManager(t, true)
	_ = m.SetPreferences(Preferences{})
	if err := m.BeginCheck(); err != nil {
		t.Fatal(err)
	}
	awaitState(t, m, "available")
	if err := m.BeginDownload(); err != nil {
		t.Fatal(err)
	}
	awaitState(t, m, "error")
	if m.Status().Ready || m.Status().Code != "update_download_failed" {
		t.Fatal(m.Status())
	}
	if err := m.RequestRestart(); err != ErrNotReady {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.dir, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("invalid download was staged")
	}
}
func TestRestartRevalidatesDownloadedFile(t *testing.T) {
	m := testManager(t, false)
	_ = m.SetPreferences(Preferences{})
	_ = m.BeginCheck()
	awaitState(t, m, "available")
	_ = m.BeginDownload()
	awaitState(t, m, "ready")
	var pending Candidate
	_ = ReadJSON(filepath.Join(m.dir, "pending.json"), &pending)
	if err := os.WriteFile(pending.Path(m.dir), []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.RequestRestart(); err != ErrNotReady {
		t.Fatal(err)
	}
}

func TestRejectedRestartDoesNotLeaveServiceBlocked(t *testing.T) {
	m := testManager(t, false)
	_ = m.SetPreferences(Preferences{})
	_ = m.BeginCheck()
	awaitState(t, m, "available")
	_ = m.BeginDownload()
	awaitState(t, m, "ready")
	if err := m.RequestRestart(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(m.dir, "pending.json"))
	if err := WriteJSON(filepath.Join(m.dir, "result.json"), ApplyResult{State: "update_invalid", At: time.Now().Add(time.Millisecond).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if m.Status().State != "update_invalid" {
		t.Fatal(m.Status())
	}
	if err := m.BeginCheck(); err != nil {
		t.Fatal("a rejected restart left the manager blocked", err)
	}
	awaitState(t, m, "available")
}
func TestReleaseValidation(t *testing.T) {
	if !Newer("v1.10.0", "1.9.9") || Newer("v1.0.0", "1.0.0") || Newer("v1.0.0-beta", "0.3.0") {
		t.Fatal("version comparison failed")
	}
	for _, tag := range []string{"../bad", "v1.2.3/evil", "v01.2.3", "v1.2.3-beta", "1.2.3"} {
		if ValidTag(tag) {
			t.Fatal(tag)
		}
	}
	s := NewSource()
	if _, err := s.get(context.Background(), "http://github.com/"+Repository); err == nil {
		t.Fatal("allowed insecure release source")
	}
	s.Client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err := s.Latest(context.Background()); err != ErrNoRelease {
		t.Fatal(err)
	}
	if _, _, err := s.Download(context.Background(), Release{Assets: []Asset{{Name: "large", Size: MaxBinarySize + 1}}}, "large"); err == nil {
		t.Fatal("allowed oversized asset")
	}
}
