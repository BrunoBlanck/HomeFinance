package httpserver

import (
	"net/http"
	"strconv"
	"strings"
)

// ErrorShim garante que TODO erro saia no formato único da API — inclusive os
// 404 e 405 que o net/http.ServeMux escreve sozinho, em texto puro
// (critério de aceite 9 da spec 0001).
//
// A troca acontece no WriteHeader: se o status é 404/405 e o Content-Type
// ainda é o text/plain que o http.Error instalou, substituímos corpo e
// cabeçalhos pelo envelope JSON e engolimos a escrita seguinte. Respostas dos
// nossos handlers já saem com Content-Type JSON e passam intactas.
func ErrorShim(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&shimWriter{ResponseWriter: w}, r)
	})
}

type shimWriter struct {
	http.ResponseWriter
	wroteHeader bool
	hijacked    bool
}

// Unwrap permite que http.ResponseController alcance o writer original.
func (s *shimWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *shimWriter) WriteHeader(status int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true

	if body, ok := shimBody(status, s.Header().Get("Content-Type")); ok {
		s.hijacked = true
		h := s.Header()
		h.Set("Content-Type", contentTypeJSON)
		h.Set("Content-Length", strconv.Itoa(len(body)))
		s.ResponseWriter.WriteHeader(status)
		_, _ = s.ResponseWriter.Write(body)
		return
	}

	s.ResponseWriter.WriteHeader(status)
}

func (s *shimWriter) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	if s.hijacked {
		// Corpo em texto puro do ServeMux: descartado, já respondemos JSON.
		return len(b), nil
	}
	return s.ResponseWriter.Write(b)
}

func shimBody(status int, contentType string) ([]byte, bool) {
	if contentType != "" && !strings.HasPrefix(strings.ToLower(contentType), "text/plain") {
		return nil, false
	}
	switch status {
	case http.StatusNotFound:
		return MarshalErrorEnvelope(CodeNotFound, MsgNotFound), true
	case http.StatusMethodNotAllowed:
		return MarshalErrorEnvelope(CodeMethodNotAllowed, MsgMethodNotAllowed), true
	default:
		return nil, false
	}
}
