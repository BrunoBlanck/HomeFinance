package mailer

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Notifier traduz eventos do domínio auth em mensagens e as enfileira.
//
// Implementa auth.Mailer por correspondência estrutural: o pacote auth não
// importa mailer, e mailer não importa auth. É a fronteira do ADR-004 —
// domínio no núcleo, plataforma na borda.
type Notifier struct {
	queue    *Queue
	from     string
	fromName string
}

// NewNotifier monta o notificador.
func NewNotifier(q *Queue, from, fromName string) *Notifier {
	return &Notifier{queue: q, from: from, fromName: fromName}
}

// EnqueueVerificationCode enfileira o código de confirmação de cadastro.
//
// NÃO recebe nome, e não é esquecimento (achado ALTA-2 da revisão): esta é a
// única mensagem que sai para um endereço que ninguém provou possuir, a
// pedido de quem chamou POST /auth/register. Se o nome do corpo da
// requisição virasse "To:" ou saudação, o produto entregaria texto escolhido
// pelo atacante, com SPF/DKIM/DMARC válidos do domínio real, para o endereço
// que ele escolhesse — phishing com a reputação do HomeFinance.
//
// Saudação neutra até o endereço estar verificado.
func (n *Notifier) EnqueueVerificationCode(to, code string, ttl time.Duration) {
	n.queue.Enqueue(Message{
		From:     n.from,
		FromName: n.fromName,
		To:       to,
		ToName:   "",
		Subject:  "Seu código de confirmação do HomeFinance",
		Body:     verificationBody(code, ttl),
	})
}

// EnqueuePasswordResetCode enfileira o código de redefinição de senha.
//
// Aqui o nome é aceito porque este caminho só existe para conta JÁ
// VERIFICADA: quem recebe provou posse da caixa, e o nome é o dele. Ainda
// assim passa por safeDisplayName — o nome pôde ser sobrescrito enquanto a
// conta estava pendente (D3), então limitamos tamanho e caracteres.
func (n *Notifier) EnqueuePasswordResetCode(to, name, code string, ttl time.Duration) {
	name = safeDisplayName(name)
	n.queue.Enqueue(Message{
		From:     n.from,
		FromName: n.fromName,
		To:       to,
		ToName:   name,
		Subject:  "Seu código para redefinir a senha do HomeFinance",
		Body:     passwordResetBody(name, code, ttl),
	})
}

// EnqueueAccountExistsNotice avisa que alguém tentou criar conta com um
// e-mail já cadastrado.
//
// É o outro lado do "registro responde sempre 202" (D3): quem já tem conta
// precisa saber da tentativa, e quem não tem não descobre nada — a resposta
// HTTP é idêntica nos dois casos.
// O nome usado é o da conta EXISTENTE E VERIFICADA — nunca o que veio na
// requisição de cadastro que disparou o aviso.
func (n *Notifier) EnqueueAccountExistsNotice(to, name string) {
	name = safeDisplayName(name)
	n.queue.Enqueue(Message{
		From:     n.from,
		FromName: n.fromName,
		To:       to,
		ToName:   name,
		Subject:  "Tentativa de cadastro com o seu e-mail no HomeFinance",
		Body:     accountExistsBody(name),
	})
}

// maxDisplayName limita o nome exibido em cabeçalho e saudação.
const maxDisplayName = 60

// sintaxeDeEndereco são os caracteres que dão significado estrutural a um
// cabeçalho de endereço (RFC 5322). Num nome de exibição eles não têm uso
// legítimo e, soltos, permitem forjar destinatário aparente.
const sintaxeDeEndereco = `<>@,;:"\`

// safeDisplayName reduz o nome ao que é seguro imprimir numa mensagem.
//
// Defesa em profundidade do achado ALTA-2: mesmo nos caminhos em que o nome
// pertence a uma conta verificada, ele foi digitado por alguém — e pôde ser
// sobrescrito por terceiro enquanto a conta estava pendente (D3). Cortamos
// controles (inclusive os que o CR/LF-check não pega, como TAB e caracteres
// de formatação), a sintaxe de endereço e o tamanho, para que nenhum nome
// vire parágrafo nem destinatário.
func safeDisplayName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if strings.ContainsRune(sintaxeDeEndereco, r) {
			// Vira espaço, não some: descartar juntaria palavras vizinhas e
			// deixaria o nome do titular irreconhecível.
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}

	out := strings.Join(strings.Fields(b.String()), " ")
	if utf8.RuneCountInString(out) > maxDisplayName {
		out = string([]rune(out)[:maxDisplayName])
		out = strings.TrimSpace(out)
	}
	return out
}

func greeting(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Olá,"
	}
	return "Olá, " + name + ","
}

func minutesOf(ttl time.Duration) int {
	m := int(ttl.Minutes())
	if m < 1 {
		m = 1
	}
	return m
}

func verificationBody(code string, ttl time.Duration) string {
	return fmt.Sprintf(`%s

Seu código de confirmação do HomeFinance é:

    %s

Ele vale por %d minutos e só pode ser usado uma vez.

Se não foi você quem pediu, ignore esta mensagem: sem o código, a conta não é ativada.

Nunca compartilhe este código. A equipe do HomeFinance jamais vai pedi-lo por telefone, e-mail ou mensagem.

--
HomeFinance
`, greeting(""), code, minutesOf(ttl))
}

func passwordResetBody(name, code string, ttl time.Duration) string {
	return fmt.Sprintf(`%s

Recebemos um pedido para redefinir a senha da sua conta do HomeFinance.

Seu código é:

    %s

Ele vale por %d minutos e só pode ser usado uma vez.

Se não foi você quem pediu, ignore esta mensagem — sua senha continua a mesma. Ao redefinir a senha, todas as sessões abertas são encerradas.

Nunca compartilhe este código.

--
HomeFinance
`, greeting(name), code, minutesOf(ttl))
}

func accountExistsBody(name string) string {
	return fmt.Sprintf(`%s

Alguém tentou criar uma conta no HomeFinance com este endereço de e-mail, que já está cadastrado.

Se foi você, é só entrar normalmente com sua senha. Se esqueceu a senha, use a opção "Esqueci minha senha".

Se não foi você, não precisa fazer nada: nenhuma conta nova foi criada e nada mudou na sua.

--
HomeFinance
`, greeting(name))
}
