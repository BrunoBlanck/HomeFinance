package gormstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeAttempt grava uma tentativa de cadastro. O token vem por parâmetro
// para o teste conseguir provar o escopo (token de um NÃO abre o outro).
func (s *store) makeAttempt(t *testing.T, ctx context.Context, email, token string, issued bool) *auth.RegistrationAttempt {
	t.Helper()

	att := &auth.RegistrationAttempt{
		ID:           s.nextID("ra"),
		Email:        email,
		UserID:       s.nextID("u"),
		Name:         "Bruno Blanck",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		TokenHash:    auth.HashRegistrationToken(token),
		CodeHash:     "hash-do-codigo-" + token[:8],
		ExpiresAt:    now().Add(15 * time.Minute),
		CreatedAt:    now(),
		UpdatedAt:    now(),
	}
	if issued {
		emitido := now()
		att.CodeIssuedAt = &emitido
	}
	require.NoError(t, s.attempts.Create(ctx, att))
	return att
}

// tokenDeTeste devolve um token bem formado e determinístico.
func tokenDeTeste(sufixo byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = '0'
	}
	b[63] = sufixo
	return string(b)
}

// O ESCOPO é a defesa: o código só é encontrado com o token da própria
// tentativa. Duas tentativas para o MESMO endereço não se enxergam.
func TestRegistrationAttemptEscopoPorToken(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()

		tokenA := tokenDeTeste('a')
		tokenB := tokenDeTeste('b')
		a := s.makeAttempt(t, ctx, email, tokenA, true)
		b := s.makeAttempt(t, ctx, email, tokenB, true)

		achadaA, err := s.attempts.LiveByToken(ctx, email, auth.HashRegistrationToken(tokenA), now())
		require.NoError(t, err)
		assert.Equal(t, a.ID, achadaA.ID)

		achadaB, err := s.attempts.LiveByToken(ctx, email, auth.HashRegistrationToken(tokenB), now())
		require.NoError(t, err)
		assert.Equal(t, b.ID, achadaB.ID)
		assert.NotEqual(t, achadaA.ID, achadaB.ID)

		// Token que não existe: nada.
		_, err = s.attempts.LiveByToken(ctx, email, auth.HashRegistrationToken(tokenDeTeste('c')), now())
		assert.ErrorIs(t, err, auth.ErrNotFound)

		// Token certo, e-mail de outra pessoa: nada.
		_, err = s.attempts.LiveByToken(ctx, s.nextEmail(), auth.HashRegistrationToken(tokenA), now())
		assert.ErrorIs(t, err, auth.ErrNotFound)
	})
}

// Tentativa cujo código NUNCA foi enviado não é validável (achado ALTA-2).
//
// O código nasce gravado mesmo quando o cooldown ou a cota seguram a
// mensagem — é o que preserva o token de quem pediu o cadastro. Se ele fosse
// validável, cada POST /auth/register criaria um alvo de chute novo sem
// gastar mensagem, e o teto de envio por endereço deixaria de limitar
// quantos códigos existem para adivinhar (docs/SEGURANCA.md §1.1).
func TestRegistrationAttemptSemEnvioNaoEhValidavel(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		token := tokenDeTeste('a')

		att := s.makeAttempt(t, ctx, email, token, false)
		require.Nil(t, att.CodeIssuedAt)

		_, err := s.attempts.LiveByToken(ctx, email, auth.HashRegistrationToken(token), now())
		assert.ErrorIs(t, err, auth.ErrNotFound,
			"código nunca enviado por e-mail não pode ser encontrado para validação")

		// ByToken continua enxergando: é o que permite ao dono do token pedir
		// a emissão de verdade pelo reenvio.
		guardada, err := s.attempts.ByToken(ctx, email, auth.HashRegistrationToken(token))
		require.NoError(t, err)
		assert.Nil(t, guardada.CodeIssuedAt)
		assert.False(t, guardada.CodeIssued(), "o helper de domínio confere o ponteiro")

		// Emitida de verdade (o que RotateCode faz quando a mensagem sai), a
		// mesma tentativa passa a valer.
		emissao := now().Add(time.Minute)
		ok, err := s.attempts.RotateCode(ctx, att.ID, "hash-novo", emissao, emissao.Add(15*time.Minute))
		require.NoError(t, err)
		require.True(t, ok)

		viva, err := s.attempts.LiveByToken(ctx, email, auth.HashRegistrationToken(token), emissao)
		require.NoError(t, err)
		assert.Equal(t, att.ID, viva.ID)
		require.NotNil(t, viva.CodeIssuedAt)
		assert.True(t, viva.CodeIssued())
	})
}

