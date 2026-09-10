package config

import "time"

// Rule descreve um limite de taxa: Requests requisições por Window, com
// estouro (burst) igual a Requests.
type Rule struct {
	Requests int
	Window   time.Duration
}

// RateLimits reúne os limites da §7 da spec 0001. Todo 429 devolve
// Retry-After (docs/SEGURANCA.md §5).
type RateLimits struct {
	// Por IP.
	Global         Rule
	Login          Rule
	Register       Rule
	ResendCode     Rule
	ForgotPassword Rule
	VerifyEmail    Rule
	ResetPassword  Rule
	Refresh        Rule
	Health         Rule

	// Por conta. A chave é o HMAC do e-mail com o pepper: o e-mail em claro
	// nunca entra no mapa do limitador (§7 da spec 0001).
	LoginPerAccount          Rule
	ForgotPasswordPerAccount Rule
	// RegisterMailPerAccount é o teto de MENSAGENS de verificação disparadas
	// pelo registro, por endereço (§1.1: rate limit por IP e por conta).
	//
	// Ele limita o ENVIO, não a requisição: o e-mail do cadastro não tem
	// prova de posse, e um 429 por endereço deixaria qualquer um trancar o
	// cadastro alheio.
	//
	// INVARIANTE: Requests tem de ser MAIOR que Register.Requests (o teto por
	// IP). Ver DefaultRateLimits para o porquê — e o teste que sustenta.
	RegisterMailPerAccount Rule
	// AccountExistsNoticePerAccount é o teto do aviso "alguém tentou criar
	// conta com o seu e-mail". Conteúdo fixo: uma vez por dia basta, e mais
	// do que isso é só barulho que um terceiro escolhe mandar.
	AccountExistsNoticePerAccount Rule

	// IdleTTL é o tempo que uma chave ociosa sobrevive no limitador, para o
	// mapa não crescer sem fim (risco 9 da §9 da spec 0001).
	IdleTTL time.Duration
}

// DefaultRateLimits devolve os padrões da §7 da spec 0001.
//
// PISO (decisão de produto de 09/09/2026):
//
//	RegisterMailPerAccount.Requests (10/h)  >  Register.Requests (5/h)
//
// ATENÇÃO: este piso é necessário, mas NÃO é suficiente — e a versão anterior
// deste comentário afirmava que era. Ver o bloco "LIMITE CONHECIDO" abaixo
// antes de confiar nele.
//
// A cota por endereço governa o ENVIO e não pode recusar a requisição (o
// e-mail vem do corpo, sem prova de posse). O efeito colateral é que um
// terceiro consegue QUEIMAR a cota do endereço alheio só registrando: com 3
// mensagens/h por endereço, ~3,7 cadastros por hora de um ÚNICO IP zeravam o
// balde e o dono do endereço não recebia mais nada — negação de cadastro por
// procuração, sem custo nenhum para o atacante.
//
// A mitigação escolhida foi a FOLGA: 10 slots por endereço em vez de 3. Isso
// ajuda o dono no caso comum.
//
// LIMITE CONHECIDO (refutado com PoC em 09/09/2026 — NÃO apague este bloco):
// um único IP AINDA drena a cota. Três rotas gastam este mesmo balde
// (canSendVerificationMail é o funil único), e a reemissão do /auth/login não
// verificado é limitada por LoginPerAccount = 5/15min = 20/h POR CONTA, que
// não limita nada num atacante de um IP só. Soma do que um IP gasta:
// Register 5/h + ResendCode 3/h + login-reissue 20/h ~= 28/h contra 10 slots.
//
// PoC: o atacante registra o endereço primeiro (a linha de users nasce com a
// senha dele) e depois só faz login a cada ~3,5 min; mediram-se 33 mensagens
// disparadas e o dono sem receber nada. Na ordem inversa o atacante queima 8
// dos 10 slots.
//
// INVARIANTE CORRETO, ainda NÃO satisfeito: cota por endereço > soma de tudo
// que um IP consegue gastar dela por hora. Backlog: (a) tirar do login não
// verificado o poder de reemitir mensagem, ou (b) sub-balde reservado para
// pedidos que não sejam login-reissue. Detalhes na §1.1 de docs/SEGURANCA.md.
//
// CUSTO: o orçamento de palpites do OTP por endereço está atado a esta cota —
// 10 códigos/h × 5 tentativas por código = 50 palpites/h contra o espaço de
// 10⁶ (antes eram 15/h). Continua folgado — da ordem de 20 mil horas para
// varrer o espaço — e está registrado na §1.1 de docs/SEGURANCA.md.
func DefaultRateLimits() RateLimits {
	return RateLimits{
		Global:         Rule{Requests: 100, Window: time.Minute},
		Login:          Rule{Requests: 10, Window: time.Minute},
		Register:       Rule{Requests: 5, Window: time.Hour},
		ResendCode:     Rule{Requests: 3, Window: time.Hour},
		ForgotPassword: Rule{Requests: 5, Window: time.Hour},
		VerifyEmail:    Rule{Requests: 10, Window: time.Minute},
		ResetPassword:  Rule{Requests: 10, Window: time.Minute},
		Refresh:        Rule{Requests: 60, Window: time.Minute},
		Health:         Rule{Requests: 60, Window: time.Minute},

		LoginPerAccount:          Rule{Requests: 5, Window: 15 * time.Minute},
		ForgotPasswordPerAccount: Rule{Requests: 3, Window: time.Hour},

		RegisterMailPerAccount:        Rule{Requests: 10, Window: time.Hour},
		AccountExistsNoticePerAccount: Rule{Requests: 1, Window: 24 * time.Hour},

		IdleTTL: time.Hour,
	}
}
