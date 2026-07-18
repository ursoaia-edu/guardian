package main

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
)

const (
	// maxRequestBodyBytes caps the request body of EVERY route. A megabyte is
	// generous for any payload the API accepts, and anything over it is
	// rejected before it reaches a JSON decoder or argon2id rather than spent
	// as memory. Applying it globally rather than only to the unauthenticated
	// routes is deliberate: holding a session is not a licence to post a
	// gigabyte, and a cap that has to be remembered per route is a cap that
	// will be forgotten on the next one.
	maxRequestBodyBytes = 1 << 20 // 1 MiB

	// rateLimitWindow is the window every per-IP limit below shares.
	rateLimitWindow = time.Minute

	// loginRateLimit and registerRateLimit protect the argon2id work R9
	// deliberately spends on every login attempt, hit or miss, to close a
	// timing oracle. Without a limiter that correctness fix is simultaneously
	// a memory-amplification denial of service: a few hundred concurrent
	// requests with garbage emails, no authentication needed.
	loginRateLimit    = 10
	registerRateLimit = 10

	// enrollRateLimit is higher than login/register on purpose: an installer
	// run across a fleet of machines behind one NAT (a school, an office) is
	// legitimate traffic, not abuse.
	enrollRateLimit = 20

	// maxAgentSyncBodyBytes caps what an agent posts on every sync. The body
	// is telemetry that lands in computers.runtime, so this bounds the row as
	// much as the request: without it one agent token could grow its machine's
	// row by megabytes every twenty seconds.
	maxAgentSyncBodyBytes = 64 << 10 // 64 KiB
)

// limitBody caps the request body for one route. A body over the cap surfaces
// as a decode error in the handler — a 400 on the routes that decode strictly,
// an empty telemetry object on the sync route, which never fails on telemetry.
func limitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// perIP rate-limits a route by the client IP resolved by the client-IP
// middleware installed below. The design spec calls for per-email keying on
// login too; reading the body to key on it would conflict with the body
// cap's MaxBytesReader ordering, so only per-IP is implemented for this plan
// (see specs/server.md).
func perIP(limit int) func(http.Handler) http.Handler {
	return httprate.LimitBy(limit, rateLimitWindow, func(r *http.Request) (string, error) {
		return httprate.CanonicalizeIP(middleware.GetClientIP(r.Context())), nil
	})
}

// requestTimeout cancels a handler's context — and with it every query it
// has in flight — before the http.Server's 15-second write timeout closes
// the connection underneath it. Below that limit on purpose: a handler that
// outlives the connection would finish its work for nobody.
const requestTimeout = 10 * time.Second

// clientIPMiddleware picks how the client address is resolved from
// TRUSTED_PROXIES. With no proxy in front (the default), the TCP peer is the
// client and X-Forwarded-For is ignored — a header the client can write is
// not evidence of anything. Behind N proxies, the entry N hops from the
// right of X-Forwarded-For is the client; the compose deploy in dist/server
// sets 1 for Caddy. Getting the count wrong in either direction is visible
// rather than exploitable: too low behind a proxy buckets everyone under the
// proxy's address (an outage of rate limiting, not a bypass), too high sets
// no IP at all and every limiter shares one bucket.
func (s *Server) clientIPMiddleware() func(http.Handler) http.Handler {
	if s.trustedProxies == 0 {
		return middleware.ClientIPFromRemoteAddr
	}
	return middleware.ClientIPFromXFFTrustedProxies(s.trustedProxies)
}

func (s *Server) setupRoutes() *chi.Mux {
	r := chi.NewRouter()

	// Order matters: the id and the client IP are resolved first so every
	// later log line carries them; the logger wraps the recoverer so a panic
	// is logged as the 500 it became; the timeout sits inside both so its
	// 504 is logged like any other status.
	r.Use(middleware.RequestID)
	r.Use(s.clientIPMiddleware())
	r.Use(requestLogger)
	r.Use(recoverer)
	r.Use(middleware.Timeout(requestTimeout))
	r.Use(limitBody(maxRequestBodyBytes))

	// NewServer refuses to start without CABINET_ORIGIN, so an empty list here
	// is only reachable from a test that builds a Server directly — and an
	// empty allow-list denies every cross-origin request, which is the right
	// way for this to fail.
	origins, _ := cabinetOriginsFromEnv()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", accountHeader},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Unauthenticated
	r.Get("/health", s.handleHealth)

	// The binding token in the body is this route's gate; see handlers_agent.go.
	r.With(perIP(enrollRateLimit)).Post("/agent/enroll", s.handleEnroll)

	r.Route("/api/v1/auth", func(r chi.Router) {
		r.With(perIP(registerRateLimit)).Post("/register", s.handleRegister)
		r.With(perIP(loginRateLimit)).Post("/login", s.handleLogin)
		r.Post("/logout", s.handleLogout)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.SessionAuth)

		// Reachable by a room guest. Every route here is either narrowed to
		// the caller's own grants by its query (ListRoomsForMember,
		// ListComputersForMember) or gated on assertRoomVisible, and
		// handlePatchComputer additionally refuses a guest the one move that
		// is account-wide in effect. Adding a route to this group is a
		// decision to let somebody else's guest reach it.
		r.Group(func(r chi.Router) {
			r.Get("/me", s.handleMe)

			r.Get("/rooms", s.handleListRooms)
			r.Get("/rooms/{roomID}", s.handleGetRoom)
			r.Patch("/rooms/{roomID}", s.handlePatchRoom)
			r.Get("/rooms/{roomID}/applications", s.handleListRoomApplications)
			r.Post("/rooms/{roomID}/applications", s.handleAddRoomApplication)
			r.Patch("/rooms/{roomID}/applications/{appID}", s.handlePatchRoomApplication)
			r.Delete("/rooms/{roomID}/applications/{appID}", s.handleDeleteRoomApplication)
			r.Get("/rooms/{roomID}/members", s.handleListRoomMembers)

			r.Get("/computers", s.handleListComputers)
			r.Get("/computers/{computerID}", s.handleGetComputer)
			r.Patch("/computers/{computerID}", s.handlePatchComputer)
		})

		// Account-wide. The guard is the middleware, not a line each handler
		// has to remember to write — see ManagerOnly in middleware.go.
		r.Group(func(r chi.Router) {
			r.Use(s.ManagerOnly)

			r.Post("/rooms", s.handleCreateRoom)
			r.Delete("/rooms/{roomID}", s.handleDeleteRoom)

			r.Post("/rooms/{roomID}/members", s.handleAddRoomMember)
			r.Delete("/rooms/{roomID}/members/{userID}", s.handleDeleteRoomMember)

			// Unenrolling is account-wide even when the machine sits in a
			// guest's room: it revokes that machine's credential.
			r.Delete("/computers/{computerID}", s.handleDeleteComputer)

			r.Get("/account/members", s.handleListAccountMembers)
			r.Post("/account/members", s.handleAddAccountMember)
			r.Delete("/account/members/{userID}", s.handleDeleteAccountMember)

			r.Post("/binding-tokens", s.handleCreateBindingToken)
			r.Delete("/binding-tokens", s.handleRevokeBindingTokens)

			r.Get("/events", s.handleListEvents)
		})
	})

	r.Group(func(r chi.Router) {
		r.Use(s.AgentAuth)
		// POST, not GET: the telemetry rides in a JSON body rather than a
		// query string, so it is not written to every proxy's access log,
		// not subject to URL length limits, and capped by size here.
		r.With(limitBody(maxAgentSyncBodyBytes)).Post("/agent/sync", s.handleAgentSync)
	})

	return r
}
