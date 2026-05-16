package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	 "anibridge-go/internal/config"
	 "anibridge-go/internal/core/sched"
	 "anibridge-go/internal/utils"
	 "anibridge-go/internal/web"
	 "anibridge-go/internal/web/services"
	_  "anibridge-go/providers/library/emby"
	_  "anibridge-go/providers/library/jellyfin"
	_  "anibridge-go/providers/library/plex"
	_  "anibridge-go/providers/list/anilist"
	_  "anibridge-go/providers/list/mal"
	_  "anibridge-go/providers/list/simkl"
	_  "anibridge-go/providers/list/trakt"
)

func main() {
	configPath := flag.String("config", "", "path to config.yml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		slog.Error("create data dir", "error", err)
		os.Exit(1)
	}

	logger := utils.NewLogger(cfg.LogLevel, os.Stdout)
	slog.SetDefault(logger)

	db, err := sql.Open("sqlite", filepath.Join(cfg.DataDir, "anibridge-go.db"))
	if err != nil {
		logger.Error("open db", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := services.ApplyEmbeddedMigrations(db); err != nil {
		logger.Error("migrate db", "error", err)
		os.Exit(1)
	}

	hub := services.NewHub()
	logs := services.NewLogStore(500, hub)
	logger = utils.NewLoggerWithSink(cfg.LogLevel, os.Stdout, logs.Add)
	slog.SetDefault(logger)

	scheduler := sched.NewClient(cfg, db, hub)
	if err := scheduler.Initialize(context.Background()); err != nil {
		logger.Error("initialize scheduler", "error", err)
		os.Exit(1)
	}
	if err := scheduler.Start(); err != nil {
		logger.Error("start scheduler", "error", err)
		os.Exit(1)
	}

	app := web.New(web.Deps{
		Config:       cfg,
		ConfigPath:   config.EffectivePath(*configPath),
		DB:           db,
		Hub:          hub,
		Logs:         logs,
		Scheduler:    scheduler,
		StartedAt:    time.Now(),
		FrontendRoot: "frontend/build",
	})
	server := &http.Server{
		Addr:              cfg.Web.Addr(),
		Handler:           app,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("AniBridge GO listening", "addr", cfg.Web.Addr())
		errs <- server.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-stop:
		logger.Info("shutdown requested", "signal", sig.String())
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = scheduler.Stop(ctx)
	_ = server.Shutdown(ctx)
}
