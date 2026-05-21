package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"server/internal/db"
)

type ctxKey int

const (
	tenantKey ctxKey = iota
	computerKey
)

// Tenant is who the caller is, resolved server-side from a session or an agent
// token. It is never built from request parameters.
type Tenant struct {
	AccountID uuid.UUID
	UserID    uuid.UUID // zero for agent requests
}

func withTenant(ctx context.Context, t Tenant) context.Context {
	return context.WithValue(ctx, tenantKey, t)
}

func tenantFrom(ctx context.Context) (Tenant, bool) {
	t, ok := ctx.Value(tenantKey).(Tenant)
	return t, ok
}

func computerFrom(ctx context.Context) (db.Computer, bool) {
	c, ok := ctx.Value(computerKey).(db.Computer)
	return c, ok
}

// mustTenant returns the tenant a SessionAuth-protected handler is running for.
// Reaching such a handler without one is a routing mistake, not a client error:
// discarding the ok and proceeding would serve a zero account id — the nil
// UUID — as if it were a real account. It fails loudly instead.
func mustTenant(w http.ResponseWriter, r *http.Request) (Tenant, bool) {
	t, ok := tenantFrom(r.Context())
	if !ok {
		slog.Error("handler reached without a tenant in context", "path", r.URL.Path)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return Tenant{}, false
	}
	return t, true
}

// writeLookupError maps the error from a scoped lookup. RLS makes another
// account's row indistinguishable from an absent one — both come back as no
// rows — so both are a 404 and neither is logged as a problem. Anything else is
// a real failure: a dropped connection, a cancelled context, a broken query.
// Those must be a logged 500, or an outage arrives at the operator disguised as
// a flood of "not found".
//
// It lives here beside mustTenant: both encode the same idea, that a
// handler's view of the world is already narrowed by the account scope.
func writeLookupError(w http.ResponseWriter, r *http.Request, err error, what string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: what + " not found"})
		return
	}
	slog.Error("scoped lookup failed", "path", r.URL.Path, "error", err)
	writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
}

// inAccount runs fn inside a transaction scoped to one account. The GUC is set
// with set_config(..., true), which makes it local to this transaction, so a
// pooled connection cannot leak the scope into the next request.
func (s *Server) inAccount(ctx context.Context, accountID uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.account_id', $1, true)`, accountID.String()); err != nil {
		return fmt.Errorf("scope transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
