package log

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"
)

func NewPluginLogger(providerName string) *slog.Logger {
	level, invalidLevel := pluginLevel(os.Getenv("DEVSY_LOG_LEVEL"))
	format := strings.ToLower(os.Getenv("DEVSY_LOG_FORMAT"))
	invalidFormat := false
	var handler slog.Handler
	options := &slog.HandlerOptions{Level: level, ReplaceAttr: replaceEnvelopeAttr}
	switch format {
	case "", "auto", "json":
		handler = slog.NewJSONHandler(os.Stderr, options)
	case "text", "plain":
		handler = slog.NewTextHandler(os.Stderr, options)
	default:
		invalidFormat = true
		handler = slog.NewJSONHandler(os.Stderr, options)
	}
	handler = handler.WithAttrs(
		[]slog.Attr{slog.String("component", "provider"), slog.String("provider", providerName)},
	)
	logger := slog.New(&pluginHandler{handler: handler, root: true})
	if invalidLevel {
		_ = logger.Handler().
			Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelWarn, "invalid DEVSY_LOG_LEVEL; using info", 0))
	}
	if invalidFormat {
		_ = logger.Handler().
			Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelWarn, "invalid DEVSY_LOG_FORMAT; using json", 0))
	}
	return logger
}

func pluginLevel(value string) (slog.Level, bool) {
	switch strings.ToLower(value) {
	case "", "info":
		return slog.LevelInfo, false
	case "error":
		return slog.LevelError, false
	case "warn":
		return slog.LevelWarn, false
	case "debug":
		return slog.LevelDebug, false
	case "trace":
		return slog.Level(-8), false
	default:
		return slog.LevelInfo, true
	}
}

func replaceEnvelopeAttr(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) != 0 {
		return attr
	}
	switch attr.Key {
	case slog.TimeKey:
		if attr.Value.Kind() == slog.KindTime {
			return slog.String("ts", attr.Value.Time().Format("2006-01-02T15:04:05.000Z0700"))
		}
	case slog.LevelKey:
		return replaceLevelAttr(attr)
	}
	return attr
}

func replaceLevelAttr(attr slog.Attr) slog.Attr {
	level, ok := attr.Value.Any().(slog.Level)
	if !ok {
		return attr
	}
	switch level {
	case -8:
		return slog.String("level", "trace")
	case 12:
		return slog.String("level", "fatal")
	default:
		return slog.String("level", strings.ToLower(level.String()))
	}
}

type pluginHandler struct {
	handler slog.Handler
	root    bool
}

func (h *pluginHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *pluginHandler) Handle(ctx context.Context, record slog.Record) error {
	if !h.root {
		return h.handler.Handle(ctx, record)
	}
	var replacement slog.Record
	changed := false
	index := 0
	record.Attrs(func(attr slog.Attr) bool {
		escaped, modified := escapeRootAttr(attr)
		if modified && !changed {
			replacement = slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
			copyRecordPrefix(&replacement, record, index)
			changed = true
		}
		if changed {
			replacement.AddAttrs(escaped)
		}
		index++
		return true
	})
	if changed {
		return h.handler.Handle(ctx, replacement)
	}
	return h.handler.Handle(ctx, record)
}

func copyRecordPrefix(dst *slog.Record, src slog.Record, count int) {
	src.Attrs(func(attr slog.Attr) bool {
		if count == 0 {
			return false
		}
		dst.AddAttrs(attr)
		count--
		return true
	})
}

func (h *pluginHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h.root {
		attrs, _ = escapeRootAttrs(attrs)
	}
	return &pluginHandler{handler: h.handler.WithAttrs(attrs), root: h.root}
}

func (h *pluginHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	if h.root {
		name = escapeRootKey(name)
	}
	return &pluginHandler{handler: h.handler.WithGroup(name)}
}

func escapeRootKey(key string) string {
	switch key {
	case "ts", "time", "level", "msg", "message", "component", "provider":
		return "user_" + key
	default:
		return key
	}
}

func escapeRootAttrs(attrs []slog.Attr) ([]slog.Attr, bool) {
	var escaped []slog.Attr
	for i, attr := range attrs {
		value, modified := escapeRootAttr(attr)
		if modified && escaped == nil {
			escaped = make([]slog.Attr, len(attrs))
			copy(escaped, attrs[:i])
		}
		if escaped != nil {
			escaped[i] = value
		}
	}
	if escaped == nil {
		return attrs, false
	}
	return escaped, true
}

func escapeRootAttr(attr slog.Attr) (slog.Attr, bool) {
	if attr.Key != "" {
		key := escapeRootKey(attr.Key)
		if key != attr.Key {
			attr.Key = key
			return attr, true
		}
		return attr, false
	}
	modified := attr.Value.Kind() == slog.KindLogValuer
	if modified {
		attr.Value = attr.Value.Resolve()
	}
	if attr.Value.Kind() != slog.KindGroup {
		return attr, modified
	}
	attrs, escaped := escapeRootAttrs(attr.Value.Group())
	if escaped {
		attr.Value = slog.GroupValue(attrs...)
	}
	return attr, modified || escaped
}
