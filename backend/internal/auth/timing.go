package auth

import (
	"context"
	"time"
)

// padTo segura a resposta até que tenha passado ao menos d desde start.
//
// Por que isto existe (§3.12 e risco 4 da §9 da spec 0001): os grupos A–F
// exigem respostas indistinguíveis. Corpo e status iguais não bastam — o
// TEMPO é um canal lateral. "E-mail existe" faz uma consulta a mais e um
// Argon2id; "e-mail não existe" faz menos trabalho e responde antes. Sem
// normalização, dá para enumerar a base inteira com um cronômetro.
//
// O piso é configurável (AUTH_MIN_RESPONSE_TIME, padrão 300 ms) e precisa
// ficar acima do pior caso normal do endpoint. Se a operação demorar MAIS que
// o piso, não há o que fazer aqui — é por isso que o envio de e-mail é
// assíncrono (D7): SMTP dentro do request estouraria qualquer piso razoável.
//
// Respeita o cancelamento do contexto: cliente que desiste não deixa
// goroutine dormindo (risco 9 da §9).
func padTo(ctx context.Context, start time.Time, d time.Duration) {
	if d <= 0 {
		return
	}
	remaining := d - time.Since(start)
	if remaining <= 0 {
		return
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
