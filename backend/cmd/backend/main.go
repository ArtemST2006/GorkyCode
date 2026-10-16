// Package main starts the backend HTTP process.
package main

import (
	"context"
	"errors"
	"gorkycode/backend/internal/config"
	api "gorkycode/backend/internal/generated"
	"gorkycode/backend/internal/http-server/apierr"
	"gorkycode/backend/internal/http-server/handlers"
	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/service"
	"gorkycode/backend/internal/storage"
	"gorkycode/backend/internal/storage/postgres"
	"gorkycode/backend/internal/voice"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	cfg := config.MustLoad()
	log := logger.New(cfg.LogLevel, cfg.Env)

	log.Info("starting backend", slog.String("env", cfg.Env), slog.String("log_level", cfg.LogLevel))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.NewPool(ctx, cfg.Database)
	if err != nil {
		log.Error("failed to connect to PostgreSQL", logger.Err(err))
		return err
	}
	defer pool.Close()

	rec, err := voice.NewModel(&voice.VoiceConfig{
		ModelPath:    cfg.ArsModel.ModelPath,
		NumThreads:   cfg.ArsModel.NumThreads,
		DecodeMethod: cfg.ArsModel.DecodeMethod,
		Provider:     cfg.ArsModel.Provider,
	})
	if err != nil {
		panic("failed to load model")
	}
	defer rec.Close()

	repo := storage.NewRepository(pool)
	services := service.New(repo, cfg.Database.QueryTimeout)
	handler := handlers.NewServer(log, services, rec)
	router := chi.NewRouter()
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		apierr.Write(w, r, apierr.NotFound("route not found"))
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		apierr.Write(w, r, apierr.MethodNotAllowed("method not allowed for this route"))
	})
	api.HandlerWithOptions(handler, api.ChiServerOptions{BaseRouter: router})
	srv := &http.Server{
		Addr:              cfg.HTTPServer.Address,
		Handler:           router,
		ReadHeaderTimeout: cfg.HTTPServer.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPServer.ReadTimeout,
		WriteTimeout:      cfg.HTTPServer.WriteTimeout,
		IdleTimeout:       cfg.HTTPServer.IdleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("starting HTTP server", slog.String("address", cfg.HTTPServer.Address))
		serverErr <- srv.ListenAndServe()
	}()

	var listenErr error
	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP server stopped", logger.Err(err))
			listenErr = err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", logger.Err(err))
		_ = srv.Close()
		return err
	}
	log.Info("backend stopped")
	return listenErr
}
