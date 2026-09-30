package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"campusclaw/backend/internal/auth"
	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/gateway"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/index"
	"campusclaw/backend/internal/materials"
	"campusclaw/backend/internal/vector"
	"campusclaw/backend/migrations"
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

	// Apply pending migrations before anything reads the schema. The MySQL
	// image's initdb.d scripts only run on an empty data volume, so an existing
	// volume depends entirely on this step (design.md Decision 2).
	if err := db.Migrate(context.Background(), cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, cfg.DBPassword, migrations.FS); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// Idempotent startup DDL: existing data volumes never re-run the MySQL
	// image's initdb.d scripts (design.md Decision 3).
	if err := db.EnsureRevokedTokens(context.Background(), conn); err != nil {
		log.Fatalf("ensure schema: %v", err)
	}

	if err := seed.Run(context.Background(), conn, seed.Config{
		TeacherAPassword:  cfg.SeedTeacherAPassword,
		StudentA1Password: cfg.SeedStudentA1Password,
		StudentB1Password: cfg.SeedStudentB1Password,
		UploadDir:         cfg.UploadDir,
	}); err != nil {
		log.Fatalf("seed: %v", err)
	}

	// External services are constructed but not contacted here: the vector
	// collection is ensured on first use, so the api starts even when Qdrant or
	// a gateway is down (design.md Decision 3).
	embeddingClient := gateway.NewEmbeddingClient(gateway.EmbeddingConfig{
		BaseURL: cfg.EmbeddingBaseURL,
		APIKey:  cfg.EmbeddingAPIKey,
		Model:   cfg.EmbeddingModel,
		Dim:     cfg.EmbeddingDim,
		Batch:   cfg.EmbeddingBatchSize,
		Timeout: cfg.GatewayTimeout,
	})
	vectorClient := vector.NewClient(cfg.QdrantURL, cfg.QdrantAPIKey, cfg.EmbeddingDim, cfg.GatewayTimeout)
	indexer := index.New(index.SQLChunkStore{Conn: conn}, embeddingClient, vectorClient, cfg.EmbeddingBatchSize, cfg.IndexTimeout)

	// Startup compensation scan (design.md Decision 6): materials that predate
	// the vector pipeline get their chunks generated and indexed, and indexing
	// runs that were interrupted mid-way are finished. It is idempotent, and a
	// gateway or vector store being down only marks the affected chunks failed —
	// the api still starts and keyword search still works.
	if err := index.Reconcile(context.Background(), index.SQLReconcileStore{Conn: conn}, indexer); err != nil {
		log.Printf("startup reconcile: %v", err)
	}

	limiter := auth.NewLoginLimiter(cfg.LoginMaxFailures, cfg.LoginLockSeconds)
	tokenIssuer := auth.NewTokenIssuer(cfg.JWTSecret, cfg.JWTTTL)
	authHandlers := auth.NewHandlers(conn, tokenIssuer, limiter)
	requireAuth := auth.RequireAuth(conn, tokenIssuer)

	materialsHandlers := materials.NewHandlers(conn, indexer, vectorClient)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Every /api response — success or 401/404/503 — is non-cacheable, so a
	// cached authenticated response can never be replayed after logout
	// (design.md Decision 7). /health is unaffected.
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("POST /api/login", authHandlers.Login)
	apiMux.Handle("POST /api/logout", requireAuth(http.HandlerFunc(authHandlers.Logout)))
	apiMux.Handle("GET /api/me", requireAuth(http.HandlerFunc(authHandlers.Me)))
	apiMux.Handle("GET /api/materials", requireAuth(http.HandlerFunc(materialsHandlers.List)))
	apiMux.Handle("GET /api/materials/{id}", requireAuth(http.HandlerFunc(materialsHandlers.Detail)))
	apiMux.Handle("GET /api/materials/{id}/file", requireAuth(materialsHandlers.File(cfg.UploadDir)))
	apiMux.Handle("POST /api/materials", requireAuth(materialsHandlers.Upload(materials.UploadConfig{
		UploadDir:      cfg.UploadDir,
		MaxUploadBytes: cfg.MaxUploadBytes,
	})))
	apiMux.Handle("POST /api/materials/{id}/reindex", requireAuth(http.HandlerFunc(materialsHandlers.Reindex)))
	mux.Handle("/api/", httpapi.NoStore(apiMux))

	addr := ":" + cfg.APIPort
	log.Printf("campusclaw backend listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
