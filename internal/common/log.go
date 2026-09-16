package common

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// LogConfig configures all module loggers, including previously created ones.
// Its zero value selects INFO and standard error. Output remains caller-owned.
type LogConfig struct {
	Level  slog.Level
	Output io.Writer
}

var logging = struct {
	sync.Mutex
	handler slog.Handler
}{handler: newLogHandler(LogConfig{})}

func newLogHandler(config LogConfig) slog.Handler {
	if config.Output == nil {
		config.Output = os.Stderr
	}
	return slog.NewJSONHandler(config.Output, &slog.HandlerOptions{
		Level: config.Level,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Time(slog.TimeKey, attr.Value.Time().UTC())
			}
			return attr
		},
	})
}

// InitLogging replaces the shared logging configuration atomically.
// An invalid level leaves the existing configuration unchanged.
func InitLogging(config LogConfig) error {
	switch config.Level {
	case slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError:
	default:
		return fmt.Errorf("invalid log level: %v", config.Level)
	}
	logging.Lock()
	defer logging.Unlock()
	logging.handler = newLogHandler(config)
	return nil
}

// Logger binds an immutable module name and exposes only the five-field format.
// The zero value uses the module name "unknown".
type Logger struct{ module string }

func ModuleLogger(module string) Logger {
	if strings.TrimSpace(module) == "" {
		module = "unknown"
	}
	return Logger{module: module}
}

func (l Logger) Debug(event, msg string) { l.write(slog.LevelDebug, event, msg) }
func (l Logger) Info(event, msg string)  { l.write(slog.LevelInfo, event, msg) }
func (l Logger) Warn(event, msg string)  { l.write(slog.LevelWarn, event, msg) }
func (l Logger) Error(event, msg string) { l.write(slog.LevelError, event, msg) }

func (l Logger) write(level slog.Level, event, msg string) {
	logging.Lock()
	defer logging.Unlock()
	// A failing output must not interrupt the caller or recursively log itself.
	defer func() { _ = recover() }()
	ctx := context.Background()
	if !logging.handler.Enabled(ctx, level) {
		return
	}
	module := cmp.Or(l.module, "unknown")
	if strings.TrimSpace(event) == "" {
		event = "message"
	}
	record := slog.NewRecord(time.Now().UTC(), level, msg, 0)
	record.AddAttrs(slog.String("module", module), slog.String("event", event))
	_ = logging.handler.Handle(ctx, record)
}

// StandardLogger adapts APIs that require *log.Logger to the common output.
// The adapter does not own its output and follows InitLogging changes.
func (l Logger) StandardLogger(level slog.Level, event string) *log.Logger {
	return log.New(logWriter{logger: l, level: level, event: event}, "", 0)
}

type logWriter struct {
	logger Logger
	level  slog.Level
	event  string
}

func (w logWriter) Write(p []byte) (int, error) {
	switch w.level {
	case slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError:
	default:
		return 0, errors.New("invalid log level")
	}
	w.logger.write(w.level, w.event, strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}
