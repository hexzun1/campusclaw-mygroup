package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"campusclaw/backend/internal/auth"
	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/materials"
	"campusclaw/backend/seed"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}

	conn, err := db.Open(cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, cfg.DBPassword)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer conn.Close()

	if err := seed.Run(context.Background(), conn, seed.Config{
		TeacherAPassword:  cfg.SeedTeacherAPassword,
		StudentA1Password: cfg.SeedStudentA1Password,
		StudentB1Password: cfg.SeedStudentB1Password,
		UploadDir:         cfg.UploadDir,
	}); err != nil {
		log.Fatalf("seed: %v", err)
	}

	limiter := auth.NewLoginLimiter(cfg.LoginMaxFailures, cfg.LoginLockSeconds)
	authHandlers := auth.NewHandlers(conn, cfg.SessionTTL, limiter)
	requireSession := auth.RequireSession(conn)
	materialsHandlers := materials.NewHandlers(conn)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/login", authHandlers.Login)
	mux.Handle("POST /api/logout", requireSession(http.HandlerFunc(authHandlers.Logout)))
	mux.Handle("GET /api/me", requireSession(http.HandlerFunc(authHandlers.Me)))
	mux.Handle("GET /api/materials", requireSession(http.HandlerFunc(materialsHandlers.List)))
	mux.Handle("GET /api/materials/{id}", requireSession(http.HandlerFunc(materialsHandlers.Detail)))
	mux.Handle("GET /api/materials/{id}/file", requireSession(materialsHandlers.File(cfg.UploadDir)))
	mux.Handle("POST /api/materials", requireSession(materialsHandlers.Upload(materials.UploadConfig{
		UploadDir:      cfg.UploadDir,
		MaxUploadBytes: cfg.MaxUploadBytes,
	})))

	addr := ":" + cfg.APIPort
	log.Printf("campusclaw backend listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
