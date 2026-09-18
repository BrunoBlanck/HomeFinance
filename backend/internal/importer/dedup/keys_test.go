package dedup_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// sha é o cálculo de referência escrito à mão, para conferir o FORMATO da
// string que entra no hash — que é o que a §4.4 da spec 0004 define, literal.
//
// Escrito de novo aqui de propósito: um teste que chamasse a mesma função de
// produção provaria apenas que ela é igual a si mesma. Este aqui quebra se
// alguém mexer na ordem dos campos, no separador ou no prefixo de versão.
func sha(s string) string {
	soma := sha256.Sum256([]byte(s))
	return hex.EncodeToString(soma[:])
}

func TestFormatoDasChaves(t *testing.T) {
	assert.Equal(t,
		sha("v1|nat|nubank|conta-1|uuid-01"),
		dedup.NaturalKey("nubank", "conta-1", "uuid-01"))

	assert.Equal(t,
		sha("v1|der|conta-1|expense|2026-08-14|1100|cafe exemplo"),
		dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-14"), 1100, "cafe exemplo"))

	assert.Equal(t,
		sha("v1|man|tx-001"),
		dedup.ManualKey("tx-001"))

	assert.Equal(t,
		sha("v1|pair|grupo-1|in"),
		dedup.PairKey("grupo-1"))
}

func TestChaveTemALarguraDaColuna(t *testing.T) {
	// transactions.dedup_key é varchar(64), e o hex do SHA-256 tem exatamente
	// 64 caracteres. Não é coincidência: é o dimensionamento da coluna.
	for nome, chave := range map[string]string{
		"natural": dedup.NaturalKey("nubank", "conta-1", "uuid-01"),
		"derivada": dedup.DerivedKey(
			"conta-1", transaction.KindExpense, data("2026-08-14"), 1100, "cafe exemplo"),
		"manual":        dedup.ManualKey("tx-001"),
		"transferência": dedup.PairKey("grupo-1"),
	} {
		assert.Len(t, chave, 64, "chave %s", nome)
	}
}

// TestTiposDeChaveNaoColidem: o discriminador (nat/der/man/pair) existe para
// que uma chave natural nunca possa cair em cima de uma derivada.
func TestTiposDeChaveNaoColidem(t *testing.T) {
	chaves := []string{
		dedup.NaturalKey("nubank", "conta-1", "x"),
		dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-14"), 1100, "x"),
		dedup.ManualKey("x"),
		dedup.PairKey("x"),
	}
	vistas := map[string]bool{}
	for _, c := range chaves {
		require.False(t, vistas[c], "duas chaves de tipos diferentes colidiram")
		vistas[c] = true
	}
}

// TestCadaCampoMudaAChaveDerivada trava a composição da chave: se algum campo
// deixar de participar, duas linhas diferentes passam a ser a mesma, e uma
// some.
func TestCadaCampoMudaAChaveDerivada(t *testing.T) {
	base := dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-14"), 1100, "cafe exemplo")

	variacoes := map[string]string{
		"outra conta":     dedup.DerivedKey("conta-2", transaction.KindExpense, data("2026-08-14"), 1100, "cafe exemplo"),
		"outro kind":      dedup.DerivedKey("conta-1", transaction.KindIncome, data("2026-08-14"), 1100, "cafe exemplo"),
		"outra data":      dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-15"), 1100, "cafe exemplo"),
		"outro valor":     dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-14"), 1101, "cafe exemplo"),
		"outra descrição": dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-14"), 1100, "cafe exemplo 2"),
	}

	for nome, chave := range variacoes {
		assert.NotEqual(t, base, chave, "mudar %s tinha de mudar a chave", nome)
	}
}

func TestCadaCampoMudaAChaveNatural(t *testing.T) {
	base := dedup.NaturalKey("nubank", "conta-1", "uuid-01")

	// Um id só é único DENTRO do emissor: sem a instituição na chave, o "1" de
	// um banco seria o "1" do outro.
	assert.NotEqual(t, base, dedup.NaturalKey("c6", "conta-1", "uuid-01"))
	assert.NotEqual(t, base, dedup.NaturalKey("nubank", "conta-2", "uuid-01"))
	assert.NotEqual(t, base, dedup.NaturalKey("nubank", "conta-1", "uuid-02"))
}

// TestChaveEhEstavel: a mesma entrada dá sempre a mesma chave. É a propriedade
// de que a reimportação depende inteiramente.
func TestChaveEhEstavel(t *testing.T) {
	for range 100 {
		assert.Equal(t,
			dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-14"), 1100, "cafe exemplo"),
			dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-14"), 1100, "cafe exemplo"))
	}
}

// TestVersaoDaChaveEstaCongelada é um lembrete com dente: mudar KeyVersion cega
// a deduplicação contra tudo o que já foi importado, e exige ADR (ADR-025c).
func TestVersaoDaChaveEstaCongelada(t *testing.T) {
	assert.Equal(t, "v1", dedup.KeyVersion,
		"mudar a versão da chave exige ADR e uma decisão sobre o histórico já importado")
}

// TestSeparadorNoUltimoCampoNaoCriaAmbiguidade: o campo de forma livre é sempre
// o ÚLTIMO, e por isso o "|" dentro dele não consegue fabricar colisão com
// outra tupla. Os campos do meio são conferidos por Analyze.
func TestSeparadorNoUltimoCampoNaoCriaAmbiguidade(t *testing.T) {
	assert.NotEqual(t,
		dedup.NaturalKey("nubank", "conta-1", "a|b"),
		dedup.NaturalKey("nubank", "conta-1", "a|b|c"))

	assert.NotEqual(t,
		dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-14"), 1100, "a|b"),
		dedup.DerivedKey("conta-1", transaction.KindExpense, data("2026-08-14"), 1100, "a|b|c"))
}
