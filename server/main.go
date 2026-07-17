package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server holds the state of the guardian server
type Server struct {
	pool *pgxpool.Pool

	// trustedProxies is the number of reverse-proxy hops between the public
	// internet and this process — the value the client-IP middleware in
	// routes.go keys rate limits and session IPs on. Zero means "nothing in
	// front of us; the TCP peer is the client", which is the safe default:
	// trusting X-Forwarded-For with no proxy to write it lets any client
	// choose its own rate-limit bucket.
	trustedProxies int
}

func NewServer(ctx context.Context) (*Server, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	proxies, err := trustedProxiesFromEnv()
	if err != nil {
		return nil, err
	}
	if _, err := cabinetOriginsFromEnv(); err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Server{pool: pool, trustedProxies: proxies}, nil
}

// cabinetOriginsFromEnv reads CABINET_ORIGIN, the list of origins allowed to
// send credentialed requests. There is deliberately no default: the previous
// one was http://localhost:5173, which is correct for a developer and, in
// production with CABINET_ORIGIN unset, silently means "a page served from
// the developer's own machine may act as any signed-in user of this server".
// A missing value is a startup error, where it is impossible to miss.
func cabinetOriginsFromEnv() ([]string, error) {
	raw := os.Getenv("CABINET_ORIGIN")
	var origins []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("CABINET_ORIGIN is required: the comma-separated origins allowed to " +
			"send credentialed requests (for local development, http://localhost:5173)")
	}
	return origins, nil
}

// trustedProxiesFromEnv reads TRUSTED_PROXIES. Unset is zero; anything that
// is not a non-negative integer is a startup error rather than a silent
// zero, because a typo here decides whether rate limiting works at all.
func trustedProxiesFromEnv() (int, error) {
	v := os.Getenv("TRUSTED_PROXIES")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("TRUSTED_PROXIES must be a non-negative integer, got %q", v)
	}
	return n, nil
}

func (s *Server) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Loaded before the subcommand dispatch so that "guardian-server migrate"
	// reads MIGRATE_DATABASE_URL from the same .env the service reads
	// DATABASE_URL from. Real environment variables still take precedence.
	if err := loadEnvFile(".env"); err != nil && !os.IsNotExist(err) {
		slog.Warn("could not load .env file", "error", err)
	}

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		dsn := os.Getenv("MIGRATE_DATABASE_URL")
		if dsn == "" {
			slog.Error("MIGRATE_DATABASE_URL is required for migrate")
			os.Exit(1)
		}
		if err := runMigrations(context.Background(), dsn); err != nil {
			slog.Error("migration failed", "error", err)
			os.Exit(1)
		}
		slog.Info("migrations applied")
		return
	}

	server, err := NewServer(context.Background())
	if err != nil {
		slog.Error("failed to create server", "error", err)
		os.Exit(1)
	}
	defer server.Close()

	router := server.setupRoutes()

	addr := "0.0.0.0:8080"
	displayAddr := "http://localhost:8080/"
	if envAddr := os.Getenv("SERVER_ADDRESS"); envAddr != "" {
		displayAddr = envAddr
		parts := strings.Split(envAddr, "//")
		addr = strings.TrimSuffix(parts[len(parts)-1], "/")
	}

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Housekeeping runs alongside the server and stops with it.
	maintenanceCtx, stopMaintenance := context.WithCancel(context.Background())
	defer stopMaintenance()
	go server.runMaintenance(maintenanceCtx)

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	// A listen failure is reported back here rather than os.Exit'd from the
	// goroutine, so the deferred pool close above still runs.
	failed := make(chan error, 1)
	go func() {
		slog.Info("guardian server starting", "addr", addr, "api", displayAddr, "trusted_proxies", server.trustedProxies)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			failed <- err
		}
	}()

	select {
	case err := <-failed:
		slog.Error("server failed", "error", err)
		server.Close()
		os.Exit(1)
	case <-done:
	}
	slog.Info("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("server stopped")
}
