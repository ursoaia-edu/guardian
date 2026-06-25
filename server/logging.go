package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// clientIP is the address every log line and every stored session IP uses:
// the one the client-IP middleware resolved (see clientIPMiddleware in
// routes.go), or the TCP peer when nothing resolved one. Reading r.RemoteAddr
// directly is wrong behind a proxy — every session and every failed-login
// warning would name the proxy — so nothing else in the package should.
func clientIP(r *http.Request) string {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requestLogger writes one structured line per request. Successful health
// probes are skipped: an orchestrator polls /health every couple of seconds,
// and a log made mostly of those buries the lines that matter.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		defer func() {
			if r.URL.Path == "/health" && ww.Status() < 400 {
				return
			}
			slog.Info("request",
				"request_id", middleware.GetReqID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"ip", clientIP(r),
			)
		}()
		next.ServeHTTP(ww, r)
	})
}

// recoverer turns a panicking handler into a logged 500 instead of a dropped
// connection. net/http already recovers per connection, but it prints to
// stderr in plain text — invisible in a JSON log stream and carrying no
// request id. chi's own Recoverer is not used for the same reason: it writes
// a pretty-printed stack to stderr rather than through slog.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rvr := recover()
			if rvr == nil {
				return
			}
			// The one panic that is a protocol, not a bug: net/http uses it to
			// abort a response deliberately, and swallowing it changes behaviour.
			if rvr == http.ErrAbortHandler {
				panic(rvr)
			}
			slog.Error("panic in handler",
				"request_id", middleware.GetReqID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"panic", fmt.Sprint(rvr),
				"stack", string(debug.Stack()),
			)
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		}()
		next.ServeHTTP(w, r)
	})
}
