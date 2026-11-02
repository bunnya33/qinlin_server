//go:build linux

// Package supervisor runs downloaded versions with the same unprivileged user
// as the web server. The installed supervisor and systemd unit stay unchanged.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"qinlin/internal/config"
	"qinlin/internal/update"
	"qinlin/internal/version"
)

type activeVersion struct {
	Current  *update.Candidate `json:"current"`
	Previous *update.Candidate `json:"previous"`
}
type child struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func launch(path string) (*child, error) {
	cmd := exec.Command(path)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "QINLIN_SUPERVISED=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "QINLIN_SUPERVISED=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &child{cmd: cmd, done: make(chan struct{})}
	go func() { c.err = cmd.Wait(); close(c.done) }()
	return c, nil
}
func (c *child) stop() {
	select {
	case <-c.done:
		return
	default:
	}
	_ = c.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-c.done:
	case <-time.After(45 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
}
func binaryPath(dir string, c *update.Candidate, base string) string {
	if c == nil {
		return base
	}
	return filepath.Join(dir, "version-"+c.Tag+"-"+c.SHA256+".bin")
}
func healthURL(addr string) (string, error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if h == "" || h == "0.0.0.0" {
		h = "127.0.0.1"
	}
	if h == "::" {
		h = "::1"
	}
	return "http://" + net.JoinHostPort(h, p) + "/healthz", nil
}
func ready(ctx context.Context, c *child, address, wanted string) bool {
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-c.done:
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
			req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
			if err != nil {
				return false
			}
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			var status struct {
				OK      bool   `json:"ok"`
				Version string `json:"version"`
			}
			err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&status)
			resp.Body.Close()
			if err == nil && resp.StatusCode == 200 && status.OK && status.Version == strings.TrimPrefix(wanted, "v") {
				return true
			}
		}
	}
}

func Run(ctx context.Context, cfg config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return run(ctx, cfg, filepath.Join(filepath.Dir(exe), "qinlin-server"))
}
func run(ctx context.Context, cfg config.Config, base string) error {
	dir := filepath.Join(filepath.Dir(cfg.DB), "updates")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	address, err := healthURL(cfg.Addr)
	if err != nil {
		return err
	}
	activeFile := filepath.Join(dir, "active.json")
	requestFile := filepath.Join(dir, "restart.json")
	var active activeVersion
	_ = update.ReadJSON(activeFile, &active)
	if active.Current != nil && (!active.Current.Valid() || !update.Newer(active.Current.Tag, version.Version) || update.Verify(binaryPath(dir, active.Current, base), *active.Current) != nil) {
		active = activeVersion{}
	}
	if active.Previous != nil && (!active.Previous.Valid() || update.Verify(binaryPath(dir, active.Previous, base), *active.Previous) != nil) {
		active.Previous = nil
	}
	wanted := func(c *update.Candidate) string {
		if c == nil {
			return version.Version
		}
		return c.Tag
	}
	start := func(c *update.Candidate) (*child, error) {
		proc, err := launch(binaryPath(dir, c, base))
		if err != nil {
			return nil, err
		}
		if !ready(ctx, proc, address, wanted(c)) {
			proc.stop()
			return nil, errors.New("server did not pass startup health check")
		}
		return proc, nil
	}
	proc, err := start(active.Current)
	if err != nil && active.Current != nil {
		failed := active.Current.Tag
		active.Current = active.Previous
		active.Previous = nil
		proc, err = start(active.Current)
		if err == nil {
			_ = update.WriteJSON(activeFile, active)
			_ = update.WriteJSON(filepath.Join(dir, "result.json"), update.ApplyResult{State: "rolled_back", Tag: failed, At: time.Now().UnixMilli()})
		}
	}
	if err != nil {
		return err
	}
	defer func() {
		if proc != nil {
			proc.stop()
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-proc.done:
			return fmt.Errorf("server process exited: %w", proc.err)
		case <-tick.C:
			var next update.Candidate
			if err = update.ReadJSON(requestFile, &next); os.IsNotExist(err) {
				continue
			}
			_ = os.Remove(requestFile)
			if err != nil || !next.Valid() || !update.Newer(next.Tag, wanted(active.Current)) || update.Verify(next.Path(dir), next) != nil {
				_ = os.Remove(filepath.Join(dir, "pending.json"))
				_ = update.WriteJSON(filepath.Join(dir, "result.json"), update.ApplyResult{State: "update_invalid", At: time.Now().UnixMilli()})
				continue
			}
			path := binaryPath(dir, &next, base)
			if err = os.Rename(next.Path(dir), path); err != nil {
				_ = os.Remove(filepath.Join(dir, "pending.json"))
				_ = update.WriteJSON(filepath.Join(dir, "result.json"), update.ApplyResult{State: "update_invalid", Tag: next.Tag, At: time.Now().UnixMilli()})
				continue
			}
			proc.stop()
			candidate, startErr := start(&next)
			if startErr == nil {
				newActive := activeVersion{Current: &next, Previous: active.Current}
				if err = update.WriteJSON(activeFile, newActive); err == nil {
					proc = candidate
					active = newActive
					_ = os.Remove(filepath.Join(dir, "pending.json"))
					_ = update.WriteJSON(filepath.Join(dir, "result.json"), update.ApplyResult{State: "updated", Tag: next.Tag, At: time.Now().UnixMilli()})
					continue
				}
				candidate.stop()
			}
			if ctx.Err() != nil {
				return nil
			}
			proc, err = start(active.Current)
			if err != nil {
				return err
			}
			_ = os.Remove(filepath.Join(dir, "pending.json"))
			_ = os.Remove(path)
			_ = update.WriteJSON(filepath.Join(dir, "result.json"), update.ApplyResult{State: "rolled_back", Tag: next.Tag, At: time.Now().UnixMilli()})
		}
	}
}
