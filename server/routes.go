package main

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
)

const (
	// maxUnauthenticatedBodyBytes caps the request body accepted by the three
	// routes below that run with no session and no agent token: register,
	// login, and enroll. A megabyte is generous for any of their payloads.
	// Anything over the cap is rejected as a 400 before it reaches a JSON
	// decoder or argon2id, not spent as memory.
	maxUnauthenticatedBodyBytes = 1 << 20 // 1 MiB

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
)

// maxBody caps the request body for one route. A body over the cap surfaces
// as a decode error in the handler, which is already reported as 400.
func maxBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxUnauthenticatedBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// perIP rate-limits a route by the client IP resolved by
// middleware.ClientIPFromXFFTrustedProxies, installed once below. The design
// spec calls for per-email keying on login too; reading the body to key on it
// would conflict with maxBody's MaxBytesReader ordering, so only per-IP is
// implemented for this plan (see specs/server.md).
func perIP(limit int) func(http.Handler) http.Handler {
	return httprate.LimitBy(limit, rateLimitWindow, func(r *http.Request) (string, error) {
		return httprate.CanonicalizeIP(middleware.GetClientIP(r.Context())), nil
	})
}

func (s *Server) setupRoutes() *chi.Mux {
	r := chi.NewRouter()

	// The server sits behind exactly one reverse proxy in this deployment
	// (Caddy — see specs/2026-09-05-saas-design.md's deploy section), so the
	// real client IP is the single entry ClientIPFromXFFTrustedProxies adds
	// to X-Forwarded-For. Without this, rate limiting below would key on
	// Caddy's own address and bucket every client together.
	r.Use(middleware.ClientIPFromXFFTrustedProxies(1))

	origins := []string{"http://localhost:5173"}
	if o := os.Getenv("CABINET_ORIGIN"); o != "" {
		origins = strings.Split(o, ",")
	}
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Unauthenticated
	r.Get("/health", handleHealth)

	// The binding token in the body is this route's gate; see handlers_agent.go.
	r.With(maxBody, perIP(enrollRateLimit)).Post("/agent/enroll", s.handleEnroll)

	r.Route("/api/v1/auth", func(r chi.Router) {
		r.With(maxBody, perIP(registerRateLimit)).Post("/register", s.handleRegister)
		r.With(maxBody, perIP(loginRateLimit)).Post("/login", s.handleLogin)
		r.Post("/logout", s.handleLogout)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.SessionAuth)
		r.Get("/me", s.handleMe)

		r.Get("/rooms", s.handleListRooms)
		r.Post("/rooms", s.handleCreateRoom)
		r.Get("/rooms/{roomID}", s.handleGetRoom)
		r.Patch("/rooms/{roomID}", s.handlePatchRoom)
		r.Delete("/rooms/{roomID}", s.handleDeleteRoom)
		r.Get("/rooms/{roomID}/applications", s.handleListRoomApplications)
		r.Post("/rooms/{roomID}/applications", s.handleAddRoomApplication)
		r.Delete("/rooms/{roomID}/applications/{appID}", s.handleDeleteRoomApplication)

		r.Get("/rooms/{roomID}/members", s.handleListRoomMembers)
		r.Post("/rooms/{roomID}/members", s.handleAddRoomMember)
		r.Delete("/rooms/{roomID}/members/{userID}", s.handleDeleteRoomMember)

		r.Get("/computers", s.handleListComputers)
		r.Patch("/computers/{computerID}", s.handlePatchComputer)

		r.Post("/binding-tokens", s.handleCreateBindingToken)
		r.Delete("/binding-tokens", s.handleRevokeBindingTokens)

		r.Get("/events", s.handleListEvents)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.AgentAuth)
		r.Get("/agent/sync", s.handleAgentSync)
	})

	return r
}
