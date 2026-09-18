package transaction

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A parada voluntária da fase de cálculo, testada nas DUAS perguntas que ela
// faz (achado A12 da revisão de segurança).
//
// Isto é um teste INTERNO porque `conferirPrazo` é interna de propósito: quem
// tem o direito de dizer "o prazo venceu" é quem impôs o prazo, e esse direito
// não é exportado. Testá-la por fora só alcançaria a primeira pergunta.

// contextoDeRelogio é um contexto que responde às duas perguntas de forma
// INDEPENDENTE: `Err()` diz o que o timer já percebeu, `Deadline()` diz o que o
// relógio sabe.
//
// Ele existe porque a janela que a segunda pergunta fecha é IMPOSSÍVEL de
// encenar com o `context` da biblioteca padrão: `context.WithDeadline` com uma
// data no passado cancela na hora, e um prazo de 1 ns lido dentro do mesmo tick
// do relógio do Windows (resolução de milissegundos) simplesmente ainda não
// venceu. Com o contexto da stdlib, portanto, ou se testa a primeira pergunta,
// ou se torce pelo escalonador — e um teste que passa por sorte não é teste.
type contextoDeRelogio struct {
	context.Context
	prazo time.Time
	erro  error
}

func (c contextoDeRelogio) Deadline() (time.Time, bool) { return c.prazo, !c.prazo.IsZero() }
func (c contextoDeRelogio) Err() error                  { return c.erro }

// A SEGUNDA pergunta: o relógio já passou do prazo, mas o timer do contexto
// ainda não percebeu (`Err()` nil). Sem ela, uma fatia inteira do cálculo
// começaria com o orçamento já no vermelho — e, pior, se o estouro só fosse
// percebido DENTRO de uma consulta, o erro viria do driver e a borda
// responderia 500 em vez de 422.
func TestConferirPrazoPerguntaAoRelogioQuandoOTimerAindaNaoPercebeu(t *testing.T) {
	t.Parallel()

	ctx := contextoDeRelogio{
		Context: context.Background(),
		prazo:   time.Now().Add(-time.Millisecond),
		erro:    nil,
	}

	err := conferirPrazo(ctx, "pontuando lançamentos do mês")
	require.Error(t, err, "prazo vencido no RELÓGIO é prazo vencido, mesmo com Err() ainda nil")
	assert.ErrorIs(t, err, ErrPlanTimeout,
		"a sentinela do domínio é a única autorização para dizer que o mês demorou demais")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "o motivo original fica na cadeia para o log")
	assert.Contains(t, err.Error(), "pontuando lançamentos do mês", "a etapa entra no log")
}

// Prazo ainda de pé: nada acontece. Sem este caso, uma parada que devolvesse
// erro sempre passaria nos outros testes e mataria toda execução legítima.
func TestConferirPrazoDeixaPassarComOrcamentoDePe(t *testing.T) {
	t.Parallel()

	ctx := contextoDeRelogio{
		Context: context.Background(),
		prazo:   time.Now().Add(time.Hour),
	}
	assert.NoError(t, conferirPrazo(ctx, "pontuando lançamentos do mês"))

	// Sem prazo nenhum no contexto também passa: é o caso do pareamento puro
	// chamado direto pelos testes de tabela.
	assert.NoError(t, conferirPrazo(context.Background(), "pareando candidatas"))
}

// O CANCELAMENTO não é o prazo, e a diferença é a razão de erroDeParada
// existir: 422 é uma afirmação sobre o MÊS ("é grande demais para uma
// execução"), e ela seria FALSA sobre uma execução que a pessoa interrompeu.
//
// MUTAÇÃO: fazer erroDeParada devolver ErrPlanTimeout para qualquer motivo faz
// este teste falhar — e é o que transformaria "aba fechada" em "o seu mês é
// grande demais".
func TestConferirPrazoNaoChamaCancelamentoDePrazo(t *testing.T) {
	t.Parallel()

	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()

	err := conferirPrazo(ctx, "pontuando lançamentos do mês")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrPlanTimeout, "cliente que desiste não é teto do mês")
	assert.ErrorIs(t, err, context.Canceled, "o motivo original fica na cadeia: a borda o registra em INFO")
}

// O prazo VENCIDO percebido pelo timer (o caminho comum) continua sendo prazo,
// e a primeira pergunta é quem o pega.
func TestConferirPrazoReconhecePrazoJaPercebidoPeloTimer(t *testing.T) {
	t.Parallel()

	ctx, cancelar := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelar()

	err := conferirPrazo(ctx, "abrindo o cálculo")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlanTimeout)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// A mensagem da parada não carrega dado da casa: ela atravessa o log da borda,
// e mês, id, descrição, valor e palavra-chave não entram lá (S8).
func TestParadaVoluntariaNaoCarregaDadoDaCasa(t *testing.T) {
	t.Parallel()

	for _, motivo := range []error{context.DeadlineExceeded, context.Canceled} {
		err := erroDeParada(motivo, "pontuando lançamentos do mês")
		require.Error(t, err)
		texto := err.Error()
		for _, proibido := range []string{"2026-09", "SUPERMERCADO", "cat-", "acc-", "150"} {
			assert.NotContains(t, texto, proibido, "a parada voluntária não descreve o dado da casa")
		}
		assert.True(t, errors.Is(err, motivo), "o motivo original sempre fica na cadeia")
	}
}
