package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
)

// Baldes do menu IA (spec 0010 §8.6) e a emenda do estouro das duas rotas do
// "Reprocessar" (achado A1 da emenda §10 — decisão do usuário, 21/09/2026).
//
// Fica em arquivo PRÓPRIO, e não no meio de config_test.go, pelo mesmo motivo
// de investmentdetect_test.go: aquele arquivo guarda os invariantes de limite e
// está sendo editado por outras frentes; teste novo não vale um conflito nele.
//
// O invariante universal (0 < Burst < Requests em TODA regra, nos três perfis)
// já é varrido por reflexão em TestRotasDeEscritaEmMassaTemEstouroMenorQueACota,
// e a classificação de cada balde novo mora em regrasPorClasse. O que se afirma
// aqui são os NÚMEROS, as RELAÇÕES e — o principal — o CAMINHO FELIZ da §5.2,
// que a reflexão não sabe medir.

func TestBaldesDoMenuIATemCotaPropriaPorCasa(t *testing.T) {
	t.Parallel()

	rl := config.DefaultRateLimits()

	// AiExport nasceu com estouro 3 e subiu para 6 em 21/09/2026 (achado A do
	// `qa-testes`): com 3, a QUARTA requisição da tela era 429 determinístico.
	// A justificativa está no doc do campo em ratelimits.go e medida em
	// TestExportarExplorandoAJanelaCabeNoEstouro, abaixo.
	assert.Equal(t, config.Rule{Requests: 30, Window: time.Hour, Burst: 6}, rl.AiExport)
	assert.Equal(t, config.Rule{Requests: 60, Window: time.Hour, Burst: 3}, rl.AiImportPreview)
	assert.Equal(t, config.Rule{Requests: 30, Window: time.Hour, Burst: 3}, rl.AiImportConfirm)

	// A relação entre os dois lados do par é a mesma do resto do projeto: a
	// metade barata e iterativa (a prévia) tem o dobro da cota da metade cara
	// (o confirm). Ela é a razão de os números serem estes, e não outros.
	assert.Equal(t, 2*rl.AiImportConfirm.Requests, rl.AiImportPreview.Requests,
		"a prévia é a metade iterativa do par: o teto dela é o dobro do confirm")
	assert.Equal(t, rl.AiImportConfirm.Window, rl.AiImportPreview.Window)

	// A exportação acompanha o confirm da importação: agregação pesada que
	// ninguém repete dezenas de vezes por hora.
	assert.Equal(t, rl.ImportConfirm.Requests, rl.AiExport.Requests,
		"exportar o prompt custa o que um confirm de importação custa")

	// Os três são baldes SEPARADOS (campos próprios em RateLimits): usar um
	// não pode trancar o seguinte, e a pessoa exporta, confere e confirma na
	// mesma sentada, nessa ordem. O que se afirma aqui é o que cada um tem de
	// respeitar por si.
	for nome, regra := range map[string]config.Rule{
		"AiExport":        rl.AiExport,
		"AiImportPreview": rl.AiImportPreview,
		"AiImportConfirm": rl.AiImportConfirm,
	} {
		assert.Positivef(t, regra.Burst, "regra %s sem estouro declarado", nome)
		assert.Lessf(t, regra.Burst, regra.Requests,
			"regra %s: estouro >= cota não limita nada", nome)
	}
}

