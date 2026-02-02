package main

import (
	"database/sql"
	"fmt"
	"log/slog"
)

func openDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}
	return db, nil
}

func (s *Server) initDatabase() error {
	statements := []struct {
		label string
		sql   string
	}{
		{"applications", `
			CREATE TABLE IF NOT EXISTS applications (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				enabled BOOLEAN NOT NULL DEFAULT 1,
				mode TEXT NOT NULL DEFAULT 'blacklist',
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(name, mode)
			);
			CREATE INDEX IF NOT EXISTS idx_apps_name_mode ON applications(name, mode);
		`},
		{"server", `
			CREATE TABLE IF NOT EXISTS server (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				enabled BOOLEAN NOT NULL DEFAULT 0,
				mode TEXT NOT NULL DEFAULT 'blacklist',
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			INSERT OR IGNORE INTO server (id, enabled, mode) VALUES (1, 0, 'blacklist');
		`},
		{"client", `
			CREATE TABLE IF NOT EXISTS client (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT UNIQUE NOT NULL,
				status BOOLEAN NOT NULL DEFAULT 1,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			CREATE INDEX IF NOT EXISTS idx_client_name ON client(name);
			INSERT OR IGNORE INTO client (name, status) VALUES ('power', 1);
		`},
		{"computers", `
			CREATE TABLE IF NOT EXISTS computers (
				identity INTEGER PRIMARY KEY,
				blocked BOOLEAN NOT NULL DEFAULT 0,
				datetime DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			CREATE INDEX IF NOT EXISTS idx_computers_identity ON computers(identity);
		`},
	}

	for _, stmt := range statements {
		if _, err := s.db.Exec(stmt.sql); err != nil {
			return fmt.Errorf("failed to create %s table: %v", stmt.label, err)
		}
	}

	// Migrate: if applications table has old UNIQUE(name) constraint, recreate it
	s.migrateApplicationsTable()

	return nil
}

func (s *Server) migrateApplicationsTable() {
	// Check if the old unique index on name only exists
	var indexSQL string
	err := s.db.QueryRow("SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_apps_name'").Scan(&indexSQL)
	if err != nil {
		// Old index doesn't exist — no migration needed
		return
	}

	slog.Info("migrating applications table: UNIQUE(name) -> UNIQUE(name, mode)")

	// Recreate table with new constraint
	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("migration: failed to begin transaction", "error", err)
		return
	}

	migrations := []string{
		"ALTER TABLE applications RENAME TO applications_old",
		`CREATE TABLE applications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT 1,
			mode TEXT NOT NULL DEFAULT 'blacklist',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(name, mode)
		)`,
		"INSERT INTO applications (id, name, enabled, mode, created_at) SELECT id, name, enabled, mode, created_at FROM applications_old",
		"DROP TABLE applications_old",
		"DROP INDEX IF EXISTS idx_apps_name",
		"CREATE INDEX IF NOT EXISTS idx_apps_name_mode ON applications(name, mode)",
	}

	for _, sql := range migrations {
		if _, err := tx.Exec(sql); err != nil {
			slog.Error("migration failed, rolling back", "sql", sql, "error", err)
			tx.Rollback()
			return
		}
	}

	if err := tx.Commit(); err != nil {
		slog.Error("migration: failed to commit", "error", err)
		return
	}

	slog.Info("migration complete: applications table updated")
}

func (s *Server) loadFromDatabase() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load applications into cache
	rows, err := s.db.Query("SELECT id, name, enabled, mode FROM applications")
	if err != nil {
		return fmt.Errorf("failed to query applications: %v", err)
	}
	defer rows.Close()

	s.appsCache = make(map[string]Application)
	for rows.Next() {
		var app Application
		if err := rows.Scan(&app.ID, &app.Name, &app.Enabled, &app.Mode); err != nil {
			return fmt.Errorf("failed to scan application: %v", err)
		}
		s.appsCache[appCacheKey(app.Name, app.Mode)] = app
	}

	// Load server status
	var enabled bool
	var mode string
	err = s.db.QueryRow("SELECT enabled, mode FROM server WHERE id = 1").Scan(&enabled, &mode)
	if err != nil {
		return fmt.Errorf("failed to query server status: %v", err)
	}
	s.enabledCache = enabled
	s.modeCache = mode

	// Load client data
	clientRows, err := s.db.Query("SELECT name, status FROM client")
	if err != nil {
		return fmt.Errorf("failed to query client data: %v", err)
	}
	defer clientRows.Close()

	s.clientCache = make(map[string]bool)
	for clientRows.Next() {
		var name string
		var status bool
		if err := clientRows.Scan(&name, &status); err != nil {
			return fmt.Errorf("failed to scan client data: %v", err)
		}
		s.clientCache[name] = status
	}

	slog.Info("loaded data from database",
		"applications", len(s.appsCache),
		"client_entries", len(s.clientCache),
		"enabled", s.enabledCache,
	)
	return nil
}

func (s *Server) saveApplicationToDatabase(name string, mode string) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO applications (name, mode) VALUES (?, ?)", name, mode)
	return err
}

func (s *Server) removeApplicationFromDatabase(name string) error {
	_, err := s.db.Exec("DELETE FROM applications WHERE name = ?", name)
	return err
}

func (s *Server) saveStatusToDatabase(enabled bool, mode string) error {
	_, err := s.db.Exec("UPDATE server SET enabled = ?, mode = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1", enabled, mode)
	return err
}

func (s *Server) resetApplicationsDatabase() error {
	_, err := s.db.Exec("DELETE FROM applications")
	return err
}

func (s *Server) saveClientToDatabase(name string, status bool) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO client (name, status, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)", name, status)
	return err
}

func (s *Server) updateComputerDateTime(identity int) error {
	_, err := s.db.Exec("INSERT INTO computers (identity, blocked, datetime) VALUES (?, 1, CURRENT_TIMESTAMP) ON CONFLICT(identity) DO UPDATE SET datetime = CURRENT_TIMESTAMP", identity)
	return err
}
