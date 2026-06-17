package main

import (
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
)

func (s *Server) setupRoutes() *chi.Mux {
	r := chi.NewRouter()

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
	r.Post("/agent/enroll", s.handleEnroll)

	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Post("/register", s.handleRegister)
		r.Post("/login", s.handleLogin)
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
