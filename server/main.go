package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// Server holds the state of the procsentinel server
type Server struct {
	mu           sync.RWMutex
	db           *sql.DB
	appsCache    map[string]Application
	enabledCache bool
	modeCache    string
	clientCache  map[string]bool
}

func NewServer() (*Server, error) {
	db, err := openDatabase("./procsentinel.db")
	if err != nil {
		return nil, err
	}

	s := &Server{
		db:          db,
		appsCache:   make(map[string]Application),
		modeCache:   "blacklist",
		clientCache: make(map[string]bool),
	}

	if err := s.initDatabase(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %v", err)
	}
	if err := s.loadFromDatabase(); err != nil {
		return nil, fmt.Errorf("failed to load data: %v", err)
	}

	return s, nil
}

func (s *Server) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if err := loadEnvFile(".env"); err != nil {
		slog.Warn("could not load .env file", "error", err)
	}

	server, err := NewServer()
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
		fmt.Printf("ProcSentinel Server starting on %s\n", addr)
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