// A SENTINELA DE "NUNCA EMITIDO" É NULL NO BANCO — não o zero de time.Time.
//
// Este teste olha a COLUNA, e não o campo em Go, porque é exatamente aí que
// a versão anterior quebrava: o zero de time.Time chega ao MySQL como
// '0000-00-00 00:00:00' e o sql_mode padrão do MySQL 8 (STRICT_TRANS_TABLES
// + NO_ZERO_DATE) recusa o INSERT com o erro 1292. A linha não nascia, o
// POST /auth/register respondia 500 para "e-mail livre" e "e-mail pendente"
// enquanto "e-mail já verificado" respondia 202 (oráculo de enumeração,
// grupo A da §3.12), e o invariante do ADR-014 — a tentativa é gravada MESMO
// quando o cooldown ou a cota seguram a mensagem — ia junto.
//
// Enquanto esta asserção valer, nenhuma data mágica volta para a coluna.
func TestRegistrationAttemptCodigoNuncaEmitidoEhNuloNaColuna(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		// Sufixo próprio: o índice ux_reg_attempts_token é GLOBAL, e num
		// Postgres compartilhado dois testes paralelos com o mesmo token
		// colidiriam.
		token := tokenDeTeste('n')
		att := s.makeAttempt(t, ctx, email, token, false)

		var nulas int64
		require.NoError(t, s.db.Gorm().WithContext(ctx).
			Table("registration_attempts").
			Where("id = ? AND code_issued_at IS NULL", att.ID).
			Count(&nulas).Error)
		assert.EqualValues(t, 1, nulas,
			"tentativa sem envio precisa gravar NULL em code_issued_at; data mágica não é portátil (MySQL erro 1292)")

		// E nenhuma linha da tabela pode carregar data anterior a 1970 — que
		// é a forma que o zero de time.Time assumiria em qualquer dialeto.
		var magicas int64
		require.NoError(t, s.db.Gorm().WithContext(ctx).
			Table("registration_attempts").
			Where("code_issued_at IS NOT NULL AND code_issued_at < ?", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)).
			Count(&magicas).Error)
		assert.Zero(t, magicas, "nenhuma data-sentinela pode ser gravada na coluna")

		// Emitido de verdade, o valor é uma data real.
		emissao := now().Add(time.Minute)
		ok, err := s.attempts.RotateCode(ctx, att.ID, "hash-novo", emissao, emissao.Add(15*time.Minute))
		require.NoError(t, err)
		require.True(t, ok)

		depois, err := s.attempts.ByToken(ctx, email, auth.HashRegistrationToken(token))
		require.NoError(t, err)
		require.NotNil(t, depois.CodeIssuedAt)
		assert.WithinDuration(t, emissao, *depois.CodeIssuedAt, time.Second)
	})
}

func TestRegistrationAttemptConsumoEhUnico(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		token := tokenDeTeste('a')
		att := s.makeAttempt(t, ctx, email, token, true)

		ok, err := s.attempts.Consume(ctx, att.ID, now())
		require.NoError(t, err)
		assert.True(t, ok)

		ok, err = s.attempts.Consume(ctx, att.ID, now())
		require.NoError(t, err)
		assert.False(t, ok, "consumir duas vezes precisa falhar na segunda")

		_, err = s.attempts.LiveByToken(ctx, email, auth.HashRegistrationToken(token), now())
		assert.ErrorIs(t, err, auth.ErrNotFound, "tentativa consumida não é mais viva")

		// ByToken continua enxergando: é o que permite pedir outro código.
		morta, err := s.attempts.ByToken(ctx, email, auth.HashRegistrationToken(token))
		require.NoError(t, err)
		assert.NotNil(t, morta.ConsumedAt)
	})
}

func TestRegistrationAttemptExpiradaNaoEhViva(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		token := tokenDeTeste('a')
		s.makeAttempt(t, ctx, email, token, true)

		_, err := s.attempts.LiveByToken(ctx, email, auth.HashRegistrationToken(token), now().Add(16*time.Minute))
		assert.ErrorIs(t, err, auth.ErrNotFound)
	})
}

// O contador precisa subir DENTRO do banco: read-modify-write em Go faria
// duas tentativas simultâneas contarem como uma e furaria o limite de 5.
func TestRegistrationAttemptIncrementaTentativasAtomicamente(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		att := s.makeAttempt(t, ctx, s.nextEmail(), tokenDeTeste('a'), true)

		for esperado := 1; esperado <= 3; esperado++ {
			n, ok, err := s.attempts.IncrementAttempts(ctx, att.ID)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, esperado, n)
		}

		_, err := s.attempts.Consume(ctx, att.ID, now())
		require.NoError(t, err)

		_, ok, err := s.attempts.IncrementAttempts(ctx, att.ID)
		require.NoError(t, err)
		assert.False(t, ok, "tentativa consumida não conta mais")
	})
}

// RotateCode zera o contador: o limite de 5 é POR CÓDIGO. Se atravessasse a
// rotação, cinco chutes errados de um terceiro trancariam o cadastro do dono
// do endereço para sempre.
func TestRegistrationAttemptRotateCodeZeraOContadorEMantemOToken(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		token := tokenDeTeste('a')
		att := s.makeAttempt(t, ctx, email, token, true)

		for range 3 {
			_, _, err := s.attempts.IncrementAttempts(ctx, att.ID)
			require.NoError(t, err)
		}
		_, err := s.attempts.Consume(ctx, att.ID, now())
		require.NoError(t, err)

		depois := now().Add(2 * time.Minute)
		ok, err := s.attempts.RotateCode(ctx, att.ID, "hash-novo", depois, depois.Add(15*time.Minute))
		require.NoError(t, err)
		require.True(t, ok)

		viva, err := s.attempts.LiveByToken(ctx, email, auth.HashRegistrationToken(token), depois)
		require.NoError(t, err)
		assert.Equal(t, "hash-novo", viva.CodeHash)
		assert.Zero(t, viva.Attempts, "o contador é por código")
		assert.Nil(t, viva.ConsumedAt)
		assert.Equal(t, att.PasswordHash, viva.PasswordHash, "a rotação não mexe nas credenciais")
	})
}

