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
//
// Default (neither flag set): Info+ to stderr. That's what makes
// `weather serve` print its per-request access log without any flags,
// matching standard web-server behaviour. CLI commands don't emit Info
// at runtime, so the default stays quiet for them.
//
// --debug: lower to Debug, still on stderr.
// --trace: full firehose to /tmp/weather.log (truncated each run). Stderr
// stays clean so the trace file is the single source of truth for a
// post-mortem.
func initLogger(tracePath, level string) func() {
	w := io.Writer(os.Stderr)
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
	lvl := slog.LevelInfo
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
