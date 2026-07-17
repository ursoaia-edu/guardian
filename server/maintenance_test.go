package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

// seedSession inserts a session row directly, so a test can put one in a state
// that would take a year of real use to reach.
func seedSession(t *testing.T, s *Server, userID uuid.UUID, plain string, created, expires time.Time) {
	t.Helper()
	_, err := s.pool.Exec(context.Background(), `
		INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_used_at)
		VALUES ($1, $2, $3, $4, $5)`,
		hashToken(plain), userID, created, expires, created)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func userIDOf(t *testing.T, s *Server, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := observe(t).QueryRow(context.Background(),
		`SELECT id FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatalf("user lookup: %v", err)
	}
	return id
}

// Sliding renewal with no ceiling never expires: a session used once a week
// lives forever, so a token taken from a device that stays in use is good
// forever too. The renewal is capped at a year from the session's creation.
func TestSessionRenewalCannotOutrunItsCeiling(t *testing.T) {
	s := &Server{pool: testPool(t)}
	registerAndLogin(t, s, "parent@example.com")
	userID := userIDOf(t, s, "parent@example.com")

	// Created over a year ago and still being used: exactly the session the
	// ceiling exists for.
	created := time.Now().Add(-400 * 24 * time.Hour)
	seedSession(t, s, userID, "old-but-active", created, time.Now().Add(24*time.Hour))

	ctx := context.Background()
	if err := db.New(s.pool).TouchSession(ctx, db.TouchSessionParams{
		TokenHash: hashToken("old-but-active"),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(sessionTTL), Valid: true},
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}

	var expires time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT expires_at FROM sessions WHERE token_hash = $1`,
		hashToken("old-but-active")).Scan(&expires); err != nil {
		t.Fatalf("read back: %v", err)
	}
	ceiling := created.Add(365 * 24 * time.Hour)
	if expires.After(ceiling.Add(time.Minute)) {
		t.Fatalf("renewal pushed expiry to %s, past the ceiling of %s", expires, ceiling)
	}
	// Which for this session means it is now over: renewal cannot revive it.
	if _, err := db.New(s.pool).GetSession(ctx, hashToken("old-but-active")); err == nil {
		t.Fatal("a session past its absolute ceiling still authenticates")
	}

	// The control: an ordinary session renews normally, so the ceiling is not
	// simply breaking renewal for everyone.
	seedSession(t, s, userID, "fresh", time.Now().Add(-2*time.Hour), time.Now().Add(24*time.Hour))
	if err := db.New(s.pool).TouchSession(ctx, db.TouchSessionParams{
		TokenHash: hashToken("fresh"),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(sessionTTL), Valid: true},
	}); err != nil {
		t.Fatalf("touch fresh: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT expires_at FROM sessions WHERE token_hash = $1`, hashToken("fresh")).Scan(&expires); err != nil {
		t.Fatalf("read back fresh: %v", err)
	}
	if time.Until(expires) < 20*24*time.Hour {
		t.Fatalf("an ordinary session was not renewed: expires %s", expires)
	}
}

// Housekeeping: expired sessions and aged-out events are removed, and nothing
// still in use is.
func TestPurgeRemovesOnlyWhatIsFinished(t *testing.T) {
	s := &Server{pool: testPool(t)}
	ctx := context.Background()
	live := registerAndLogin(t, s, "parent@example.com")
	userID := userIDOf(t, s, "parent@example.com")
	account := accountIDOf(t, s, "parent@example.com")

	seedSession(t, s, userID, "long-expired",
		time.Now().Add(-90*24*time.Hour), time.Now().Add(-3*24*time.Hour))

	// Events are RLS-scoped, so they are seeded as the owner role, which is
	// also the only way to count them independently afterwards.
	obs := observe(t)
	for _, age := range []time.Duration{200 * 24 * time.Hour, time.Hour} {
		if _, err := obs.Exec(ctx, `
			INSERT INTO events (account_id, type, created_at)
			VALUES ($1, 'test.event', $2)`, account, time.Now().Add(-age)); err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}

	s.purgeOnce(ctx)

	var sessions int
	if err := obs.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token_hash = $1`,
		hashToken("long-expired")).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatal("an expired session survived the purge")
	}

	var events int
	if err := obs.QueryRow(ctx, `SELECT count(*) FROM events WHERE type = 'test.event'`).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Fatalf("%d test events left, want only the recent one", events)
	}

	// The live session still works: the purge took nothing it should not.
	if rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/me", nil, live); rr.Code != 200 {
		t.Fatalf("the purge invalidated a live session: %d %s", rr.Code, rr.Body.String())
	}
}

// Every agent in the fleet syncs on a timer whether anything changed or not,
// and each sync used to rewrite a jsonb column. The write is throttled; the
// policy the sync returns is not.
func TestTelemetryWritesAreThrottled(t *testing.T) {
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")
	_, agentToken := enroll(t, s, mintBindingToken(t, s, c), "guid-1", "PC-1")

	lastSeen := func() *time.Time {
		t.Helper()
		var at *time.Time
		if err := observe(t).QueryRow(context.Background(),
			`SELECT last_seen_at FROM computers WHERE machine_guid = 'guid-1'`).Scan(&at); err != nil {
			t.Fatalf("read last_seen_at: %v", err)
		}
		return at
	}

	if code, _ := agentSync(t, s, agentToken); code != 200 {
		t.Fatalf("first sync: %d", code)
	}
	first := lastSeen()
	if first == nil {
		t.Fatal("the first sync recorded no last_seen_at")
	}

	if code, out := agentSync(t, s, agentToken); code != 200 {
		t.Fatalf("second sync: %d", code)
	} else if out.Mode == "" {
		t.Fatal("the throttled sync returned no policy")
	}
	second := lastSeen()
	if !second.Equal(*first) {
		t.Fatalf("a sync %s after the last one wrote the row again", time.Since(*first))
	}
}
