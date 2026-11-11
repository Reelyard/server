// Package logging sets up the process-wide slog logger.
package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/reelyard/server/internal/config"
)

// Setup builds the logger described by cfg, installs it as slog's default,
// and returns it with a closer for the log file (if any).
// extra, when non-nil, receives a copy of every line (used for the Windows
// event log / service logger).
func Setup(cfg *config.Config, extra io.Writer) (*slog.Logger, io.Closer, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Log.Level)); err != nil {
		return nil, nil, err
	}

	writers := []io.Writer{os.Stderr}
	var closer io.Closer = nopCloser{}
	if cfg.Log.File {
		if err := os.MkdirAll(cfg.LogDir(), 0o755); err != nil {
			return nil, nil, err
		}
		lj := &lumberjack.Logger{
			Filename:   filepath.Join(cfg.LogDir(), "reelyard.log"),
			MaxSize:    20, // MB
			MaxBackups: 5,
			MaxAge:     30, // days
			Compress:   true,
		}
		writers = append(writers, lj)
		closer = lj
	}
	if extra != nil {
		writers = append(writers, extra)
	}
	w := io.MultiWriter(writers...)

	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if cfg.Log.Format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	logger := slog.New(h)
	slog.SetDefault(logger)
	return logger, closer, nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }
