package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// purgadorFake conta as passadas e devolve o que o teste mandar.
//
// O relógio do expurgo de importação é INJETADO no serviço (importer.WithClock),
// não aqui: o janitor só decide QUANDO chamar. Este dublê é o que permite
// afirmar que ele chama — e que uma falha não derruba a passada inteira.
type purgadorFake struct {
	mu       sync.Mutex
	chamadas int
	erro     error
	retorno  [3]int64
}

func (p *purgadorFake) PurgeExpired(context.Context) (int64, int64, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chamadas++
	return p.retorno[0], p.retorno[1], p.retorno[2], p.erro
}

func (p *purgadorFake) vezes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.chamadas
}

// TestJanitorVarreAImportacaoNaPrimeiraPassada prova a ligação: o expurgo do
// staging roda junto com o de credenciais, e roda já no boot — reinícios
// frequentes não podem adiar indefinidamente a limpeza de dado financeiro
// parado numa tabela de rascunho.
func TestJanitorVarreAImportacaoNaPrimeiraPassada(t *testing.T) {
	t.Parallel()

	purgador := &purgadorFake{retorno: [3]int64{1, 13, 0}}
	// auth e audit nulos: a passada precisa continuar chamando o expurgo de
	// importação mesmo quando os outros não estão ligados.
	j := newJanitor(nil, nil, purgador, logging.Discard(), janitorOptions{
		Interval: time.Hour,
		Timeout:  5 * time.Second,
	})

	j.Start(t.Context())
	t.Cleanup(j.Stop)

	require.Eventually(t, func() bool { return purgador.vezes() >= 1 }, 2*time.Second, 10*time.Millisecond,
		"a primeira passada acontece no boot")
}

// TestJanitorSobreviveAFalhaDoExpurgoDeImportacao: cada expurgo é independente,
// e deixar de limpar credencial porque o staging falhou seria trocar um problema
// por dois.
func TestJanitorSobreviveAFalhaDoExpurgoDeImportacao(t *testing.T) {
	t.Parallel()

	purgador := &purgadorFake{erro: errors.New("banco indisponível")}
	j := newJanitor(nil, nil, purgador, logging.Discard(), janitorOptions{
		Interval: 20 * time.Millisecond,
		Timeout:  time.Second,
	})

	j.Start(t.Context())
	t.Cleanup(j.Stop)

	require.Eventually(t, func() bool { return purgador.vezes() >= 2 }, 2*time.Second, 10*time.Millisecond,
		"a falha de uma passada não interrompe o laço")
}

// TestJanitorSemPurgadorNaoQuebra: o campo é uma interface, e interface nula no
// caminho de varredura é o tipo de coisa que vira panic em produção às 3 da
// manhã.
func TestJanitorSemPurgadorNaoQuebra(t *testing.T) {
	t.Parallel()

	j := newJanitor(nil, nil, nil, logging.Discard(), janitorOptions{
		Interval: time.Hour,
		Timeout:  time.Second,
	})
	assert.NotPanics(t, func() {
		j.Start(t.Context())
		j.Stop()
	})
}