// O perfil frouxo mantém a FORMA das três regras: mesma janela, teto nunca
// menor, estouro positivo e menor que a cota, e a relação prévia = 2 × confirm
// preservada. Frouxo não é "sem limite".
func TestBaldesDoMenuIAMantemAFormaNoPerfilDeTeste(t *testing.T) {
	t.Parallel()

	rl := config.ProfileRateLimits(config.RateLimitProfileTest)
	padrao := config.DefaultRateLimits()

	assert.Equal(t, config.Rule{Requests: 300, Window: time.Hour, Burst: 30}, rl.AiExport)
	assert.Equal(t, config.Rule{Requests: 600, Window: time.Hour, Burst: 30}, rl.AiImportPreview)
	assert.Equal(t, config.Rule{Requests: 300, Window: time.Hour, Burst: 30}, rl.AiImportConfirm)

	assert.Equal(t, 2*rl.AiImportConfirm.Requests, rl.AiImportPreview.Requests,
		"a forma da regra é preservada no perfil frouxo, não só o teto")

	// E o principal: o afrouxamento do teste NÃO vazou para produção.
	assert.Equal(t, 30, padrao.AiExport.Requests)
	assert.Equal(t, 60, padrao.AiImportPreview.Requests)
	assert.Equal(t, 30, padrao.AiImportConfirm.Requests)
	assert.Equal(t, 6, padrao.AiExport.Burst, "o estouro da exportação é 6 desde o achado A do QA")
	assert.Equal(t, 3, padrao.AiImportPreview.Burst)
	assert.Equal(t, 3, padrao.AiImportConfirm.Burst)
}

// Os três baldes chegam ao boot: Load() sem variável de perfil entrega os
// padrões, e regra nova não pode ficar zerada no caminho que produção usa —
// Rule{} zerado significaria, no limitador, "1 requisição por minuto".
func TestBaldesDoMenuIAChegamAoBoot(t *testing.T) {
	cfg, err := config.LoadFrom(baseEnv())
	require.NoError(t, err)

	rl := cfg.RateLimits
	assert.Equal(t, config.DefaultRateLimits().AiExport, rl.AiExport)
	assert.Equal(t, config.DefaultRateLimits().AiImportPreview, rl.AiImportPreview)
	assert.Equal(t, config.DefaultRateLimits().AiImportConfirm, rl.AiImportConfirm)
}

// O perfil de DESENVOLVIMENTO não toca nos três — ele eleva só a importação
// (decisão do usuário, 18/09/2026). A varredura por reflexão de
// TestPerfilDeDesenvolvimentoSobeSoAImportacao já garante isso campo a campo;
// a asserção aqui é explícita para o leitor que vier pelo arquivo da feature.
func TestPerfilDeDesenvolvimentoNaoAfrouxaOMenuIA(t *testing.T) {
	t.Parallel()

	dev := config.ProfileRateLimits(config.RateLimitProfileDev)
	padrao := config.DefaultRateLimits()

	assert.Equal(t, padrao.AiExport, dev.AiExport)
	assert.Equal(t, padrao.AiImportPreview, dev.AiImportPreview)
	assert.Equal(t, padrao.AiImportConfirm, dev.AiImportConfirm)
}

// ---------------------------------------------------------------------------
// O CAMINHO FELIZ da EXPORTAÇÃO — o teste que justifica o Burst 3 -> 6 do
// AiExport (achado A do `qa-testes`, 21/09/2026)
// ---------------------------------------------------------------------------

