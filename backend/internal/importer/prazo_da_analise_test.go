package importer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O prazo da fase 1 x a falha do servidor — e por que os dois PRECISAM de
// respostas diferentes.
//
// # O defeito que estes testes existem para não deixar voltar
//
// O gormstore passou a somar o motivo do contexto ao erro do driver
// (internal/platform/storage/ctxerr.go), porque o driver puro-Go do SQLite
// devolve "interrupted (9)" — que não embrulha context.DeadlineExceeded — e sem
// o embrulho um prazo estourado virava 500. O efeito colateral é que, com o
// contexto MORTO, QUALQUER erro de banco passou a casar
// `errors.Is(err, context.DeadlineExceeded)`.
//
// A borda da importação decidia o 422 IMPORT_FILE_REJECTED ("a análise deste
// arquivo demorou demais") justamente por essa pergunta. Duas consequências,
// as duas ruins:
//
//  1. uma falha do SERVIDOR (banco corrompido, permissão, defeito nosso) era
//     reportada como culpa do ARQUIVO de quem importou;
//  2. o 422 sai ANTES do ramo de 500, então a linha de ERROR — o único
//     registro daquela falha, e o que o próprio ctxerr.go chama de sinal de
//     segurança — simplesmente sumia do log.
//
// # O par de testes
//
// Os dois montam exatamente o MESMO cenário — a consulta da conta de destino
// fica em voo até o prazo da análise vencer — e mudam UM eixo só: o que o banco
// responde quando volta. Nada falhou → 422. Algo falhou → 500 com ERROR.
//
// Sem dublê de banco de propósito para a falha ser INJETADA e não sorteada:
// tentar produzi-la correndo uma consulta lenta contra o prazo esbarra na
// armadilha medida em ctxerr.go — com a tabela vazia o SQLite nem avalia a
// condição, o comando volta em 1 ms sem erro nenhum, e o teste passa sem ter
// exercitado coisa alguma. O caminho ponta a ponta, com SQLite e arquivo de
// verdade, está no terceiro teste.

// contasQueEsperamOPrazo é o seam: `ByID` fica em voo até o contexto da análise
// morrer e só então responde o que o teste mandar.
type contasQueEsperamOPrazo struct {
	resposta func(ctx context.Context) (*account.Account, error)
}

func (c contasQueEsperamOPrazo) ByID(ctx context.Context, _, _ string) (*account.Account, error) {
	<-ctx.Done() // o prazo da fase 1 vence COM a consulta em voo
	return c.resposta(ctx)
}

// servicoComContas monta um Service em que só a conta de destino é alcançada.
//
// As outras dependências vão como nil de propósito: a análise morre na conta ou
// na parada voluntária logo depois dela, e se um dia o caminho mudar o teste
// explode em vez de passar contando outra história.
func servicoComContas(contas importer.Accounts, prazo time.Duration) *importer.Service {
	return importer.NewService(nil, nil, contas, nil, nil, nil, nil, nil,
		importer.WithAnalyzeTimeout(prazo))
}

// enviarComLogCapturado roda POST /imports pelo handler, com o logger sob
// captura, e devolve a resposta e as linhas estruturadas do log.
func enviarComLogCapturado(t *testing.T, ctx context.Context, svc *importer.Service) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()

	var log bytes.Buffer
	h := importer.NewHandler(svc, slog.New(slog.NewJSONHandler(&log, nil)), 0)

	corpo, tipo := montarMultipart(t,
		parte{nome: importer.PartFile, arquivo: "NU_2026-08.csv", conteudo: fixtureExtrato(t)},
		parte{nome: importer.PartAccountID, conteudo: []byte("00000000-0000-7000-c000-0000000000aa")},
	)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/imports", corpo).WithContext(ctx)
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	h.Create(rec, r)
	return rec, linhasDoLog(t, log.String())
}

// linhasDoLog decodifica o log estruturado. Comparar nível por JSON, e não por
// substring, é o que impede um "ERROR" escrito dentro de uma mensagem de passar
// por uma linha de ERROR.
func linhasDoLog(t *testing.T, bruto string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(bruto), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &m), "linha de log inválida: %s", l)
		out = append(out, m)
	}
	return out
}

