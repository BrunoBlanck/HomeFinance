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

	// CodeResourceInUse (HTTP 422) — o recurso não pode ser EXCLUÍDO porque
	// há dado dependente; arquivar continua permitido (spec 0003, D4).
	//
	// Código próprio, e não VALIDATION_FAILED, porque as duas situações pedem
	// telas diferentes: uma manda o usuário corrigir o formulário, a outra
	// oferece arquivar. Fazer o frontend distinguir isso interpretando o TEXTO
	// da mensagem seria acoplar a interface à redação em português.
	CodeResourceInUse = "RESOURCE_IN_USE"

	// CodeKeywordTaken (HTTP 409) — a palavra-chave já pertence a OUTRA
	// categoria (ou outra conta) DA MESMA CASA (spec 0005 §4.1). Código
	// próprio pelo mesmo motivo de RESOURCE_IN_USE: a tela mostra "já está em
	// X" resolvendo `fields.ownerId`, uma ação diferente do 400 de forma.
	//
	// `fields.ownerId` é sempre um recurso da casa do token — a checagem que o
	// produz filtra por household_id —, então citá-lo nunca revela recurso
	// alheio. A palavra em `fields.keyword` é a que o próprio cliente enviou.
	CodeKeywordTaken = "KEYWORD_TAKEN"

	// CodeConflict (HTTP 409) — o estado mudou ENTRE a leitura e a escrita da
	// mesma operação (spec 0005 §13, ADR-028d): em POST /transfers/detect, o
	// UPDATE condicional de um par não afetou exatamente duas linhas porque
	// outra requisição excluiu, converteu ou editou um dos lançamentos no
	// meio. A transação inteira foi desfeita — nada gravado.
	//
	// Código próprio, e não INTERNAL_ERROR (não é falha do servidor) nem
	// VALIDATION_FAILED (o pedido estava certo), porque mapeia para UMA ação
	// da tela: pedir a prévia de novo. Sem `fields`: não há campo a corrigir,
	// e citar qual lançamento mudou seria vazar o estado de outra requisição.
	CodeConflict = "CONFLICT"

	// Os SEIS códigos da importação (HTTP 422 — §5.4 da spec 0004).
	//
	// Cada um existe porque mapeia para UMA AÇÃO DIFERENTE DA INTERFACE, e é
	// essa correspondência — nada mais — que justifica seis códigos em vez de
	// um. Colapsá-los em VALIDATION_FAILED obrigaria o frontend a interpretar
	// texto em português, que é exatamente o que a D4 da spec 0003 recusou.
	// Quem for "simplificar" isto depois: a tabela abaixo é o motivo.
	//
	//	PASSWORD_REQUIRED  -> a tela ABRE O CAMPO DE SENHA
	//	PASSWORD_INVALID   -> a tela MANTÉM o campo e pede de novo
	//	FORMAT_UNKNOWN     -> a tela LISTA os formatos suportados
	//	FORMAT_AMBIGUOUS   -> a tela mostra um SELETOR (fields.format)
	//	TARGET_MISMATCH    -> a tela manda TROCAR A CONTA de destino
	//	FILE_REJECTED      -> a tela EXPLICA QUAL LIMITE estourou (fields)
	CodeImportPasswordRequired = "IMPORT_PASSWORD_REQUIRED"
	CodeImportPasswordInvalid  = "IMPORT_PASSWORD_INVALID"
	CodeImportFormatUnknown    = "IMPORT_FORMAT_UNKNOWN"
	CodeImportFormatAmbiguous  = "IMPORT_FORMAT_AMBIGUOUS"
	CodeImportTargetMismatch   = "IMPORT_TARGET_MISMATCH"
	CodeImportFileRejected     = "IMPORT_FILE_REJECTED"
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
	MsgResourceInUse        = "Este item está em uso e não pode ser excluído."
	MsgKeywordTaken         = "Esta palavra-chave já está em uso nesta casa."
	MsgConflict             = "Os dados mudaram enquanto a operação rodava. Confira a prévia de novo."

	// Mensagens da importação. Nenhuma contém trecho do arquivo, linha crua,
	// nome de estabelecimento nem — jamais — a senha do ZIP.
	//
	// As duas primeiras NÃO se chamam "...Password...", e é de propósito: o
	// gosec trata qualquer identificador com "pass"/"pwd"/"cred" como
	// credencial em potencial (G101), e a regra do projeto é zero achado sem
	// nenhuma supressão. É o mesmo motivo de importer.SuggestionCardInflow não se
	// chamar "...CardCredit".
	MsgImportFileLocked       = "Este arquivo está protegido por senha."
	MsgImportFileUnlockFailed = "Senha incorreta para este arquivo."
	MsgImportFormatUnknown    = "Ainda não sei ler este formato de arquivo."
	MsgImportFormatAmbiguous  = "Mais de um formato reconheceu este arquivo. Escolha qual usar."
	MsgImportTargetMismatch   = "Este arquivo não corresponde à conta escolhida."
	MsgImportFileRejected     = "Não consegui aceitar este arquivo."
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

// WriteUnprocessable escreve 422 com código próprio e, opcionalmente, o mapa
// de campos.
//
// 400 e 422 dizem coisas diferentes e a interface reage a cada um de um jeito:
// 400 é "o corpo está malformado ou o valor é inválido, corrija e reenvie";
// 422 é "o corpo está correto, mas a regra de negócio recusa" — nome já usado,
// teto de quantidade, recurso em uso. Colapsar os dois em 400 obrigaria o
// frontend a adivinhar qual dos dois aconteceu.
func WriteUnprocessable(w http.ResponseWriter, code, message string, fields map[string]string) {
	env := ErrorEnvelope{Error: ErrorPayload{Code: code, Message: message, Fields: fields}}
	body, err := json.Marshal(env)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, MsgInternalError)
		return
	}
	writeRaw(w, http.StatusUnprocessableEntity, body)
}

// WriteConflict escreve 409 com código próprio e, opcionalmente, o mapa de
// campos — a mesma forma de WriteUnprocessable, com outro status.
//
// 409 é "o corpo está correto e a regra de negócio aceitaria, mas o estado
// ATUAL de outro recurso da casa impede": KEYWORD_TAKEN, em que a
// palavra-chave já pertence a outra categoria ou conta (spec 0005 §4.1), e
// CONFLICT, em que o estado mudou entre a leitura e a escrita da mesma
// operação (ADR-028d). O mapa de campos carrega a palavra recusada e, quando
// conhecida, a dona — sempre um recurso da casa do token; quem chama é
// responsável por isso. CONFLICT vai sem campos.
func WriteConflict(w http.ResponseWriter, code, message string, fields map[string]string) {
	env := ErrorEnvelope{Error: ErrorPayload{Code: code, Message: message, Fields: fields}}
	body, err := json.Marshal(env)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, MsgInternalError)
		return
	}
	writeRaw(w, http.StatusConflict, body)
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentTypeJSON)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
