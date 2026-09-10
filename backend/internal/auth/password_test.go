package auth_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newHasher(t *testing.T) *auth.PasswordHasher {
	t.Helper()
	h, err := auth.NewPasswordHasher(auth.Argon2Params{
		MemoryKiB:     auth.MinArgon2MemoryKiB,
		Iterations:    auth.MinArgon2Iterations,
		Parallelism:   auth.MinArgon2Parallelism,
		MaxConcurrent: 4,
	})
	require.NoError(t, err)
	return h
}

func TestHashEVerify(t *testing.T) {
	t.Parallel()

	h := newHasher(t)
	ctx := t.Context()
	const senha = "uma-senha-bem-longa-2026"

	encoded, err := h.Hash(ctx, senha)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=2$"))
	assert.NotContains(t, encoded, senha)
	assert.NoError(t, h.Verify(ctx, senha, encoded))
	assert.ErrorIs(t, h.Verify(ctx, "outra-senha-qualquer", encoded), auth.ErrPasswordMismatch)
}

// Salt aleatório: a mesma senha nunca produz o mesmo registro, o que impede
// tabela arco-íris e revela quem repetiu senha.
func TestHashUsaSaltAleatorio(t *testing.T) {
	t.Parallel()

	h := newHasher(t)
	ctx := t.Context()

	a, err := h.Hash(ctx, "mesma-senha-para-os-dois")
	require.NoError(t, err)
	b, err := h.Hash(ctx, "mesma-senha-para-os-dois")
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
	assert.NoError(t, h.Verify(ctx, "mesma-senha-para-os-dois", a))
	assert.NoError(t, h.Verify(ctx, "mesma-senha-para-os-dois", b))
}

// docs/SEGURANCA.md §1 fixa o piso: parâmetro abaixo do mínimo é ELEVADO,
// nunca aceito.
func TestParametrosAbaixoDoMinimoSaoElevados(t *testing.T) {
	t.Parallel()

	h, err := auth.NewPasswordHasher(auth.Argon2Params{
		MemoryKiB: 1024, Iterations: 1, Parallelism: 1, MaxConcurrent: 0,
	})
	require.NoError(t, err)

	p := h.Params()
	assert.Equal(t, auth.MinArgon2MemoryKiB, p.MemoryKiB)
	assert.Equal(t, auth.MinArgon2Iterations, p.Iterations)
	assert.Equal(t, auth.MinArgon2Parallelism, p.Parallelism)
	assert.GreaterOrEqual(t, p.MaxConcurrent, 1)
}

func TestVerifyRejeitaHashMalformado(t *testing.T) {
	t.Parallel()

	h := newHasher(t)
	ctx := t.Context()

	invalidos := []string{
		"",
		"texto-solto",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=16$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=0,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=99999999,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2E$",
	}
	for _, encoded := range invalidos {
		assert.ErrorIs(t, h.Verify(ctx, "qualquer-senha", encoded), auth.ErrInvalidHash, "deveria rejeitar %q", encoded)
	}
}

// Grupo E da §3.12: quando o e-mail não existe, o login precisa gastar o
// MESMO tempo de CPU. Se o dummy fosse muito mais rápido, o cronômetro
// separaria "conta existe" de "conta não existe".
//
// A medição é INTERCALADA e comparada por MEDIANA: rodando junto com o resto
// da suíte (e com -race), a média de tempo de parede é dominada por ruído de
// escalonamento. Intercalar cancela a deriva de carga, e a mediana descarta
// picos isolados. A banda é folgada de propósito — o que este teste precisa
// pegar é a regressão real (remover o hash-isca deixaria o caminho
// "inexistente" ordens de grandeza mais rápido, não 30% mais rápido).
func TestVerifyDummyTemCustoComparavel(t *testing.T) {
	h := newHasher(t)
	ctx := t.Context()

	encoded, err := h.Hash(ctx, "senha-real-do-usuario")
	require.NoError(t, err)

	real, dummy := medianasIntercaladas(7,
		func() { _ = h.Verify(ctx, "senha-errada-qualquer", encoded) },
		func() { h.VerifyDummy(ctx, "senha-errada-qualquer") },
	)

	razao := float64(dummy) / float64(real)
	t.Logf("verify=%v dummy=%v razão=%.2f", real, dummy, razao)
	assert.Greater(t, razao, 0.2, "o hash-isca precisa custar o mesmo que uma verificação real")
	assert.Less(t, razao, 5.0)
}

// medianasIntercaladas roda a e b alternadamente e devolve a mediana de cada
// um. Alternar é o que torna a comparação imune à variação de carga da
// máquina durante o teste.
func medianasIntercaladas(amostras int, a, b func()) (time.Duration, time.Duration) {
	medir := func(fn func()) time.Duration {
		inicio := time.Now()
		fn()
		return time.Since(inicio)
	}

	da := make([]time.Duration, 0, amostras)
	db := make([]time.Duration, 0, amostras)

	// Aquecimento: a primeira execução paga alocação e cache frio.
	a()
	b()

	for range amostras {
		da = append(da, medir(a))
		db = append(db, medir(b))
	}
	slices.Sort(da)
	slices.Sort(db)
	return da[len(da)/2], db[len(db)/2]
}

// D15: o semáforo é defesa contra exaustão de memória, não afinação de
// performance. Com 64 MiB por verificação, concorrência ilimitada mata o
// processo.
func TestSemaforoLimitaConcorrencia(t *testing.T) {
	t.Parallel()

	h, err := auth.NewPasswordHasher(auth.Argon2Params{MaxConcurrent: 2})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.Hash(t.Context(), "senha-concorrente-2026")
		}()
	}
	wg.Wait()
	// Sem deadlock e sem corrida: o -race é quem julga este teste.
}

func TestHashRespeitaCancelamentoDoContexto(t *testing.T) {
	t.Parallel()

	h, err := auth.NewPasswordHasher(auth.Argon2Params{MaxConcurrent: 1})
	require.NoError(t, err)

	// Ocupa a única vaga.
	liberar := make(chan struct{})
	ocupado := make(chan struct{})
	go func() {
		close(ocupado)
		_, _ = h.Hash(context.Background(), "ocupa-a-vaga")
		<-liberar
	}()
	<-ocupado

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Com a vaga possivelmente ocupada, um contexto já cancelado não pode
	// ficar pendurado.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = h.Hash(ctx, "nao-deve-bloquear")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Hash ignorou o cancelamento do contexto")
	}
	close(liberar)
}

func TestNeedsRehash(t *testing.T) {
	t.Parallel()

	h := newHasher(t)
	encoded, err := h.Hash(t.Context(), "senha-atual-do-usuario")
	require.NoError(t, err)

	assert.False(t, h.NeedsRehash(encoded))
	assert.True(t, h.NeedsRehash("lixo"))
	assert.True(t, h.NeedsRehash("$argon2id$v=19$m=16384,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA"))
}
