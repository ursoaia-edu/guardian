package main

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
)

func (s *Server) setupRoutes() *chi.Mux {
	r := chi.NewRouter()

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
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
		r.Delete("/rooms/{roomID}", s.handleDeleteRoom)
		r.Get("/rooms/{roomID}/applications", s.handleListRoomApplications)
		r.Post("/rooms/{roomID}/applications", s.handleAddRoomApplication)
		r.Delete("/rooms/{roomID}/applications/{appID}", s.handleDeleteRoomApplication)

		r.Get("/computers", s.handleListComputers)
		r.Patch("/computers/{computerID}", s.handlePatchComputer)

		r.Post("/binding-tokens", s.handleCreateBindingToken)
	})

	// Legacy single-tenant routes, disabled from Task 3 onward: NewServer no
	// longer populates s.db, so any of these handlers would panic on a nil
	// pointer at request time. Tasks 5 through 12 add the new, tenant-scoped
	// routes; Task 15 deletes this block along with db.go and the legacy
	// handlers it calls.
	//
	// // Client auth (agent endpoints)
	// r.Group(func(r chi.Router) {
	// 	r.Use(ClientAuth)
	// 	r.Get("/client/sync", s.handleClientSync)
	// })
	//
	// // Admin auth (management endpoints)
	// r.Group(func(r chi.Router) {
	// 	r.Use(AdminAuth)
	//
	// 	r.Get("/manage/applications", s.handleGetAllApplications)
	// 	r.Post("/manage/applications", s.handleAddApplication)
	// 	r.Put("/manage/applications", s.handleUpdateApplication)
	// 	r.Delete("/manage/applications", s.handleRemoveApplication)
	// 	r.Delete("/manage/applications/reset", s.handleResetApplications)
	//
	// 	r.Get("/status", s.handleGetStatus)
	// 	r.Put("/status", s.handleUpdateStatus)
	//
	// 	r.Get("/info", s.handleGetServerInfo)
	//
	// 	r.Get("/client", s.handleGetClient)
	// 	r.Put("/client", s.handleUpdateClient)
	//
	// 	r.Get("/manage/computers", s.handleGetComputers)
	// 	r.Put("/manage/computers", s.handleUpdateComputer)
	// 	r.Delete("/manage/computers/reset", s.handleResetComputers)
	// 	r.Put("/manage/computers/block_all", s.handleBlockAllComputers)
	// })

	return r
}
