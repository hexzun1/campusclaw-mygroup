// @title CampusClaw API
// @version 1.0
// @description 面向中小学的教研智能体后端接口：认证、教学材料、检索、问答、智能体、技能与会话。
// @description 除 POST /api/login 外，所有接口都需要 `Authorization: Bearer <token>`。
// @description 班级与角色只取自 token：请求中的 class_id / role / user_id 一律被忽略。
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description 填 `Bearer <token>`（Bearer 与 token 之间有一个空格），token 来自 POST /api/login 的响应。
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"campusclaw/backend/docs"
	"campusclaw/backend/internal/ask"
	"campusclaw/backend/internal/auth"
	"campusclaw/backend/internal/config"
	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/gateway"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/index"
	"campusclaw/backend/internal/materials"
	"campusclaw/backend/internal/mcp"
	"campusclaw/backend/internal/openapi"
	"campusclaw/backend/internal/search"
	"campusclaw/backend/internal/swaggerui"
	"campusclaw/backend/internal/tutor"
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
		MCPAuthToken:      cfg.MCPAuthToken,
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
	chatClient := gateway.NewChatClient(gateway.ChatConfig{
		BaseURL: cfg.ChatBaseURL,
		APIKey:  cfg.ChatAPIKey,
		Model:   cfg.ChatModel,
		Timeout: cfg.GatewayTimeout,
	})
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
	mcpClient := mcp.NewClient(cfg.MCPConnectTimeout, cfg.MCPCallTimeout, cfg.MCPMaxResponseBytes)
	mcpHandlers := mcp.NewHandlers(conn, mcpClient)
	searchEngine := search.New(conn, embeddingClient, vectorClient)
	searchHandlers := search.NewHandlers(searchEngine)
	askHandlers := ask.NewHandlers(ask.New(searchEngine, chatClient))
	toolStage := tutor.NewToolStage(conn, mcpClient, chatClient, cfg.MCPToolStageTimeout)
	tutorHandlers := tutor.NewHandlers(conn, searchEngine, chatClient, toolStage)
	teacher := func(h http.HandlerFunc) http.Handler {
		return requireAuth(auth.RequireTeacher(h))
	}

	// The generated document is converted once and cached for the process
	// lifetime; a malformed generated document would mean the binary and its
	// docs disagree, which is a build problem worth failing on.
	openapiDoc, err := openapi.Convert(docs.SwaggerInfo.ReadDoc())
	if err != nil {
		log.Fatalf("convert openapi document: %v", err)
	}

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
	apiMux.Handle("POST /api/search", requireAuth(http.HandlerFunc(searchHandlers.Search)))
	apiMux.Handle("POST /api/ask", requireAuth(http.HandlerFunc(askHandlers.Ask)))
	apiMux.Handle("GET /api/agents", requireAuth(http.HandlerFunc(tutorHandlers.ListAgents)))
	apiMux.Handle("POST /api/agents", teacher(tutorHandlers.CreateAgent))
	apiMux.Handle("PUT /api/agents/{id}", teacher(tutorHandlers.UpdateAgent))
	apiMux.Handle("GET /api/skills", requireAuth(http.HandlerFunc(tutorHandlers.ListSkills)))
	apiMux.Handle("POST /api/skills", teacher(tutorHandlers.CreateSkill))
	apiMux.Handle("PUT /api/skills/{id}", teacher(tutorHandlers.UpdateSkill))
	apiMux.Handle("DELETE /api/skills/{id}", teacher(tutorHandlers.DeleteSkill))
	// MCP management and audit are teacher-only; class scoping comes from the
	// session inside every handler (design.md Decision 5).
	apiMux.Handle("GET /api/mcp/servers", teacher(mcpHandlers.ListServers))
	apiMux.Handle("POST /api/mcp/servers", teacher(mcpHandlers.CreateServer))
	apiMux.Handle("DELETE /api/mcp/servers/{id}", teacher(mcpHandlers.DeleteServer))
	apiMux.Handle("POST /api/mcp/servers/{id}/connect", teacher(mcpHandlers.Connect))
	apiMux.Handle("GET /api/mcp/servers/{id}/tools", teacher(mcpHandlers.ListTools))
	apiMux.Handle("POST /api/mcp/servers/{id}/disconnect", teacher(mcpHandlers.Disconnect))
	apiMux.Handle("GET /api/mcp/calls", teacher(mcpHandlers.ListCalls))
	apiMux.Handle("GET /api/conversations", requireAuth(http.HandlerFunc(tutorHandlers.ListConversations)))
	apiMux.Handle("POST /api/conversations", requireAuth(http.HandlerFunc(tutorHandlers.CreateConversation)))
	apiMux.Handle("GET /api/conversations/{id}", requireAuth(http.HandlerFunc(tutorHandlers.DetailConversation)))
	apiMux.Handle("POST /api/conversations/{id}/stream", requireAuth(http.HandlerFunc(tutorHandlers.StreamConversation)))
	// API documentation (design.md Decision 9): served before the /api/
	// subtree, without authentication and deliberately outside NoStore. The
	// page, its initializer, the spec and its OpenAPI 3.0 conversion are
	// served by this binary, so the doc can never go stale relative to the
	// code.
	mux.Handle("/swagger/", swaggerui.Handler(docs.SwaggerInfo.ReadDoc))
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(openapiDoc)
	})

	mux.Handle("/api/", httpapi.NoStore(apiMux))

	addr := ":" + cfg.APIPort
	log.Printf("campusclaw backend listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
