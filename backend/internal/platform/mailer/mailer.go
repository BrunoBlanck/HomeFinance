// Package mailer envia e-mail transacional.
//
// ADR-009: a implementação de console escreve o conteúdo no terminal e NÃO
// pode existir em produção — a config falha no boot se APP_ENV=production com
// MAILER=console.
package mailer

import "context"

// Mailer entrega uma mensagem.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
	// Name identifica a implementação no log de boot.
	Name() string
}