// TestExportarExplorandoAJanelaCabeNoEstouro mede, no limitador REAL e com
// relógio congelado, o que a tela `/ia` faz quando a pessoa mexe no seletor.
//
// A tela pede UM prompt por par (mês do cabeçalho × tamanho da janela), com
// `staleTime: Infinity` — cada par é buscado uma vez e fica em cache. Explorar
// os TRÊS tamanhos que a spec 0010 permite (1, 2 e 3 meses de competência) e
// depois trocar o mês do cabeçalho e explorar de novo são 3 × 2 = 6
// requisições, em sequência, em segundos.
//
// O relógio fica PARADO de propósito: 30 req/h repõem 1 token a cada 2
// minutos, e ninguém espera dois minutos entre dois cliques num seletor. Com o
// estouro antigo (3) a QUARTA requisição era 429 determinístico e o botão
// "Tentar de novo" também — a tela nascia quebrada para todo mundo, sem abuso
// nenhum. É o achado A1 da emenda §10 repetido no balde que nasceu depois dele.
func TestExportarExplorandoAJanelaCabeNoEstouro(t *testing.T) {
	t.Parallel()

	// Os tamanhos de janela que a spec 0010 §2.1 permite: 1, 2 ou 3 meses de
	// competência. Constante local, e não o teto importado do domínio, pelo
	// mesmo motivo do teste vizinho: `platform/config` não conhece domínio.
	const tamanhosDeJanela = 3
	// Quantas vezes a pessoa troca o mês do cabeçalho antes de a primeira
	// reposição chegar. Dois é o mínimo realista: o mês corrente e o anterior.
	const mesesDoCabecalho = 2

	rl := config.DefaultRateLimits()
	regra := rl.AiExport

	agora := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	lim := httpserver.NewLimiter(regra.Requests, regra.Window, rl.IdleTTL).
		WithBurst(regra.Burst).
		WithClock(func() time.Time { return agora })

	const casa = "hmac-da-casa"

	for mes := 1; mes <= mesesDoCabecalho; mes++ {
		for tamanho := 1; tamanho <= tamanhosDeJanela; tamanho++ {
			ok, espera := lim.Allow(casa)
			require.Truef(t, ok,
				"mês %d, janela de %d mês(es): 429 (esperar %s) — a tela /ia nasce quebrada no seletor",
				mes, tamanho, espera)
		}
	}

	// E o balde continua FINITO: a 7ª chamada instantânea é negada. É o que
	// separa "cabe o uso legítimo" de "não limita nada".
	ok, espera := lim.Allow(casa)
	assert.False(t, ok,
		"a 7ª chamada instantânea tinha de ser 429 — o estouro é %d, não a cota", regra.Burst)
	assert.Positive(t, espera, "429 precisa dizer quanto esperar (Retry-After)")

	// A conta que sustenta o número, escrita como asserção para que mexer no
	// Burst sem refazer a conta fique vermelho — e para que ninguém o suba
	// "só mais um pouco" sem dizer que uso novo isso atende.
	assert.Equalf(t, tamanhosDeJanela*mesesDoCabecalho, regra.Burst,
		"o estouro é exatamente os %d tamanhos de janela × %d meses de cabeçalho",
		tamanhosDeJanela, mesesDoCabecalho)

	// Com 5 o uso legítimo NÃO cabia: a 6ª (a última exploração do segundo
	// mês) era negada. Provar isso é o que transforma "6" no MENOR número que
	// serve, em vez de uma folga escolhida a olho.
	apertado := httpserver.NewLimiter(regra.Requests, regra.Window, rl.IdleTTL).
		WithBurst(regra.Burst - 1).
		WithClock(func() time.Time { return agora })
	for i := 1; i < tamanhosDeJanela*mesesDoCabecalho; i++ {
		permitida, _ := apertado.Allow(casa)
		require.Truef(t, permitida, "com estouro %d a %dª ainda passava", regra.Burst-1, i)
	}
	ultima, _ := apertado.Allow(casa)
	assert.Falsef(t, ultima,
		"com estouro %d a %dª seria negada — é por isso que %d é o menor número que serve",
		regra.Burst-1, tamanhosDeJanela*mesesDoCabecalho, regra.Burst)

	// As mesmas duas margens do teste vizinho: o estouro segue muito abaixo do
	// pool de conexões e da cota horária.
	const poolDeConexoes = 25
	assert.LessOrEqual(t, regra.Burst*4, poolDeConexoes,
		"o estouro precisa caber 4 vezes no pool de 25 conexões (achado A2)")
	assert.LessOrEqual(t, regra.Burst*5, regra.Requests,
		"o estouro precisa caber 5 vezes na cota horária")
}

// ---------------------------------------------------------------------------
// O CAMINHO FELIZ da §5.2 — o teste que justifica o Burst 3 -> 6
// ---------------------------------------------------------------------------

