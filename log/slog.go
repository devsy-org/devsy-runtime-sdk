package log

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/devsy-org/devsy/logging"
	"github.com/devsy-org/devsy/logging/redact"
)

func NewPluginLogger(providerName string) *slog.Logger {
	config, invalid := logging.ResolveProvider(
		os.Getenv("DEVSY_LOG_LEVEL"),
		os.Getenv("DEVSY_LOG_FORMAT"),
	)
	handler := logging.NewHandler(os.Stderr, logging.HandlerOptions{
		Level:        config.Level,
		Format:       config.Format,
		Redactor:     redact.New(redact.Options{Environment: os.Environ()}),
		ReservedKeys: logging.EscapeReserved,
		TrustedAttrs: []slog.Attr{
			slog.String("component", "provider"),
			slog.String("provider", providerName),
		},
	})
	if invalid.Level {
		_ = handler.Handle(context.Background(), slog.NewRecord(
			time.Now(), slog.LevelWarn, "invalid DEVSY_LOG_LEVEL; using info", 0,
		))
	}
	if invalid.Format {
		_ = handler.Handle(context.Background(), slog.NewRecord(
			time.Now(), slog.LevelWarn, "invalid DEVSY_LOG_FORMAT; using json", 0,
		))
	}
	return slog.New(handler)
}
