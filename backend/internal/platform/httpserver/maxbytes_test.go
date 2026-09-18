package httpserver_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes do teto de corpo POR CAMINHO (spec 0004 §6.4).
//
// O que eles precisam provar não é que /imports aceita 8 MiB — é que TODA outra
// rota continua em 1 MiB. Um teto que vaza por prefixo é pior do que teto
// nenhum, porque ninguém desconfia dele.

const (
	tetoPadrao    int64 = 1 << 10 // 1 KiB: proporção do 1 MiB real, sem o custo
	tetoDaRota    int64 = 8 << 10 // 8 KiB: proporção do 8 MiB real
	caminhoDaRota       = "/api/v1/imports"
)

// lerTudo é o handler de teste: ele lê o corpo inteiro e devolve quantos bytes
// conseguiu, ou 413 quando o MaxBytesReader cortou.
func lerTudo() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		var teto *http.MaxBytesError
		if errors.As(err, &teto) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// O status basta: o que se mede aqui é se o corpo INTEIRO passou.
		_ = b
		w.WriteHeader(http.StatusOK)
	})
}

func enviar(t *testing.T, mw httpserver.Middleware, caminho string, tamanho int64) int {
	t.Helper()
	corpo := bytes.NewReader(bytes.Repeat([]byte("a"), int(tamanho)))
	r := httptest.NewRequest(http.MethodPost, caminho, corpo)
	rec := httptest.NewRecorder()
	mw(lerTudo()).ServeHTTP(rec, r)
	return rec.Code
}

func TestMaxBytesByPathAbreAExcecaoSoNoCaminhoExato(t *testing.T) {
	t.Parallel()

	mw := httpserver.MaxBytesByPath(tetoPadrao, map[string]int64{caminhoDaRota: tetoDaRota})

	assert.Equal(t, http.StatusOK, enviar(t, mw, caminhoDaRota, tetoDaRota),
		"a rota da exceção aceita o teto maior")
	assert.Equal(t, http.StatusRequestEntityTooLarge, enviar(t, mw, caminhoDaRota, tetoDaRota+1),
		"e recusa um byte acima dele")
}

// TestMaxBytesByPathMantemTodaOutraRotaNoTetoPadrao é o teste que a §6.4 exige
// explicitamente.
func TestMaxBytesByPathMantemTodaOutraRotaNoTetoPadrao(t *testing.T) {
	t.Parallel()

	mw := httpserver.MaxBytesByPath(tetoPadrao, map[string]int64{caminhoDaRota: tetoDaRota})

	outrasRotas := []string{
		"/api/v1/auth/login",
		"/api/v1/auth/register",
		"/api/v1/accounts",
		"/api/v1/transactions",

		// As variações perigosas: prefixo faria TODAS herdarem 8 MiB.
		"/api/v1/importsXYZ",
		"/api/v1/imports/",
		"/api/v1/imports/123",
		"/api/v1/imports/123/confirm",
		"/api/v1/IMPORTS",
		"/api/v1//imports",
	}
	for _, caminho := range outrasRotas {
		t.Run(caminho, func(t *testing.T) {
			assert.Equal(t, http.StatusOK, enviar(t, mw, caminho, tetoPadrao))
			assert.Equal(t, http.StatusRequestEntityTooLarge, enviar(t, mw, caminho, tetoPadrao+1),
				"%s precisa continuar no teto padrão", caminho)
		})
	}
}

func TestMaxBytesByPathSemTabelaEquivaleAoTetoGlobal(t *testing.T) {
	t.Parallel()

	mw := httpserver.MaxBytesByPath(tetoPadrao, nil)
	assert.Equal(t, http.StatusRequestEntityTooLarge, enviar(t, mw, caminhoDaRota, tetoPadrao+1))
}

func TestMaxBytesByPathIgnoraExcecaoNaoPositiva(t *testing.T) {
	t.Parallel()

	// Teto zero ou negativo na tabela seria "corpo de zero byte" — e a
	// interpretação ingênua ("zero = sem limite") abriria a porta inteira.
	// Entrada inválida cai no padrão.
	mw := httpserver.MaxBytesByPath(tetoPadrao, map[string]int64{caminhoDaRota: 0})
	assert.Equal(t, http.StatusOK, enviar(t, mw, caminhoDaRota, tetoPadrao))
	assert.Equal(t, http.StatusRequestEntityTooLarge, enviar(t, mw, caminhoDaRota, tetoPadrao+1))
}

// --- chave do limitador por casa ------------------------------------------

func TestHouseholdKeyNaoUsaOIdEmClaro(t *testing.T) {
	t.Parallel()

	const casa = "00000000-0000-7000-9000-000000000001"
	chave := httpserver.HouseholdKey(func(id string) string { return "hmac:" + id[:4] })

	r := httptest.NewRequest(http.MethodPost, "/api/v1/imports", nil)
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID:      "00000000-0000-7000-8000-000000000001",
		HouseholdID: casa,
		Role:        session.RoleOwner,
		SessionID:   "00000000-0000-7000-c000-000000000001",
	}))

	valor := chave(r)
	require.NotEmpty(t, valor)
	assert.NotContains(t, valor, casa, "o id da casa em claro não entra no mapa do limitador")
}

func TestHouseholdKeySemIdentidadeNaoVazaENaoLibera(t *testing.T) {
	t.Parallel()

	chave := httpserver.HouseholdKey(func(id string) string { return "hmac:" + id })
	valor := chave(httptest.NewRequest(http.MethodPost, "/api/v1/imports", nil))

	// Chave constante: requisição sem identidade cai toda no mesmo balde. É
	// restritivo, e é a direção segura — nunca "sem limite".
	assert.Equal(t, "sem-casa", valor)
}

func TestHouseholdKeySemFuncaoDeHashNaoDevolveOIdEmClaro(t *testing.T) {
	t.Parallel()

	const casa = "00000000-0000-7000-9000-000000000002"
	chave := httpserver.HouseholdKey(nil)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/imports", nil)
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID:      "00000000-0000-7000-8000-000000000002",
		HouseholdID: casa,
		Role:        session.RoleOwner,
		SessionID:   "00000000-0000-7000-c000-000000000002",
	}))

	assert.NotContains(t, chave(r), casa)
}
