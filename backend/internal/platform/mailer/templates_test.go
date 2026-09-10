package mailer_test

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/mailer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enfileirar roda fn contra um Notifier real e devolve o que sairia no envio.
func enfileirar(t *testing.T, fn func(n *mailer.Notifier)) []mailer.Message {
	t.Helper()

	spy := &spyMailer{}
	q := mailer.NewQueue(spy, logging.Discard(), mailer.QueueOptions{Size: 8, Workers: 1})
	fn(mailer.NewNotifier(q, "nao-responda@homefinance.test", "HomeFinance"))
	require.NoError(t, q.Close(context.Background()))

	spy.mu.Lock()
	defer spy.mu.Unlock()
	out := make([]mailer.Message, len(spy.recebido))
	copy(out, spy.recebido)
	return out
}

// Achado ALTA-2: a mensagem de PRIMEIRO CONTATO não tem por onde receber
// nome. Este teste é o contrato — se alguém reintroduzir o parâmetro, ele
// para de compilar; enquanto isso, garante a saudação neutra.
func TestMensagemDeVerificacaoNaoTemNome(t *testing.T) {
	t.Parallel()

	msgs := enfileirar(t, func(n *mailer.Notifier) {
		n.EnqueueVerificationCode("vitima@banco.test", "123456", 15*time.Minute)
	})
	require.Len(t, msgs, 1)

	assert.Empty(t, msgs[0].ToName)
	assert.True(t, strings.HasPrefix(msgs[0].Body, "Olá,\n"),
		"a saudação precisa ser neutra: %q", msgs[0].Body[:20])
}

// safeDisplayName é defesa em profundidade nos caminhos que AINDA levam nome
// (conta verificada). Testado pela API pública, que é como ele é usado.
func TestNomeExibidoEhSaneadoELimitado(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome     string
		entrada  string
		esperado string
	}{
		{"nome normal passa inteiro", "Bruno Blanck", "Bruno Blanck"},
		{"espaços colapsam", "  Bruno   Blanck  ", "Bruno Blanck"},
		{"tab e vertical tab somem", "Bruno\tBlanck\v!", "BrunoBlanck!"},
		{"caractere de formatação some", "Bru​no", "Bruno"},
		{"só espaço vira vazio", "   ", ""},
		{"sintaxe de endereço some", `Suporte <seguranca@banco-falso.test>, Equipe`, "Suporte seguranca banco-falso.test Equipe"},
		{"aspas somem", `Ana "A" Silva`, "Ana A Silva"},
	}

	for _, cs := range casos {
		t.Run(cs.nome, func(t *testing.T) {
			t.Parallel()

			msgs := enfileirar(t, func(n *mailer.Notifier) {
				n.EnqueueAccountExistsNotice("bruno@exemplo.test", cs.entrada)
			})
			require.Len(t, msgs, 1)
			assert.Equal(t, cs.esperado, msgs[0].ToName)
		})
	}
}

func TestNomeExibidoTemTeto(t *testing.T) {
	t.Parallel()

	// 120 runas é o máximo que a borda aceita em "name"; o cabeçalho corta
	// bem antes, para nenhum nome virar parágrafo.
	longo := strings.Repeat("a", 120)

	msgs := enfileirar(t, func(n *mailer.Notifier) {
		n.EnqueuePasswordResetCode("bruno@exemplo.test", longo, "123456", 15*time.Minute)
	})
	require.Len(t, msgs, 1)

	assert.LessOrEqual(t, utf8.RuneCountInString(msgs[0].ToName), 60)
	assert.NotEmpty(t, msgs[0].ToName)

	// E o cabeçalho montado continua válido.
	bruto, err := msgs[0].Build(time.Now(), "id")
	require.NoError(t, err)
	assert.Contains(t, string(bruto), "To: ")
}

// Achado 4 da re-revisão: mesmo que um nome chegue com sintaxe de endereço,
// o cabeçalho montado não pode ganhar um destinatário aparente a mais.
func TestCabecalhoToNaoEhForjavelPeloNome(t *testing.T) {
	t.Parallel()

	const forja = `Suporte HomeFinance <seguranca@banco-falso.test>, Equipe`

	msgs := enfileirar(t, func(n *mailer.Notifier) {
		n.EnqueueAccountExistsNotice("vitima@banco.test", forja)
	})
	require.Len(t, msgs, 1)

	bruto, err := msgs[0].Build(time.Now(), "id")
	require.NoError(t, err)

	linha := ""
	for _, l := range strings.Split(string(bruto), "\r\n") {
		if strings.HasPrefix(l, "To: ") {
			linha = l
			break
		}
	}
	require.NotEmpty(t, linha)

	assert.NotContains(t, linha, "seguranca@banco-falso.test",
		"endereço forjado apareceu no cabeçalho To: %s", linha)
	assert.True(t, strings.HasSuffix(linha, "<vitima@banco.test>"),
		"o To: precisa terminar no destinatário real: %s", linha)
	assert.Equal(t, 1, strings.Count(linha, "@"),
		"só pode haver um endereço no To: %s", linha)
}

// Contraprova de que o teto não corta acentuação pela metade (byte a byte).
func TestNomeExibidoCortaPorRunaNaoPorByte(t *testing.T) {
	t.Parallel()

	longo := strings.Repeat("á", 100)

	msgs := enfileirar(t, func(n *mailer.Notifier) {
		n.EnqueueAccountExistsNotice("bruno@exemplo.test", longo)
	})
	require.Len(t, msgs, 1)

	assert.True(t, utf8.ValidString(msgs[0].ToName), "corte por byte quebraria o UTF-8")
	assert.Equal(t, 60, utf8.RuneCountInString(msgs[0].ToName))
}
