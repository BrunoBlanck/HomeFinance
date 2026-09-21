package importer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Teste CAIXA-BRANCA das duas metades da canonização da contraparte
// (ADR-035), no ponto exato onde elas decidem:
//
//   - `conferirContrapartes` devolve, para cada id PEDIDO, o id CANÔNICO que o
//     banco reconheceu — e é esse mapa que o resto do confirm usa;
//   - `canonizarContas` troca os pedidos pelos canônicos e RECONFERE que
//     nenhum par ficou com as duas pernas na mesma conta.
//
// Os testes ponta a ponta (contraparte_canonica_test.go) provam a resposta da
// API; estes provam o contrato interno que a sustenta, inclusive os ramos que
// a ponta a ponta alcança só por um dialeto de cada vez.

const (
	contaDoLoteUnit    = "00000000-0000-7000-baba-0000000000a1"
	contraparteUnit    = "00000000-0000-7000-abba-0000000000b2"
	terceiraContaUnit  = "00000000-0000-7000-acca-0000000000c3"
	contaDeOutraCasa   = "00000000-0000-7000-adda-0000000000d4"
	casaDoToken        = "00000000-0000-7000-9000-000000000001"
	casaAlheiaDoToken  = "00000000-0000-7000-9000-000000000002"
	naoExisteEmCasaNen = "00000000-0000-7000-9999-999999999999"
)

// contasFrouxas é um dublê de Accounts que casa o id como MySQL 8
// (`utf8mb4_0900_ai_ci`, ignora caixa) e MSSQL (`CI_AS` com padding ANSI,
// ignora espaço à direita) casariam — e SEMPRE dentro da casa perguntada, que
// é a invariante que a collation não pode afrouxar.
//
// Ele também registra o que foi perguntado: é como o teste confere que a
// conferência custa uma consulta por conta DISTINTA e que o household vem do
// token, nunca do corpo.
type contasFrouxas struct {
	contas    []account.Account
	idsVistos []string
	casasVist []string
}

func (c *contasFrouxas) ByID(_ context.Context, householdID, id string) (*account.Account, error) {
	c.idsVistos = append(c.idsVistos, id)
	c.casasVist = append(c.casasVist, householdID)

	alvo := strings.TrimRight(id, " ")
	for i := range c.contas {
		if c.contas[i].HouseholdID != householdID {
			continue
		}
		if strings.EqualFold(alvo, c.contas[i].ID) {
			copia := c.contas[i]
			return &copia, nil
		}
	}
	return nil, account.ErrNotFound
}

// mundoDeContas monta o dublê com as contas dos testes: duas da casa do token
// (uma delas arquivada por opção) e uma da casa alheia.
func mundoDeContas(arquivada bool) *contasFrouxas {
	quando := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	contraparte := account.Account{ID: contraparteUnit, HouseholdID: casaDoToken}
	if arquivada {
		contraparte.ArchivedAt = &quando
	}
	return &contasFrouxas{contas: []account.Account{
		{ID: contaDoLoteUnit, HouseholdID: casaDoToken},
		contraparte,
		{ID: terceiraContaUnit, HouseholdID: casaDoToken},
		{ID: contaDeOutraCasa, HouseholdID: casaAlheiaDoToken},
	}}
}

// O mapa é o produto da conferência, e não um efeito colateral dela: para
// CADA id pedido, o id que o banco reconheceu.
func TestConferirContrapartesDevolveOMapaDoPedidoParaOCanonico(t *testing.T) {
	t.Parallel()

	mundo := mundoDeContas(false)
	s := &Service{accounts: mundo}

	pedidos := []string{
		strings.ToUpper(contraparteUnit), // MySQL 8: caixa trocada
		terceiraContaUnit + "   ",        // MSSQL: padding ANSI
	}
	canonicas, err := s.conferirContrapartes(t.Context(), casaDoToken, pedidos)
	require.NoError(t, err)

	require.Len(t, canonicas, 2)
	assert.Equal(t, contraparteUnit, canonicas[pedidos[0]],
		"o que vale daqui para a frente é o id do BANCO, não a grafia do cliente")
	assert.Equal(t, terceiraContaUnit, canonicas[pedidos[1]])

	// Uma consulta por id pedido, e sempre na casa do TOKEN.
	assert.Equal(t, pedidos, mundo.idsVistos)
	assert.Equal(t, []string{casaDoToken, casaDoToken}, mundo.casasVist)

	// Lista vazia é caso normal (lote sem nenhum par): mapa vazio, zero
	// consulta, nenhum erro.
	mundoVazio := mundoDeContas(false)
	s2 := &Service{accounts: mundoVazio}
	vazio, err := s2.conferirContrapartes(t.Context(), casaDoToken, nil)
	require.NoError(t, err)
	assert.Empty(t, vazio)
	assert.Empty(t, mundoVazio.idsVistos)
}

