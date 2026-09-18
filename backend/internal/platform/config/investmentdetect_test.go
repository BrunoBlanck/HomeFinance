package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
)

// Balde de POST /investments/detect (spec 0006 §3.3.5, ADR-029h).
//
// Fica em arquivo PRÓPRIO, e não no meio de config_test.go, porque aquele
// arquivo está sendo editado por outra frente ao mesmo tempo — e um teste novo
// não vale um conflito no arquivo que guarda os invariantes de limite.
//
// O invariante universal (0 < Burst < Requests em TODA regra, nos dois perfis)
// já é varrido por reflexão em TestRotasDeEscritaEmMassaTemEstouroMenorQueACota.
// O que se afirma aqui são os NÚMEROS e as RELAÇÕES desta regra, que a
// reflexão não sabe.

func TestInvestmentDetectTemBaldeProprioDe60PorHora(t *testing.T) {
	t.Parallel()

	rl := config.DefaultRateLimits()

	// Mesma cota das duas irmãs de escrita em massa: cada uso legítimo gasta
	// prévia + confirmação, e 60/h dá o mesmo número de USOS por hora que o
	// confirm da importação (30/h).
	assert.Equal(t, config.Rule{Requests: 60, Window: time.Hour, Burst: 3}, rl.InvestmentDetect)
	assert.Equal(t, rl.AutoCategorize.Requests, rl.InvestmentDetect.Requests)
	assert.Equal(t, rl.TransferDetect.Requests, rl.InvestmentDetect.Requests)
	assert.Equal(t, 2*rl.ImportConfirm.Requests, rl.InvestmentDetect.Requests,
		"cada uso gasta prévia + confirmação: a cota é o dobro do confirm da importação")

	// O ESTOURO é o ponto (achado A2): na execução real os UPDATE e a
	// auditoria rodam DENTRO de uma transação, então cada requisição em voo
	// segura uma conexão do pool. Estouro igual à cota deixaria uma casa
	// sozinha ocupar o pool inteiro sem passar de limite nenhum.
	assert.Equal(t, 3, rl.InvestmentDetect.Burst)
	assert.Less(t, rl.InvestmentDetect.Burst, rl.InvestmentDetect.Requests,
		"rota cara não pode gastar a cota inteira de uma vez")
	assert.Equal(t, rl.TransferDetect.Burst, rl.InvestmentDetect.Burst,
		"as rotas de escrita em massa têm o MESMO estouro — divergir é decisão, não descuido")
}

// O perfil frouxo mantém a FORMA da regra: mesma janela, teto nunca menor, e o
// estouro continua positivo e menor que a cota. Frouxo não é "sem limite".
//
// Ele acompanha as duas irmãs (600/h, estouro 30) porque a suíte de ponta a
// ponta encadeia prévia e confirmação e não pode esperar um balde de 3 se
// recompor — com o teto de produção, o Playwright levaria 429 intermitente.
// A diferença entre os perfis é o ponto do teste: produção NÃO sobe junto.
func TestInvestmentDetectMantemAFormaNoPerfilDeTeste(t *testing.T) {
	t.Parallel()

	rl := config.ProfileRateLimits(config.RateLimitProfileTest)
	padrao := config.DefaultRateLimits()

	assert.Equal(t, config.Rule{Requests: 600, Window: time.Hour, Burst: 30}, rl.InvestmentDetect)
	assert.Equal(t, rl.AutoCategorize, rl.InvestmentDetect,
		"as rotas de escrita em massa sobem juntas no perfil frouxo")
	assert.Equal(t, rl.TransferDetect, rl.InvestmentDetect)

	assert.Equal(t, padrao.InvestmentDetect.Window, rl.InvestmentDetect.Window,
		"o perfil de teste eleva o TETO, não estica o tempo")
	assert.GreaterOrEqual(t, rl.InvestmentDetect.Requests, padrao.InvestmentDetect.Requests,
		"o perfil de teste não pode APERTAR um limite")
	assert.Positive(t, rl.InvestmentDetect.Burst)
	assert.Less(t, rl.InvestmentDetect.Burst, rl.InvestmentDetect.Requests,
		"rota cara não pode gastar a cota inteira de uma vez, nem no perfil de teste")

	// E o principal: o afrouxamento do teste NÃO vazou para produção.
	assert.Equal(t, 60, padrao.InvestmentDetect.Requests)
	assert.Equal(t, 3, padrao.InvestmentDetect.Burst)
}

// O balde chega ao boot: Load() sem variável de perfil entrega os padrões, e a
// regra nova não pode ficar zerada no caminho que produção usa.
func TestInvestmentDetectChegaAoBoot(t *testing.T) {
	cfg, err := config.LoadFrom(baseEnv())
	require.NoError(t, err)

	assert.Equal(t, 60, cfg.RateLimits.InvestmentDetect.Requests)
	assert.Equal(t, time.Hour, cfg.RateLimits.InvestmentDetect.Window)
	assert.Equal(t, 3, cfg.RateLimits.InvestmentDetect.Burst)
}