// temNivel informa se alguma linha do log saiu no nível pedido.
func temNivel(linhas []map[string]any, nivel string) bool {
	for _, l := range linhas {
		if l["level"] == nivel {
			return true
		}
	}
	return false
}

// TestFalhaDeBancoComPrazoVencidoNaoViraLimiteDoArquivo é a metade do par que
// guarda o defeito: o banco FALHOU, e a falha é do servidor mesmo que o prazo
// já tivesse vencido quando ela apareceu.
//
// Mutação: trocar `errors.Is(err, ErrAnalyzeTimeout)` de volta por
// `errors.Is(err, context.DeadlineExceeded)` em limiteDoArquivo faz este teste
// virar 422 IMPORT_FILE_REJECTED, sem linha de ERROR.
func TestFalhaDeBancoComPrazoVencidoNaoViraLimiteDoArquivo(t *testing.T) {
	t.Parallel()

	// Erro de DRIVER genérico, sem relação nenhuma com prazo, embrulhado
	// exatamente como o gormstore o entrega hoje. A função real, e não uma
	// imitação: se o embrulho de ctxerr.go mudar de forma, é aqui que se vê.
	falhaDoDriver := errors.New("database disk image is malformed")
	svc := servicoComContas(contasQueEsperamOPrazo{
		resposta: func(ctx context.Context) (*account.Account, error) {
			return nil, storage.ComErroDeContexto(ctx, falhaDoDriver)
		},
	}, 30*time.Millisecond)

	rec, log := enviarComLogCapturado(t, contextoDeRequisicaoViva(t), svc)

	// A premissa do cenário: o erro entregue à borda REALMENTE casa o motivo do
	// contexto. Sem isto o teste passaria por não exercitar nada.
	require.True(t, errors.Is(storage.ComErroDeContexto(contextoJaVencido(), falhaDoDriver), context.DeadlineExceeded),
		"o embrulho do gormstore precisa fazer a falha de driver casar DeadlineExceeded — é essa a armadilha")

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "INTERNAL_ERROR")
	assert.NotContains(t, rec.Body.String(), "IMPORT_FILE_REJECTED",
		"falha do servidor não pode ser reportada como limite do arquivo")
	assert.NotContains(t, rec.Body.String(), "demorou demais")

	assert.True(t, temNivel(log, "ERROR"),
		"a falha do servidor precisa deixar uma linha de ERROR no log — ela é o único registro dela")

	// E o detalhe técnico fica no log, nunca na resposta.
	assert.NotContains(t, rec.Body.String(), "malformed")
}

// TestPrazoDaAnaliseVencidoSemFalhaContinuaSendo422 é a outra metade: nada
// falhou, o orçamento é que acabou. Aí sim o arquivo é o assunto.
func TestPrazoDaAnaliseVencidoSemFalhaContinuaSendo422(t *testing.T) {
	t.Parallel()

	svc := servicoComContas(contasQueEsperamOPrazo{
		resposta: func(context.Context) (*account.Account, error) {
			// A consulta terminou BEM — só demorou. A parada voluntária da
			// etapa seguinte é quem interrompe a análise.
			return &account.Account{ID: "00000000-0000-7000-c000-0000000000aa"}, nil
		},
	}, 30*time.Millisecond)

	rec, log := enviarComLogCapturado(t, contextoDeRequisicaoViva(t), svc)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "IMPORT_FILE_REJECTED")
	assert.Contains(t, rec.Body.String(), "demorou demais")
	assert.False(t, temNivel(log, "ERROR"),
		"limite de trabalho previsto não é incidente e não polui o log de ERROR")
}

// TestClienteQueDesisteNoMeioNaoViraErroNemLimite cobre a terceira causa que o
// contexto morto pode ter: a aba fechada.
//
// Não é 422 (não há a quem orientar) e não é incidente. O que decide isto é o
// contexto da REQUISIÇÃO — nunca o erro, que sob o embrulho do gormstore casa
// context.Canceled para qualquer falha de banco.
func TestClienteQueDesisteNoMeioNaoViraErroNemLimite(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // o cliente já foi embora quando a análise começa
	svc := servicoComContas(contasQueEsperamOPrazo{
		resposta: func(context.Context) (*account.Account, error) {
			return &account.Account{ID: "00000000-0000-7000-c000-0000000000aa"}, nil
		},
	}, importer.AnalyzeTimeout)

	rec, log := enviarComLogCapturado(t, comIdentidadeDoToken(ctx), svc)

	assert.NotContains(t, rec.Body.String(), "IMPORT_FILE_REJECTED",
		"cliente que desistiu não é limite do arquivo")
	assert.False(t, temNivel(log, "ERROR"),
		"aba fechada é comportamento normal de cliente, não sinal de segurança")
	assert.True(t, temNivel(log, "INFO"),
		"a desistência continua registrada — em INFO, mas registrada")
}