// TestReprocessarTresMesesCabeNoEstouroDasDuasRotas mede, no limitador REAL e
// com relógio congelado, o que o botão "Reprocessar" do menu IA faz:
//
//	Conferir     → 3 chamadas (uma por mês, dryRun) em CADA balde
//	Reprocessar  → 3 chamadas (uma por mês, execução) em CADA balde
//
// São 6 chamadas por balde, em sequência, em segundos. O relógio fica PARADO
// de propósito: 60 req/h repõem 1 token por minuto, e uma pessoa não espera um
// minuto entre dois cliques. Com o estouro antigo (3) a 4ª chamada era 429
// DETERMINÍSTICO — o botão nascia quebrado para todo mundo, sem abuso nenhum.
//
// O teste também mede o outro lado: a 7ª chamada instantânea É negada. 6 é o
// menor número que faz o uso legítimo caber, não uma folga escolhida a olho, e
// o balde continua finito.
func TestReprocessarTresMesesCabeNoEstouroDasDuasRotas(t *testing.T) {
	t.Parallel()

	const mesesDaJanela = 3 // o máximo da janela de trabalho (spec 0010 §2.1)

	rl := config.DefaultRateLimits()

	baldes := map[string]config.Rule{
		"TransferDetect": rl.TransferDetect,
		"AutoCategorize": rl.AutoCategorize,
	}

	for nome, regra := range baldes {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			// Relógio congelado: nenhum token é reposto durante a sequência.
			agora := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
			lim := httpserver.NewLimiter(regra.Requests, regra.Window, rl.IdleTTL).
				WithBurst(regra.Burst).
				WithClock(func() time.Time { return agora })

			const casa = "hmac-da-casa"

			// Conferir: a prévia de cada mês.
			for mes := 1; mes <= mesesDaJanela; mes++ {
				ok, espera := lim.Allow(casa)
				require.Truef(t, ok,
					"prévia do mês %d levou 429 (esperar %s): o botão Conferir nasce quebrado", mes, espera)
			}

			// Reprocessar: a execução de cada mês, logo em seguida.
			for mes := 1; mes <= mesesDaJanela; mes++ {
				ok, espera := lim.Allow(casa)
				require.Truef(t, ok,
					"execução do mês %d levou 429 (esperar %s): o botão Reprocessar nasce quebrado", mes, espera)
			}

			// E o balde continua FINITO: a 7ª chamada instantânea é negada.
			// É o que separa "cabe o uso legítimo" de "não limita nada".
			ok, espera := lim.Allow(casa)
			assert.False(t, ok,
				"a 7ª chamada instantânea tinha de ser 429 — o estouro é %d, não a cota", regra.Burst)
			assert.Positive(t, espera, "429 precisa dizer quanto esperar (Retry-After)")

			// A conta que sustenta o número, escrita como asserção para que
			// mexer no Burst sem refazer a conta fique vermelho.
			assert.Equalf(t, 2*mesesDaJanela, regra.Burst,
				"%s: o estouro é exatamente prévia + execução da janela cheia (%d × 2)", nome, mesesDaJanela)
		})
	}

	// O estouro segue MUITO abaixo do que o achado A2 protege: o pool de
	// conexões (DB_MAX_OPEN_CONNS = 25) e a cota horária. As duas margens
	// estão escritas como asserção para que subir o Burst "só mais um pouco"
	// no futuro fique vermelho aqui, e não em produção.
	const poolDeConexoes = 25
	for nome, regra := range baldes {
		assert.LessOrEqualf(t, regra.Burst*4, poolDeConexoes,
			"%s: o estouro precisa caber 4 vezes no pool de %d conexões (achado A2)", nome, poolDeConexoes)
		assert.LessOrEqualf(t, regra.Burst*10, regra.Requests,
			"%s: o estouro precisa caber 10 vezes na cota horária (%d)", nome, regra.Requests)
	}
}
