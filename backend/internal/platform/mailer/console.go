package mailer

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Console escreve a mensagem inteira num io.Writer (por padrão os.Stdout).
//
// DECISÃO DELIBERADA, e o revisor de segurança precisa vê-la explicitamente:
// esta implementação IMPRIME O CÓDIGO DE 6 DÍGITOS. É o que torna o fluxo
// testável em desenvolvimento sem servidor SMTP (critério de aceite 10 da
// spec 0001).
//
// Por isso ela escreve num writer PRÓPRIO e NÃO no *slog.Logger da aplicação:
//
//   - o log estruturado é o que vai para arquivo, agregador e retenção longa,
//     e nele nada sensível pode entrar (critério de aceite 16);
//   - a config falha no boot se APP_ENV=production com MAILER=console, então
//     este caminho não existe em produção (D8).
type Console struct {
	mu sync.Mutex
	w  io.Writer
}

// NewConsole monta o mailer de desenvolvimento.
func NewConsole(w io.Writer) *Console { return &Console{w: w} }

// Name identifica a implementação.
func (c *Console) Name() string { return "console" }

// Send imprime a mensagem.
func (c *Console) Send(_ context.Context, msg Message) error {
	// A validação anti-injeção roda também aqui: assim o mailer de console
	// tem exatamente o mesmo contrato do de produção, e um teste que passa em
	// dev não esconde um problema que só apareceria no SMTP.
	if err := ensureNoInjection(msg.From, msg.FromName, msg.To, msg.ToName, msg.Subject); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("\n=========== E-MAIL (mailer de desenvolvimento) ===========\n")
	b.WriteString(fmt.Sprintf("Para:    %s <%s>\n", msg.ToName, msg.To))
	b.WriteString(fmt.Sprintf("De:      %s <%s>\n", msg.FromName, msg.From))
	b.WriteString(fmt.Sprintf("Assunto: %s\n", msg.Subject))
	b.WriteString("----------------------------------------------------------\n")
	b.WriteString(msg.Body)
	b.WriteString("\n==========================================================\n")

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := io.WriteString(c.w, b.String()); err != nil {
		return fmt.Errorf("escrevendo e-mail no console: %w", err)
	}
	return nil
}
