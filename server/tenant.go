package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ctxKey int

const tenantKey ctxKey = iota

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