// TestPrazoDaAnalisePontaAPontaContinua422 é o mesmo comportamento pelo caminho
// de produção inteiro: multipart, SQLite de verdade, arquivo de verdade,
// Service de verdade, handler de verdade — sem dublê nenhum.
//
// O orçamento é esgotado pelo CONTEXTO, e não por um arquivo grande, de
// propósito: gate de tempo neste repositório já ensinou a lição (ver o cabeçalho
// de qa_e2c_desempenho_test.go) e, pior, aqui ele seria ambíguo — um prazo que
// vença DENTRO da consulta da conta é 500, e qual dos dois acontece primeiro
// dependeria da carga da máquina. Um contexto cujo prazo já passou cancela de
// forma síncrona, na construção, e o teste mede o que se propõe a medir.
//
// A segunda metade é o antídoto contra o teste que passa por acidente: a MESMA
// requisição, com o prazo normal, importa e devolve 201.
func TestPrazoDaAnalisePontaAPontaContinua422(t *testing.T) {
	t.Parallel()

	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	enviar := func(ctx context.Context) (*httptest.ResponseRecorder, []map[string]any) {
		var log bytes.Buffer
		h := importer.NewHandler(a.svc, slog.New(slog.NewJSONHandler(&log, nil)), 0)

		corpo, tipo := montarMultipart(t,
			parte{nome: importer.PartFile, arquivo: "NU_2026-08.csv", conteudo: fixtureExtrato(t)},
			parte{nome: importer.PartAccountID, conteudo: []byte(conta.ID)},
		)
		r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
		r.Header.Set("Content-Type", tipo)
		r = r.WithContext(session.NewContext(ctx, identidade(a.casa.ID, a.usuario.ID)))

		rec := httptest.NewRecorder()
		h.Create(rec, r)
		return rec, linhasDoLog(t, log.String())
	}

	semOrcamento, cancelar := context.WithDeadline(t.Context(), time.Now().Add(-time.Hour))
	defer cancelar()

	rec, log := enviar(semOrcamento)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "IMPORT_FILE_REJECTED")
	assert.Contains(t, rec.Body.String(), "demorou demais")
	assert.False(t, temNivel(log, "ERROR"))

	// E nada foi gravado: o lote não existe pela metade.
	lotes, err := a.repoImport.ListBatches(t.Context(), a.casa.ID, 50)
	require.NoError(t, err)
	assert.Empty(t, lotes, "análise interrompida pelo prazo não deixa lote em staging")

	// O mesmo envio, com o prazo de produção, funciona.
	rec, log = enviar(t.Context())
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.False(t, temNivel(log, "ERROR"))
}

// --- contextos dos testes --------------------------------------------------

// contextoDeRequisicaoViva é o contexto de uma requisição NORMAL: a identidade
// do token publicada pelo RequireAuth e nenhum prazo próprio. Quem impõe prazo
// é o Analyze.
func contextoDeRequisicaoViva(t *testing.T) context.Context {
	t.Helper()
	return comIdentidadeDoToken(t.Context())
}

// comIdentidadeDoToken publica no contexto a mesma identidade que o RequireAuth
// publicaria. A casa sai DAQUI — nunca do corpo do multipart.
func comIdentidadeDoToken(ctx context.Context) context.Context {
	return session.NewContext(ctx, identidade(
		"00000000-0000-7000-c000-0000000000c1",
		"00000000-0000-7000-c000-0000000000u1",
	))
}

// contextoJaVencido serve só à asserção de premissa: um contexto cujo prazo já
// passou, para provar que o embrulho do gormstore casa DeadlineExceeded.
func contextoJaVencido() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel()
	return ctx
}
