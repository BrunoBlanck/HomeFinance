// Package httpserver reúne o servidor HTTP, os middlewares e os utilitários
// de borda (decodificação, resposta, rate limit, CORS, CSRF).
//
// Regra que este pacote sustenta: o cliente só recebe erro genérico; o
// detalhe vai para o log estruturado (docs/SEGURANCA.md §4).
package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// Códigos de erro do contrato (§3 da spec 0001). Nenhum outro código pode
// sair da API.
const (
	CodeValidationFailed = "VALIDATION_FAILED"
	// #nosec G101 -- é o CÓDIGO DE ERRO do contrato, não uma credencial.
	CodeInvalidCredentials   = "INVALID_CREDENTIALS"
	CodeInvalidCode          = "INVALID_CODE"
	CodeEmailNotVerified     = "EMAIL_NOT_VERIFIED"
	CodeUnauthenticated      = "UNAUTHENTICATED"
	CodeInvalidSession       = "INVALID_SESSION"
	CodeForbidden            = "FORBIDDEN"
	CodeNotFound             = "NOT_FOUND"
	CodeMethodNotAllowed     = "METHOD_NOT_ALLOWED"
	CodeRateLimited          = "RATE_LIMITED"
	CodePayloadTooLarge      = "PAYLOAD_TOO_LARGE"
	CodeUnsupportedMediaType = "UNSUPPORTED_MEDIA_TYPE"
	CodeServiceUnavailable   = "SERVICE_UNAVAILABLE"
	CodeInternalError        = "INTERNAL_ERROR"
)

// Mensagens genéricas em pt-BR. São constantes para que dois caminhos de erro
// diferentes produzam respostas byte a byte idênticas (§3.12 da spec 0001).
const (
	MsgValidationFailed = "Dados inválidos."
	// #nosec G101 -- é a MENSAGEM genérica ao cliente, não uma credencial.
	MsgInvalidCredentials   = "E-mail ou senha inválidos."
	MsgInvalidCode          = "Código inválido ou expirado."
	MsgEmailNotVerified     = "Confirme seu e-mail para entrar."
	MsgUnauthenticated      = "Autenticação necessária."
	MsgInvalidSession       = "Sessão inválida."
	MsgForbidden            = "Acesso negado."
	MsgNotFound             = "Recurso não encontrado."
	MsgMethodNotAllowed     = "Método não permitido."
	MsgRateLimited          = "Muitas requisições. Tente novamente mais tarde."
	MsgPayloadTooLarge      = "Corpo da requisição grande demais."
	MsgUnsupportedMediaType = "Tipo de conteúdo não suportado."
	MsgServiceUnavailable   = "Serviço indisponível."
	MsgInternalError        = "Erro interno."
)

const contentTypeJSON = "application/json; charset=utf-8"

// ErrorPayload é o miolo do envelope de erro.
type ErrorPayload struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// ErrorEnvelope é o formato ÚNICO de erro da API (§3 da spec 0001).
type ErrorEnvelope struct {
	Error ErrorPayload `json:"error"`
}

// NewErrorEnvelope monta o envelope sem campos.
func NewErrorEnvelope(code, message string) ErrorEnvelope {
	return ErrorEnvelope{Error: ErrorPayload{Code: code, Message: message}}
}

// MarshalErrorEnvelope serializa o envelope de erro. Usado também pelo
// errorshim, que precisa do corpo pronto antes de escrever o cabeçalho.
func MarshalErrorEnvelope(code, message string) []byte {
	b, err := json.Marshal(NewErrorEnvelope(code, message))
	if err != nil {
		// Impossível para este struct; ainda assim não deixamos corpo vazio.
		return []byte(`{"error":{"code":"INTERNAL_ERROR","message":"Erro interno."}}`)
	}
	return b
}

// WriteJSON escreve uma resposta JSON com status explícito.
//
// Serializa ANTES de escrever o cabeçalho: se a serialização falhar, ainda dá
// para responder 500 em vez de entregar um corpo truncado.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, MsgInternalError)
		return
	}
	writeRaw(w, status, body)
}

// WriteNoContent responde 204.
func WriteNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// WriteError escreve o envelope de erro padrão.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	writeRaw(w, status, MarshalErrorEnvelope(code, message))
}

// WriteValidationError escreve 400 com o mapa de campos. As chaves do mapa
// são serializadas em ordem determinística por encoding/json, o que mantém a
// resposta byte a byte estável.
func WriteValidationError(w http.ResponseWriter, fields map[string]string) {
	env := ErrorEnvelope{Error: ErrorPayload{
		Code:    CodeValidationFailed,
		Message: MsgValidationFailed,
		Fields:  fields,
	}}
	body, err := json.Marshal(env)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, MsgInternalError)
		return
	}
	writeRaw(w, http.StatusBadRequest, body)
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentTypeJSON)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
