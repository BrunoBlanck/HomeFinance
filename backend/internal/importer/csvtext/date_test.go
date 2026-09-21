package csvtext

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/stretchr/testify/require"
)

func TestParseDateAceita(t *testing.T) {
	casos := []struct {
		entrada  string
		formato  DateFormat
		esperado civil.Date
	}{
		{"04/08/2026", DateDMY, civil.MustNew(2026, 8, 4)},
		{"31/12/2026", DateDMY, civil.MustNew(2026, 12, 31)},
		{"29/02/2024", DateDMY, civil.MustNew(2024, 2, 29)}, // bissexto de verdade
		{"  04/08/2026  ", DateDMY, civil.MustNew(2026, 8, 4)},
		{"2026-09-05", DateISO, civil.MustNew(2026, 9, 5)},
		{"2024-02-29", DateISO, civil.MustNew(2024, 2, 29)},
	}
	for _, c := range casos {
		t.Run(c.entrada, func(t *testing.T) {
			d, err := ParseDate(c.entrada, c.formato)
			require.NoError(t, err)
			require.Equal(t, c.esperado, d)
		})
	}
}

// TestParseDateNaoNormalizaDataInexistente é o caso que separa "validar" de
// "aceitar": `time.Parse` normalizaria 31/02/2026 para 03/03/2026 em silêncio, e
// o lançamento apareceria num mês que não é o dele.
func TestParseDateNaoNormalizaDataInexistente(t *testing.T) {
	casos := []struct {
		entrada string
		formato DateFormat
	}{
		{"31/02/2026", DateDMY},
		{"30/02/2026", DateDMY},
		{"29/02/2026", DateDMY}, // 2026 não é bissexto
		{"31/04/2026", DateDMY},
		{"2026-02-31", DateISO},
		{"2026-02-29", DateISO},
	}
	for _, c := range casos {
		t.Run(c.entrada, func(t *testing.T) {
			_, err := ParseDate(c.entrada, c.formato)
			require.ErrorIs(t, err, ErrInvalidDate)
		})
	}
}

func TestParseDateRecusa(t *testing.T) {
	casos := []struct {
		nome    string
		entrada string
		formato DateFormat
	}{
		{"vazio", "", DateDMY},
		{"só espaços", "   ", DateDMY},
		{"texto", "ontem", DateDMY},
		{"sem zero à esquerda", "4/8/2026", DateDMY},
		{"separador errado", "04-08-2026", DateDMY},
		{"formato ISO no slot DMY", "2026-08-04", DateDMY},
		{"formato DMY no slot ISO", "04/08/2026", DateISO},
		{"com hora", "04/08/2026 10:30", DateDMY},
		{"ano com duas casas", "04/08/26", DateDMY},
		{"mês zero", "04/00/2026", DateDMY},
		{"dia zero", "00/08/2026", DateDMY},
		{"mês treze", "04/13/2026", DateDMY},
		{"não numérico", "aa/bb/cccc", DateDMY},
		{"ISO com hora", "2026-09-05T00:00:00Z", DateISO},
		{"formato não declarado", "04/08/2026", DateFormat(0)},
		{"formato inexistente", "04/08/2026", DateFormat(99)},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := ParseDate(c.entrada, c.formato)
			require.ErrorIs(t, err, ErrInvalidDate)
		})
	}
}

// TestParseDateNaoAdivinhaFormato é a razão de DateFormat existir: o MESMO
// texto tem duas leituras legítimas, e só a declaração de quem chama decide.
func TestParseDateNaoAdivinhaFormato(t *testing.T) {
	d, err := ParseDate("03/04/2026", DateDMY)
	require.NoError(t, err)
	require.Equal(t, civil.MustNew(2026, 4, 3), d, "em DD/MM/YYYY isto é 3 de abril")

	_, err = ParseDate("03/04/2026", DateISO)
	require.ErrorIs(t, err, ErrInvalidDate, "o mesmo texto não pode ser aceito num formato que não é o dele")
}
