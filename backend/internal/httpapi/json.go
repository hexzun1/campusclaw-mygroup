// Package httpapi holds small HTTP helpers shared across handler packages:
// consistent JSON responses and the request-context key carrying the
// authenticated session (role and class come from here, and nowhere else).
package httpapi

import (
	"encoding/json"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type errorBody struct {
	Error string `json:"error"`
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, errorBody{Error: message})
}

const (
	MsgUnauthorized = "未登录或会话已失效"
	MsgForbidden    = "没有权限执行该操作"
	MsgNotFound     = "记录不存在"
	MsgServiceDown  = "服务暂不可用，请稍后重试"
	MsgBadCredentials = "用户名或密码错误"
)
