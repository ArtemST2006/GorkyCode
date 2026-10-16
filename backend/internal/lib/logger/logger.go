// Package logger configures structured logging for the backend.
package logger

import (
	"log/slog"
	"os"
	"strings"

	slogpretty "gorkycode/backend/internal/lib/logger/handlers"
)

// New uses readable logs locally and JSON logs in dev and prod.
func New(logLevel, env string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	if env == "dev" || env == "prod" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}

	return slog.New((slogpretty.PrettyHandlerOptions{SlogOpts: opts}).NewPrettyHandler(os.Stdout))
}

// Err records an error as a structured attribute.
func Err(err error) slog.Attr {
	return slog.String("error", err.Error())
}
