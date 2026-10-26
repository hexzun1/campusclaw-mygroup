package main

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const bearerPrefix = "Bearer "

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) < len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(header[len(bearerPrefix):])
}

func (s *server) authorized(r *http.Request) bool {
	return subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) == 1
}
