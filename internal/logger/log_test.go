package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func configureTestLog(t *testing.T, config LogConfig) {
	t.Helper()
	logging.Lock()
	previous := logging.handler
	logging.Unlock()
	t.Cleanup(func() {
		logging.Lock()
		defer logging.Unlock()
		logging.handler = previous
	})
	if err := InitLogging(config); err != nil {
		t.Fatal(err)
	}
}

func decodeLogs(t *testing.T, raw string) []map[string]string {
	t.Helper()
	var entries []map[string]string
	for line := range strings.SplitSeq(strings.TrimSuffix(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]string
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		if len(entry) != 5 {
			t.Fatalf("expected exactly five fields: %v", entry)
		}
		for _, key := range []string{"time", "level", "module", "event", "msg"} {
			if _, ok := entry[key]; !ok {
				t.Fatalf("missing %s: %v", key, entry)
			}
		}
		if _, err := time.Parse(time.RFC3339Nano, entry["time"]); err != nil || !strings.HasSuffix(entry["time"], "Z") {
			t.Fatalf("expected UTC timestamp: %v", entry)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestLoggerFormatAndLevelFiltering(t *testing.T) {
	for _, tc := range []struct {
		level slog.Level
		want  int
	}{{slog.LevelDebug, 4}, {slog.LevelInfo, 3}, {slog.LevelWarn, 2}, {slog.LevelError, 1}} {
		t.Run(tc.level.String(), func(t *testing.T) {
			var output bytes.Buffer
			configureTestLog(t, LogConfig{Level: tc.level, Output: &output})
			logger := ModuleLogger("example")
			msg := "quote: \"hello\"\nnext line\tUnicode: \u4e2d\u6587"
			logger.Debug("debug_event", msg)
			logger.Info("info_event", msg)
			logger.Warn("warn_event", msg)
			logger.Error("error_event", msg)
			entries := decodeLogs(t, output.String())
			if len(entries) != tc.want {
				t.Fatalf("got %d entries, want %d", len(entries), tc.want)
			}
			levels := []string{"DEBUG", "INFO", "WARN", "ERROR"}
			for i, entry := range entries {
				if entry["module"] != "example" || entry["msg"] != msg || entry["level"] != levels[4-tc.want+i] {
					t.Fatalf("unexpected log: %v", entry)
				}
			}
		})
	}
}

func TestLoggersFollowConfigurationAndInvalidConfigIsAtomic(t *testing.T) {
	var first, second bytes.Buffer
	configureTestLog(t, LogConfig{Output: &first})
	logger := ModuleLogger("existing")
	logger.Info("before", "first output")
	if err := InitLogging(LogConfig{Level: slog.LevelError, Output: &second}); err != nil {
		t.Fatal(err)
	}
	logger.Info("filtered", "not written")
	logger.Error("after", "second output")
	if err := InitLogging(LogConfig{Level: slog.Level(1), Output: &first}); err == nil {
		t.Fatal("accepted unsupported level")
	}
	logger.Error("still_after", "second output")
	if len(decodeLogs(t, first.String())) != 1 || len(decodeLogs(t, second.String())) != 2 {
		t.Fatalf("configuration did not apply atomically: %s / %s", &first, &second)
	}
}

func TestDefaultLoggingBeforeInitialization(t *testing.T) {
	// The package's initial handler must work before any initialization.
	var output bytes.Buffer
	logging.Lock()
	initial, ok := logging.handler.(*slog.JSONHandler)
	logging.Unlock()
	if !ok || initial.Enabled(t.Context(), slog.LevelDebug) || !initial.Enabled(t.Context(), slog.LevelInfo) {
		t.Fatal("unexpected initial handler or level")
	}
	// Exercise zero-value configuration against standard error without a pipe.
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	previous := os.Stderr
	os.Stderr = file
	t.Cleanup(func() { os.Stderr = previous })
	configureTestLog(t, LogConfig{})
	ModuleLogger("default").Debug("hidden", "not emitted")
	ModuleLogger("default").Info("ready", "default output")
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(&output, file); err != nil {
		t.Fatal(err)
	}
	if entries := decodeLogs(t, output.String()); len(entries) != 1 || entries[0]["module"] != "default" {
		t.Fatalf("unexpected default output: %s", &output)
	}
}

func TestConcurrentModulesAndReconfiguration(t *testing.T) {
	var output bytes.Buffer // Intentionally not thread safe; Logger must serialize writes.
	configureTestLog(t, LogConfig{Output: &output})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			logger := ModuleLogger(fmt.Sprintf("module-%d", i))
			for j := range 50 {
				logger.Info("operation", fmt.Sprintf("%d/%d", i, j))
			}
		})
	}
	wg.Go(func() {
		for range 50 {
			if err := InitLogging(LogConfig{Output: &output}); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
	entries := decodeLogs(t, output.String())
	seen := make(map[string]bool)
	for _, entry := range entries {
		module, _, _ := strings.Cut(entry["msg"], "/")
		if entry["module"] != "module-"+module || seen[entry["msg"]] {
			t.Fatalf("interleaved or duplicate log: %v", entry)
		}
		seen[entry["msg"]] = true
	}
	if len(seen) != 1000 {
		t.Fatalf("lost logs: got %d", len(seen))
	}
}

type brokenLogOutput struct{ panicOnWrite bool }

func (w brokenLogOutput) Write([]byte) (int, error) {
	if w.panicOnWrite {
		panic("output unavailable")
	}
	return 0, errors.New("output unavailable")
}

func TestOutputFailureDoesNotInterruptCaller(t *testing.T) {
	for _, panicOnWrite := range []bool{false, true} {
		configureTestLog(t, LogConfig{Output: brokenLogOutput{panicOnWrite}})
		ModuleLogger("test").Error("failed", "message")
		var output bytes.Buffer
		if err := InitLogging(LogConfig{Output: &output}); err != nil {
			t.Fatal(err)
		}
		ModuleLogger("test").Info("recovered", "output restored")
		if len(decodeLogs(t, output.String())) != 1 {
			t.Fatal("output failure left logger unusable")
		}
	}
}

func TestStandardLoggerUsesSharedFormat(t *testing.T) {
	var output bytes.Buffer
	configureTestLog(t, LogConfig{Output: &output})
	logger := ModuleLogger("http").StandardLogger(slog.LevelError, "server_error")
	logger.Printf("connection %d failed\n", 1)
	entries := decodeLogs(t, output.String())
	if len(entries) != 1 || entries[0]["event"] != "server_error" || entries[0]["level"] != "ERROR" || entries[0]["msg"] != "connection 1 failed" {
		t.Fatalf("unexpected standard log: %s", &output)
	}
}
