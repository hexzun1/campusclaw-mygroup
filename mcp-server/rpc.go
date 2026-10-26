package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602

	maxRequestBytes = 64 << 10
	serverVersion   = "1.0.0"
)

var (
	supportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}
	latestProtocolVersion     = supportedProtocolVersions[0]
)

type server struct {
	token string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func invalidParams(message string) *rpcError {
	return &rpcError{Code: codeInvalidParams, Message: message}
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *server) handleMCP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="campus-tools"`)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, `{"error":"request body too large"}`, http.StatusRequestEntityTooLarge)
			return
		}
		writeRPCError(w, nil, codeParseError, "请求体读取失败")
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		log.Printf("method=parse-error duration_ms=%d", time.Since(started).Milliseconds())
		writeRPCError(w, nil, codeParseError, "请求体不是合法的 JSON")
		return
	}
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
		log.Printf("method=invalid-request duration_ms=%d", time.Since(started).Milliseconds())
		writeRPCError(w, req.ID, codeInvalidRequest, "不是合法的 JSON-RPC 2.0 请求：缺少 jsonrpc 或 method")
		return
	}

	// 通知（notifications/initialized 以及任何没有 id 的请求）不应答正文。
	if req.Method == "notifications/initialized" || len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		log.Printf("method=%s duration_ms=%d status=accepted", req.Method, time.Since(started).Milliseconds())
		return
	}

	switch req.Method {
	case "initialize":
		writeRPCResult(w, req.ID, initializeResult(req.Params))
		log.Printf("method=initialize duration_ms=%d status=ok", time.Since(started).Milliseconds())
	case "tools/list":
		writeRPCResult(w, req.ID, map[string]any{"tools": toolList()})
		log.Printf("method=tools/list duration_ms=%d status=ok", time.Since(started).Milliseconds())
	case "tools/call":
		result, rpcErr := s.callTool(req.Params)
		if rpcErr != nil {
			writeRPCError(w, req.ID, rpcErr.Code, rpcErr.Message)
			log.Printf("method=tools/call duration_ms=%d status=invalid-params", time.Since(started).Milliseconds())
			return
		}
		writeRPCResult(w, req.ID, result)
		log.Printf("method=tools/call duration_ms=%d status=%s", time.Since(started).Milliseconds(), result.status())
	default:
		writeRPCError(w, req.ID, codeMethodNotFound, "未知方法："+req.Method)
		log.Printf("method=%s duration_ms=%d status=method-not-found", req.Method, time.Since(started).Milliseconds())
	}
}

func initializeResult(params json.RawMessage) map[string]any {
	version := latestProtocolVersion
	if len(params) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			for _, v := range supportedProtocolVersions {
				if v == p.ProtocolVersion {
					version = v
					break
				}
			}
		}
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *server) callTool(raw json.RawMessage) (toolResult, *rpcError) {
	var params callParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return toolResult{}, invalidParams("tools/call 参数必须是 JSON 对象")
	}
	if strings.TrimSpace(params.Name) == "" {
		return toolResult{}, invalidParams("tools/call 缺少工具名 name")
	}
	t := lookupTool(params.Name)
	if t == nil {
		return toolResult{}, invalidParams("未知工具：" + params.Name)
	}
	args := params.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	return t.Call(args)
}
