package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Erros de decodificação, traduzidos pelo handler para status HTTP.
var (
	// ErrUnsupportedMediaType — Content-Type não é application/json (415).
	ErrUnsupportedMediaType = errors.New("content-type não suportado")
	// ErrPayloadTooLarge — corpo acima de MaxBytes (413).
	ErrPayloadTooLarge = errors.New("corpo grande demais")
	// ErrMalformedBody — JSON inválido, campo desconhecido, tipo errado (400).
	ErrMalformedBody = errors.New("corpo malformado")
)

// DecodeJSON lê o corpo da requisição em T aplicando as defesas da
// docs/SEGURANCA.md §3: Content-Type exigido, campos desconhecidos
// rejeitados, um único objeto JSON por corpo.
//
// O limite de tamanho vem do middleware MaxBytes, aplicado antes na cadeia —
// aqui só traduzimos o erro do http.MaxBytesReader.
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var dst T

	if err := requireJSONContentType(r); err != nil {
		return dst, err
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return dst, ErrPayloadTooLarge
		}
		if errors.Is(err, io.EOF) {
			return dst, fmt.Errorf("%w: corpo vazio", ErrMalformedBody)
		}
		// A mensagem detalhada fica só no log do handler; o cliente recebe
		// a mensagem genérica de validação.
		return dst, fmt.Errorf("%w: %w", ErrMalformedBody, err)
	}

	// Um corpo com mais de um valor JSON ("{}{}") é entrada hostil.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return dst, ErrPayloadTooLarge
		}
		return dst, fmt.Errorf("%w: corpo com mais de um valor JSON", ErrMalformedBody)
	}

	return dst, nil
}

func requireJSONContentType(r *http.Request) error {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return ErrUnsupportedMediaType
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ErrUnsupportedMediaType
	}
	if !strings.EqualFold(mediaType, "application/json") {
		return ErrUnsupportedMediaType
	}
	return nil
}

// WriteDecodeError traduz o erro de DecodeJSON para a resposta HTTP do
// contrato. Nunca ecoa o erro original (evita vazar estrutura interna).
func WriteDecodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnsupportedMediaType):
		WriteError(w, http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, MsgUnsupportedMediaType)
	case errors.Is(err, ErrPayloadTooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, MsgPayloadTooLarge)
	default:
		WriteError(w, http.StatusBadRequest, CodeValidationFailed, MsgValidationFailed)
	}
}
