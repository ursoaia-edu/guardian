package main

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os"
)

func getToken(envVar, fallback string) string {
	if t := os.Getenv(envVar); t != "" {
		return t
	}
	return fallback
}

func authMiddleware(envVar, fallback string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := getToken(envVar, fallback)
			expected := "Bearer " + token
			actual := r.Header.Get("Authorization")

			if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
				slog.Warn("unauthorized access attempt", "remote", r.RemoteAddr, "path", r.URL.Path)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func AdminAuth(next http.Handler) http.Handler {
	return authMiddleware("ADMIN_TOKEN", "mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z")(next)
}

func ClientAuth(next http.Handler) http.Handler {
	return authMiddleware("TOKEN", "mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z")(next)
}
