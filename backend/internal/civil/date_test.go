package civil_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAceitaSomenteAFormaCanonica(t *testing.T) {
	t.Parallel()

	validos := []string{"2026-09-12", "1970-01-01", "2024-02-29", "9999-12-31"}
	for _, entrada := range validos {
		d, err := civil.Parse(entrada)
		require.NoError(t, err, entrada)
		assert.Equal(t, entrada, d.String())
	}

	// Cada um destes já apareceu como bug em algum sistema de data: mês zero,
	// dia que não existe no mês, 29/02 em ano comum, largura variável, sufixo
	// de hora, e o "0000-00-00" que o MySQL recusa com NO_ZERO_DATE.
	invalidos := []string{
		"", "2026-9-12", "2026-09-1", "2026/09/12", "12-09-2026",
		"0000-00-00", "2026-00-10", "2026-13-01", "2026-02-30", "2025-02-29",
		"2026-04-31", "2026-09-12T00:00:00Z", " 2026-09-12", "2026-09-12 ",
		"+026-09-12", "abcd-ef-gh", "2026-09-12x",
	}
	for _, entrada := range invalidos {
		_, err := civil.Parse(entrada)
		assert.ErrorIs(t, err, civil.ErrInvalidDate, "deveria recusar %q", entrada)
	}
}

func TestNewRecusaDataNormalizadaPeloTime(t *testing.T) {
	t.Parallel()

	// time.Date normalizaria 31/02/2026 para 03/03/2026 sem reclamar. Aceitar
	// isso significaria gravar uma data que o usuário não digitou.
	_, err := civil.New(2026, 2, 31)
	require.ErrorIs(t, err, civil.ErrInvalidDate)

	_, err = civil.New(2024, 2, 29) // bissexto de verdade
	require.NoError(t, err)
}

func TestZeroNuncaViraTextoDeDataZero(t *testing.T) {
	t.Parallel()

	var d civil.Date
	assert.True(t, d.IsZero())
	// "0000-00-00" é justamente o valor que o MySQL recusa; string vazia é o
	// sinal de "não informado" que o resto do código sabe tratar.
	assert.Equal(t, "", d.String())
	assert.Equal(t, "", d.YearMonth())
}

func TestYearMonthEhAChaveDeAgregacao(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "2026-09", civil.MustNew(2026, 9, 12).YearMonth())
	assert.Equal(t, "2026-01", civil.MustNew(2026, 1, 1).YearMonth())
}

func TestOrdemLexicograficaBateComOrdemCronologica(t *testing.T) {
	t.Parallel()

	// É disto que depende ordenar por data no banco sem função de data
	// (armadilha P1): o texto de largura fixa ordena igual ao calendário.
	datas := []civil.Date{
		civil.MustNew(2026, 1, 9),
		civil.MustNew(2026, 1, 10),
		civil.MustNew(2026, 2, 1),
		civil.MustNew(2027, 1, 1),
	}
	for i := 1; i < len(datas); i++ {
		anterior, atual := datas[i-1], datas[i]
		assert.True(t, anterior.Before(atual))
		assert.Equal(t, -1, anterior.Compare(atual))
		assert.Less(t, anterior.String(), atual.String(),
			"a ordem do texto precisa acompanhar a do calendário")
	}
}

// O bug que este teste existe para impedir (R6 do PLANOS.md): às 21h de
// São Paulo já é o dia seguinte em UTC. Se "hoje" fosse calculado no fuso do
// servidor, a conta apareceria como vencida um dia antes.
func TestTodayUsaOFusoDaCasaENaoODoServidor(t *testing.T) {
	t.Parallel()

	sp, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	// 13/09/2026 00:30 UTC = 12/09/2026 21:30 em São Paulo.
	instante := time.Date(2026, 9, 13, 0, 30, 0, 0, time.UTC)

	assert.Equal(t, "2026-09-13", civil.FromTime(instante, time.UTC).String())
	assert.Equal(t, "2026-09-12", civil.FromTime(instante, sp).String())
}

func TestFromTimeSemFusoCaiEmUTCEmVezDeEntrarEmPanico(t *testing.T) {
	t.Parallel()

	instante := time.Date(2026, 9, 13, 0, 30, 0, 0, time.UTC)
	assert.Equal(t, "2026-09-13", civil.FromTime(instante, nil).String())
}

