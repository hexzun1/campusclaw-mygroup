package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	defaultAddr = ":8090"
	serverName  = "campus-tools"
)

func main() {
	token := os.Getenv("MCP_AUTH_TOKEN")
	if token == "" {
		log.Println("MCP_AUTH_TOKEN 未配置，mcp-demo 拒绝启动")
		os.Exit(1)
	}

	addr := os.Getenv("MCP_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(&server{token: token}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("mcp-demo 监听 %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("mcp-demo 启动失败：%v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("mcp-demo 关闭失败：%v", err)
	}
}

func newMux(s *server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /mcp", s.handleMCP)
	return mux
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
