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

	// Client auth (agent endpoints)
	r.Group(func(r chi.Router) {
		r.Use(ClientAuth)
		r.Get("/client/sync", s.handleClientSync)
	})

	// Admin auth (management endpoints)
	r.Group(func(r chi.Router) {
		r.Use(AdminAuth)

		r.Get("/manage/applications", s.handleGetAllApplications)
		r.Post("/manage/applications", s.handleAddApplication)
		r.Put("/manage/applications", s.handleUpdateApplication)
		r.Delete("/manage/applications", s.handleRemoveApplication)
		r.Delete("/manage/applications/reset", s.handleResetApplications)

		r.Get("/status", s.handleGetStatus)
		r.Put("/status", s.handleUpdateStatus)

		r.Get("/info", s.handleGetServerInfo)

		r.Get("/client", s.handleGetClient)
		r.Put("/client", s.handleUpdateClient)

		r.Get("/manage/computers", s.handleGetComputers)
		r.Put("/manage/computers", s.handleUpdateComputer)
		r.Delete("/manage/computers/reset", s.handleResetComputers)
		r.Put("/manage/computers/block_all", s.handleBlockAllComputers)
	})

	return r
}
