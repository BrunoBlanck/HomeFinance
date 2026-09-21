package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
)

// fixturesDeImportacao lista TODA fixture anonimizada dos parsers
// (internal/importer/*/testdata/*.csv), pelo glob — e não por uma lista escrita
// à mão. É de propósito: um parser novo traz a sua fixture, e ela entra nesta
// prova sem que ninguém precise lembrar de acrescentá-la.
func fixturesDeImportacao(t *testing.T) []string {
	t.Helper()
	padrao := filepath.Join("..", "..", "internal", "importer", "*", "testdata", "*.csv")
	arquivos, err := filepath.Glob(padrao)
	require.NoError(t, err)
	require.NotEmpty(t, arquivos, "nenhuma fixture encontrada em %s", padrao)
	return arquivos
}

// TestRegistroRealLeCadaFixtureComExatamenteUmParser é a prova que fecha a
// porta descrita no achado B2 da revisão de segurança do parser do Inter: os
// testes-ouro de cada pacote montam o registro à mão, copiando a lista do
// main.go — e um sexto parser acrescentado ao main.go sem tocar naqueles testes
// perderia, em silêncio, a garantia de "cada fixture casa com exatamente um
// parser". Aqui o registro é o MESMO que o serviço real usa (newParserRegistry),
// e a lista de fixtures vem do disco.
//
// Para cada fixture: exatamente um candidato (nem IMPORT_FORMAT_UNKNOWN nem
// IMPORT_FORMAT_AMBIGUOUS), o parse completo funciona, e o id escolhido carrega
// o nome do pacote da fixture — um arquivo em `c6/testdata` lido por
// `nubank.*` seria a detecção roubada que a §7.1 da spec 0004 proíbe.
func TestRegistroRealLeCadaFixtureComExatamenteUmParser(t *testing.T) {
	t.Parallel()

	reg, err := newParserRegistry()
	require.NoError(t, err)

	for _, caminho := range fixturesDeImportacao(t) {
		pacote := filepath.Base(filepath.Dir(filepath.Dir(caminho)))
		nome := filepath.Base(caminho)

		t.Run(pacote+"/"+nome, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(caminho)
			require.NoError(t, err)

			doc, err := reg.OpenDocument(raw)
			require.NoError(t, err, "o cabeçalho da fixture precisa ser reconhecido")
			candidatos := reg.Candidates(doc.Table.Header, doc.Table.Separator)
			require.Len(t, candidatos, 1, "exatamente um parser candidato, e não %v", candidatos)
			assert.True(t, strings.HasPrefix(candidatos[0], pacote+"."),
				"a fixture de %s foi reconhecida por %s — detecção roubada por outro emissor", pacote, candidatos[0])

			res, err := reg.Parse(context.Background(), raw, "", importer.DefaultLimits())
			require.NoError(t, err)
			assert.Equal(t, candidatos[0], res.FormatID)
			assert.NotEmpty(t, res.Rows, "a fixture precisa ter ao menos uma linha aproveitável")
			assert.Empty(t, res.Rejected, "nenhuma linha de fixture deveria ser rejeitada")
			assert.Equal(t, importer.Institution(pacote), res.Institution,
				"a instituição do resultado é a do pacote da fixture")
		})
	}
}

// TestRegistroRealTemUmParserPorFixture fecha o outro lado: todo parser
// registrado tem fixture. Um parser sem fixture é um parser sem teste-ouro —
// e sem teste-ouro a convenção de sinal dele nunca foi provada contra um
// arquivo real anonimizado.
func TestRegistroRealTemUmParserPorFixture(t *testing.T) {
	t.Parallel()

	reg, err := newParserRegistry()
	require.NoError(t, err)

	reconhecidos := make(map[string]bool, len(reg.FormatIDs()))
	for _, caminho := range fixturesDeImportacao(t) {
		raw, err := os.ReadFile(caminho)
		require.NoError(t, err)
		doc, err := reg.OpenDocument(raw)
		require.NoError(t, err)
		for _, id := range reg.Candidates(doc.Table.Header, doc.Table.Separator) {
			reconhecidos[id] = true
		}
	}

	for _, id := range reg.FormatIDs() {
		assert.True(t, reconhecidos[id], "o parser %s está registrado e nenhuma fixture o exercita", id)
	}
}