func TestRegistrationAttemptLiveByEmailELastCodeIssuedAt(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()

		_, ok, err := s.attempts.LastCodeIssuedAt(ctx, email)
		require.NoError(t, err)
		assert.False(t, ok, "endereço sem tentativa não tem emissão")

		// Uma tentativa SEM envio: code_issued_at nulo. Ela NÃO conta como
		// emissão (não empurra o cooldown de quem nunca recebeu mensagem) e
		// NÃO conta como viva (BAIXA-1: não é validável por ninguém, e
		// contá-la desligava a reemissão do login não verificado para o dono).
		s.makeAttempt(t, ctx, email, tokenDeTeste('a'), false)
		_, ok, err = s.attempts.LastCodeIssuedAt(ctx, email)
		require.NoError(t, err)
		assert.False(t, ok, "tentativa sem envio não conta como emissão")

		vivas, err := s.attempts.LiveByEmail(ctx, email, now(), 5)
		require.NoError(t, err)
		assert.Empty(t, vivas, "tentativa sem código emitido não é utilizável e não conta como viva")

		s.makeAttempt(t, ctx, email, tokenDeTeste('b'), true)
		quando, ok, err := s.attempts.LastCodeIssuedAt(ctx, email)
		require.NoError(t, err)
		require.True(t, ok)
		assert.False(t, quando.IsZero())

		vivas, err = s.attempts.LiveByEmail(ctx, email, now(), 5)
		require.NoError(t, err)
		assert.Len(t, vivas, 1, "só a tentativa com código emitido conta")

		// Endereço realmente disputado: DUAS emitidas.
		s.makeAttempt(t, ctx, email, tokenDeTeste('c'), true)
		vivas, err = s.attempts.LiveByEmail(ctx, email, now(), 2)
		require.NoError(t, err)
		assert.Len(t, vivas, 2, "endereço disputado tem mais de uma tentativa viva")

		// O limite é respeitado: o serviço pede 2 só para saber se é única.
		vivas, err = s.attempts.LiveByEmail(ctx, email, now(), 1)
		require.NoError(t, err)
		assert.Len(t, vivas, 1)
	})
}

// Confirmado o e-mail, NENHUMA tentativa pendente daquele endereço pode
// continuar de pé.
func TestRegistrationAttemptConsumeAllForEmail(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		outro := s.nextEmail()

		s.makeAttempt(t, ctx, email, tokenDeTeste('a'), true)
		s.makeAttempt(t, ctx, email, tokenDeTeste('b'), true)
		s.makeAttempt(t, ctx, outro, tokenDeTeste('c'), true)

		n, err := s.attempts.ConsumeAllForEmail(ctx, email, now())
		require.NoError(t, err)
		assert.EqualValues(t, 2, n)

		vivas, err := s.attempts.LiveByEmail(ctx, email, now(), 5)
		require.NoError(t, err)
		assert.Empty(t, vivas)

		// O endereço de outra pessoa fica intacto.
		vivas, err = s.attempts.LiveByEmail(ctx, outro, now(), 5)
		require.NoError(t, err)
		assert.Len(t, vivas, 1)
	})
}

func TestRegistrationAttemptDeleteExpired(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		s.makeAttempt(t, ctx, email, tokenDeTeste('a'), true)

		// Antes do corte: nada sai.
		n, err := s.attempts.DeleteExpired(ctx, now())
		require.NoError(t, err)
		assert.Zero(t, n)

		n, err = s.attempts.DeleteExpired(ctx, now().Add(time.Hour))
		require.NoError(t, err)
		assert.EqualValues(t, 1, n)
	})
}

// O token é único no banco: duas tentativas jamais compartilham o segredo que
// as separa.
func TestRegistrationAttemptTokenEhUnico(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		token := tokenDeTeste('a')
		s.makeAttempt(t, ctx, s.nextEmail(), token, true)

		emitido := now()
		err := s.attempts.Create(ctx, &auth.RegistrationAttempt{
			ID:           s.nextID("ra"),
			Email:        s.nextEmail(),
			UserID:       s.nextID("u"),
			Name:         "Outro",
			PasswordHash: "hash",
			TokenHash:    auth.HashRegistrationToken(token),
			CodeHash:     "hash",
			CodeIssuedAt: &emitido,
			ExpiresAt:    now().Add(time.Minute),
			CreatedAt:    now(),
			UpdatedAt:    now(),
		})
		assert.Error(t, err, "token repetido tem de bater na unicidade do banco")
	})
}
