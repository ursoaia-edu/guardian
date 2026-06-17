package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server holds the state of the guardian server
type Server struct {
	pool *pgxpool.Pool
}

func NewServer(ctx context.Context) (*Server, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
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
	return &Server{pool: pool}, nil
}

func (s *Server) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

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

	if err := loadEnvFile(".env"); err != nil && !os.IsNotExist(err) {
		slog.Warn("could not load .env file", "error", err)
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
		addr = parts[len(parts)-1]
	}

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		fmt.Printf("Guardian Server starting on %s\n", addr)
		fmt.Printf("API: %s\n", displayAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("server stopped")
}
