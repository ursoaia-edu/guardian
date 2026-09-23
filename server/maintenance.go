package main

import (
	"context"
	"log/slog"
	"time"

	"server/internal/db"
)

const (
	// maintenanceInterval is how often the housekeeping below runs. Nothing it
	// does is urgent — an expired session is already unusable and an old event
	// is already unread — so this is deliberately slow.
	maintenanceInterval = time.Hour

	// eventRetention is how long the activity feed keeps a row. Long enough to
	// answer "what happened to this machine last season?", short enough that a
	// busy account's feed does not grow without bound forever.
	eventRetention = 180 * 24 * time.Hour

	// processEventRetention is shorter than eventRetention on purpose. The
	// audit feed is a narrative worth keeping a season; the blocking log is
	// machine output, orders of magnitude larger, and nobody asks what was
	// killed six months ago.
	processEventRetention = 30 * 24 * time.Hour
)

// runMaintenance purges expired sessions and aged-out events until ctx is
// cancelled. It runs in one process, on a timer, rather than as a database job
// or an external cron: both of those are another thing to deploy and another
// thing to forget, and this work is cheap enough that a second server running
// it too would only find nothing to do.
func (s *Server) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.purgeOnce(ctx)
		}
	}
}

func (s *Server) purgeOnce(ctx context.Context) {
	// Bounded well under the interval: a purge that outlives its own tick
	// would stack up behind the next one.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if n, err := db.New(s.pool).DeleteExpiredSessions(ctx); err != nil {
		slog.Error("purge expired sessions", "error", err)
	} else if n > 0 {
		slog.Info("purged expired sessions", "count", n)
	}

	// events carries an RLS policy keyed on the account scope, so the
	// application role cannot delete across accounts directly; migration
	// 00016 provides a SECURITY DEFINER function that does only this.
	var purged int64
	if err := s.pool.QueryRow(ctx,
		`SELECT purge_old_events($1::interval)`, eventRetention).Scan(&purged); err != nil {
		slog.Error("purge old events", "error", err)
	} else if purged > 0 {
		slog.Info("purged old events", "count", purged, "retention", eventRetention.String())
	}

	// email_tokens has no RLS (it has no account_id), so this needs no
	// SECURITY DEFINER function — the application role can delete directly.
	if n, err := db.New(s.pool).PurgeExpiredEmailTokens(ctx); err != nil {
		slog.Error("purge expired email tokens", "error", err)
	} else if n > 0 {
		slog.Info("purged expired email tokens", "count", n)
	}

	// Same RLS reasoning as the events purge above; migration 00018 provides
	// the function.
	var purgedKills int64
	if err := s.pool.QueryRow(ctx,
		`SELECT purge_old_process_events($1::interval)`, processEventRetention).Scan(&purgedKills); err != nil {
		slog.Error("purge old process events", "error", err)
	} else if purgedKills > 0 {
		slog.Info("purged old process events", "count", purgedKills,
			"retention", processEventRetention.String())
	}
}