// A tradução dos erros da conferência: conta de outra casa é o MESMO erro de
// conta inexistente (404, S1); arquivada aponta o campo da contraparte.
func TestConferirContrapartesTraduzOsErrosSemRevelarORecursoAlheio(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome      string
		arquivada bool
		pedido    string
		esperado  error
	}{
		{"conta inexistente", false, naoExisteEmCasaNen, ErrAccountNotFound},
		{"conta de outra casa", false, contaDeOutraCasa, ErrAccountNotFound},
		{"conta de outra casa em caixa trocada", false, strings.ToUpper(contaDeOutraCasa), ErrAccountNotFound},
		{"contraparte arquivada", true, contraparteUnit, ErrCounterpartArchived},
		{"contraparte arquivada em caixa trocada", true, strings.ToUpper(contraparteUnit), ErrCounterpartArchived},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			t.Parallel()
			s := &Service{accounts: mundoDeContas(caso.arquivada)}
			canonicas, err := s.conferirContrapartes(t.Context(), casaDoToken, []string{caso.pedido})
			require.ErrorIs(t, err, caso.esperado)
			assert.Nil(t, canonicas, "erro não devolve mapa pela metade")
		})
	}
}

// A canonização troca os ids nas PERNAS e nas CONTRAPARTES, e deixa em paz o
// que não está no mapa (a conta do lote, que não veio do corpo).
func TestCanonizarContasTrocaOPedidoPeloCanonico(t *testing.T) {
	t.Parallel()

	pedido := strings.ToUpper(contraparteUnit)
	p := &planoDeEscrita{
		novas: []transaction.NewTransaction{
			{Kind: transaction.KindTransferOut, AccountID: contaDoLoteUnit},
			{Kind: transaction.KindTransferIn, AccountID: pedido},
			{Kind: transaction.KindExpense, AccountID: contaDoLoteUnit},
		},
		contrapartes: []string{pedido},
	}

	require.NoError(t, p.canonizarContas(contaDoLoteUnit, map[string]string{pedido: contraparteUnit}))

	assert.Equal(t, contaDoLoteUnit, p.novas[0].AccountID, "a conta do lote não vem do corpo e não é tocada")
	assert.Equal(t, contraparteUnit, p.novas[1].AccountID, "a perna da contraparte fica com o id do banco")
	assert.Equal(t, contaDoLoteUnit, p.novas[2].AccountID)
	assert.Equal(t, []string{contraparteUnit}, p.contrapartes)
}

// O ramo que o revisor apontou como o mais delicado: a contraparte que
// CANONIZA para a conta do lote é ErrSameAccountTransfer — o MESMO erro que a
// borda daria —, e não um par gravado em silêncio.
func TestCanonizarContasBarraAContraparteQueVirouAContaDoLote(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"caixa trocada (MySQL 8)":     strings.ToUpper(contaDoLoteUnit),
		"espaço à direita (MSSQL)":    contaDoLoteUnit + "   ",
		"caixa trocada só num dígito": strings.Replace(contaDoLoteUnit, "b", "B", 1),
	}
	for nome, pedido := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			p := &planoDeEscrita{
				novas: []transaction.NewTransaction{
					{Kind: transaction.KindTransferOut, AccountID: contaDoLoteUnit},
					{Kind: transaction.KindTransferIn, AccountID: pedido},
				},
				contrapartes: []string{pedido},
			}
			err := p.canonizarContas(contaDoLoteUnit, map[string]string{pedido: contaDoLoteUnit})
			require.ErrorIs(t, err, ErrSameAccountTransfer)
		})
	}
}

// Com mais de um par no mesmo lote, basta UM colapsar na conta do lote para o
// confirm inteiro parar: é tudo ou nada, e não "grava os bons".
func TestCanonizarContasBarraOLoteInteiroQuandoUmDosParesColapsa(t *testing.T) {
	t.Parallel()

	boa := strings.ToUpper(contraparteUnit)
	ruim := strings.ToUpper(contaDoLoteUnit)

	p := &planoDeEscrita{
		novas: []transaction.NewTransaction{
			{Kind: transaction.KindTransferIn, AccountID: boa},
			{Kind: transaction.KindTransferIn, AccountID: ruim},
		},
		contrapartes: []string{boa, ruim},
	}
	err := p.canonizarContas(contaDoLoteUnit, map[string]string{
		boa:  contraparteUnit,
		ruim: contaDoLoteUnit,
	})
	require.ErrorIs(t, err, ErrSameAccountTransfer)
}

// Plano sem par nenhum (o caso comum: extrato só com despesas) atravessa a
// canonização sem erro e sem mudar nada.
func TestCanonizarContasNaoMexeNoPlanoSemTransferencia(t *testing.T) {
	t.Parallel()

	p := &planoDeEscrita{
		novas: []transaction.NewTransaction{
			{Kind: transaction.KindExpense, AccountID: contaDoLoteUnit},
			{Kind: transaction.KindIncome, AccountID: contaDoLoteUnit},
		},
	}
	require.NoError(t, p.canonizarContas(contaDoLoteUnit, map[string]string{}))
	assert.Equal(t, contaDoLoteUnit, p.novas[0].AccountID)
	assert.Equal(t, contaDoLoteUnit, p.novas[1].AccountID)
	assert.Empty(t, p.contrapartes)
}
