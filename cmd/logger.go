package cmd

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// LevelTrace is a custom slog level below Debug, gated by --trace.
const LevelTrace = slog.Level(-8)

// L is the package logger. Defaults to a discarding handler until initLogger
// runs in PersistentPreRunE so subcommands logging from init() don't panic.
var L = slog.New(slog.NewTextHandler(io.Discard, nil))

// initLogger configures the package logger from --debug / --trace flags.
// Returns a cleanup function that closes the trace file (if any).
func initLogger(tracePath, level string) func() {
	w := io.Writer(io.Discard)
	cleanup := func() {}
	if tracePath != "" {
		f, err := os.OpenFile(tracePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err == nil {
			w = f
			cleanup = func() {
				if cerr := f.Close(); cerr != nil {
					slog.Log(context.Background(), LevelTrace, "close trace file", "err", cerr)
				}
			}
		}
	}
	if level == "debug" {
		// Debug goes to stderr, not the trace file. Trace files are for
		// post-mortem self-diagnosis with the full firehose enabled.
		w = os.Stderr
	}
	lvl := slog.LevelWarn
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "trace":
		lvl = LevelTrace
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})
	L = slog.New(h)
	slog.SetDefault(L)
	return cleanup
}

// closeBody closes an io.Closer (typically http.Response.Body), trace-logging
// any error so --trace surfaces it without aborting the caller.
func closeBody(c io.Closer, what string) {
	if err := c.Close(); err != nil {
		slog.Log(context.Background(), LevelTrace, "close", "what", what, "err", err)
	}
}