func TestJSONUsaOFormatoDoContrato(t *testing.T) {
	t.Parallel()

	type payload struct {
		Data civil.Date `json:"data"`
	}

	bytes, err := json.Marshal(payload{Data: civil.MustNew(2026, 9, 12)})
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":"2026-09-12"}`, string(bytes))

	var lido payload
	require.NoError(t, json.Unmarshal([]byte(`{"data":"2026-09-12"}`), &lido))
	assert.Equal(t, civil.MustNew(2026, 9, 12), lido.Data)

	// Data zero vira null, nunca "0000-00-00".
	bytes, err = json.Marshal(payload{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":null}`, string(bytes))
}

func TestJSONRecusaEntradaHostil(t *testing.T) {
	t.Parallel()

	type payload struct {
		Data civil.Date `json:"data"`
	}

	// Número, objeto e data inexistente precisam falhar na desserialização —
	// é a borda onde entrada externa vira tipo de domínio.
	for _, corpo := range []string{
		`{"data":20260912}`,
		`{"data":{"ano":2026}}`,
		`{"data":"2026-02-30"}`,
		`{"data":"hoje"}`,
	} {
		var lido payload
		assert.Error(t, json.Unmarshal([]byte(corpo), &lido), corpo)
	}
}

func TestDaysBetweenEhAbsolutoEAtravessaMesAnoEBissexto(t *testing.T) {
	casos := []struct {
		nome     string
		a, b     civil.Date
		esperado int
	}{
		{"mesma data", civil.MustNew(2026, 8, 14), civil.MustNew(2026, 8, 14), 0},
		{"um dia", civil.MustNew(2026, 8, 14), civil.MustNew(2026, 8, 15), 1},
		{"é absoluto", civil.MustNew(2026, 8, 15), civil.MustNew(2026, 8, 14), 1},
		{"vira o mês", civil.MustNew(2026, 8, 31), civil.MustNew(2026, 9, 1), 1},
		{"vira o ano", civil.MustNew(2025, 12, 31), civil.MustNew(2026, 1, 1), 1},
		{"ano bissexto", civil.MustNew(2024, 2, 28), civil.MustNew(2024, 3, 1), 2},
		{"ano comum", civil.MustNew(2026, 2, 28), civil.MustNew(2026, 3, 1), 1},
		{"século não bissexto", civil.MustNew(1900, 2, 28), civil.MustNew(1900, 3, 1), 1},
		{"século bissexto", civil.MustNew(2000, 2, 28), civil.MustNew(2000, 3, 1), 2},
		{"um ano comum", civil.MustNew(2026, 1, 1), civil.MustNew(2027, 1, 1), 365},
		{"a folga de três dias da deduplicação", civil.MustNew(2026, 8, 29), civil.MustNew(2026, 9, 1), 3},
		{"data zero devolve zero", civil.Date{}, civil.MustNew(2026, 8, 14), 0},
		{"as duas zero devolvem zero", civil.Date{}, civil.Date{}, 0},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			assert.Equal(t, c.esperado, civil.DaysBetween(c.a, c.b))
			assert.Equal(t, c.esperado, civil.DaysBetween(c.b, c.a), "simétrico")
		})
	}
}

func TestAddDaysAtravessaMesAnoEBissextoESeInverteComDaysBetween(t *testing.T) {
	casos := []struct {
		nome     string
		de       civil.Date
		n        int
		esperado civil.Date
	}{
		{"sem deslocamento", civil.MustNew(2026, 8, 14), 0, civil.MustNew(2026, 8, 14)},
		{"a folga da deduplicação para frente", civil.MustNew(2026, 8, 29), 3, civil.MustNew(2026, 9, 1)},
		{"a folga da deduplicação para trás", civil.MustNew(2026, 9, 1), -3, civil.MustNew(2026, 8, 29)},
		{"vira o ano", civil.MustNew(2025, 12, 31), 1, civil.MustNew(2026, 1, 1)},
		{"volta o ano", civil.MustNew(2026, 1, 1), -1, civil.MustNew(2025, 12, 31)},
		{"fevereiro bissexto", civil.MustNew(2024, 2, 28), 1, civil.MustNew(2024, 2, 29)},
		{"fevereiro comum", civil.MustNew(2026, 2, 28), 1, civil.MustNew(2026, 3, 1)},
		{"data zero não se desloca", civil.Date{}, 5, civil.Date{}},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := civil.AddDays(c.de, c.n)
			assert.Equal(t, c.esperado, got)
			if !c.de.IsZero() {
				// AddDays e DaysBetween falam da mesma distância.
				esperadaDistancia := c.n
				if esperadaDistancia < 0 {
					esperadaDistancia = -esperadaDistancia
				}
				assert.Equal(t, esperadaDistancia, civil.DaysBetween(c.de, got))
				assert.Equal(t, c.de, civil.AddDays(got, -c.n), "deslocar e voltar devolve a original")
			}
		})
	}
}
