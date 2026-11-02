package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"qinlin/internal/config"
	"qinlin/internal/qinlin"
	"qinlin/internal/server"
	"qinlin/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 {
		return runCommand(os.Args[1:])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.DB, cfg.Key, cfg.Mock)
	if err != nil {
		return err
	}
	defer db.Close()
	var api qinlin.API = qinlin.New(cfg.DeviceID, cfg.UpstreamTimeout)
	if cfg.Mock {
		api = qinlin.Mock{}
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	app := server.New(cfg, db, api, logger)
	h := &http.Server{Addr: cfg.Addr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		tick := time.NewTicker(time.Hour)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if db.Prune() != nil {
					logger.Error("operation retention cleanup failed")
				}
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- h.ListenAndServe() }()
	logger.Info("service started", "address", cfg.Addr, "public_url", cfg.PublicURL, "mock", cfg.Mock)
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return h.Shutdown(shutdown)
	}
}
