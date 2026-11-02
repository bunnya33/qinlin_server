//go:build linux

package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qinlin/internal/config"
	"qinlin/internal/update"
	"qinlin/internal/version"
)

// Launched as a separate OS process, with a real HTTP health endpoint.
func TestSupervisorChild(t *testing.T) {
	if os.Getenv("QINLIN_TEST_CHILD") != "1" {
		return
	}
	if os.Getenv("QINLIN_TEST_CHILD_VERSION") == "fail" {
		os.Exit(3)
	}
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ok":true,"version":%q}`, os.Getenv("QINLIN_TEST_CHILD_VERSION"))
	})
	if err := http.ListenAndServe(os.Getenv("QINLIN_TEST_CHILD_ADDR"), nil); err != nil {
		os.Exit(4)
	}
}
func TestSupervisorAppliesRestartsAndRollsBack(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	t.Setenv("QINLIN_TEST_CHILD_ADDR", addr)
	dir := t.TempDir()
	updates := filepath.Join(dir, "updates")
	if err = os.MkdirAll(updates, 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := func(v string) []byte {
		return []byte("#!/bin/sh\nQINLIN_TEST_CHILD=1 QINLIN_TEST_CHILD_VERSION=" + quote(v) + " exec " + quote(exe) + " -test.run='^TestSupervisorChild$'\n")
	}
	base := filepath.Join(dir, "base")
	if err = os.WriteFile(base, script(version.Version), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Addr: addr, DB: filepath.Join(dir, "test.db")}
	start := func() (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- run(ctx, cfg, base) }()
		return cancel, done
	}
	wait := func(condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("supervisor condition timed out")
	}
	health := func(v string) bool {
		client := http.Client{Timeout: time.Second}
		resp, err := client.Get("http://" + addr + "/healthz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var buf [256]byte
		n, _ := resp.Body.Read(buf[:])
		return strings.Contains(string(buf[:n]), `"version":"`+v+`"`)
	}
	cancel, done := start()
	defer func() { cancel(); <-done }()
	wait(func() bool { return health(version.Version) })
	request := func(tag, behavior string) {
		data := script(behavior)
		hash := sha256.Sum256(data)
		c := update.Candidate{Tag: tag, SHA256: hex.EncodeToString(hash[:]), Size: int64(len(data))}
		if err = os.WriteFile(c.Path(updates), data, 0700); err != nil {
			t.Fatal(err)
		}
		if err = update.WriteJSON(filepath.Join(updates, "pending.json"), c); err != nil {
			t.Fatal(err)
		}
		if err = update.WriteJSON(filepath.Join(updates, "restart.json"), c); err != nil {
			t.Fatal(err)
		}
	}
	result := func(state, tag string) bool {
		var r update.ApplyResult
		return update.ReadJSON(filepath.Join(updates, "result.json"), &r) == nil && r.State == state && r.Tag == tag
	}
	request("v9.1.0", "9.1.0")
	wait(func() bool { return result("updated", "v9.1.0") && health("9.1.0") })
	// A full supervisor restart must keep the selected version.
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	cancel, done = start()
	wait(func() bool { return health("9.1.0") })
	request("v9.2.0", "fail")
	wait(func() bool { return result("rolled_back", "v9.2.0") && health("9.1.0") })
	if _, err = os.Stat(filepath.Join(updates, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("failed candidate still pending")
	}
}
