package importer

import (
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
)

// Tradução do erro na borda (spec 0005 §12): a contraparte arquivada aponta
// `counterpartAccountId`; a conta do lote arquivada — venha de onde vier o
// erro — continua em `accountId`. Testado por unidade porque a ORDEM dos
// cases em regraDeNegocio é o que garante o campo certo.
func TestRegraDeNegocioDistingueContraparteArquivadaDaContaDoLote(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome  string
		err   error
		campo string
	}{
		{"contraparte arquivada", ErrCounterpartArchived, "counterpartAccountId"},
		{"contraparte arquivada embrulhada", fmt.Errorf("confirmando: %w", ErrCounterpartArchived), "counterpartAccountId"},
		{"conta do lote arquivada (importação)", ErrAccountArchived, PartAccountID},
		{"conta do lote arquivada (lançamentos)", transaction.ErrAccountArchived, PartAccountID},
		{"conta do lote arquivada (faturas)", cardstatement.ErrAccountArchived, PartAccountID},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			t.Parallel()
			campo, mensagem, ok := regraDeNegocio(caso.err)
			assert.True(t, ok)
			assert.Equal(t, caso.campo, campo)
			assert.Contains(t, mensagem, "arquivada")
		})
	}
}

// O plano guarda cada contraparte UMA vez: a conferência custa uma consulta
// por conta distinta, nunca uma por linha do arquivo.
func TestPlanoLembraCadaContraparteUmaVezNaOrdemEmQueApareceu(t *testing.T) {
	t.Parallel()

	var p planoDeEscrita
	for _, id := range []string{"conta-b", "conta-c", "conta-b", "conta-b", "conta-c", "conta-d"} {
		p.lembrarContraparte(id)
	}
	assert.Equal(t, []string{"conta-b", "conta-c", "conta-d"}, p.contrapartes)
}
