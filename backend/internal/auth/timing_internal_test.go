package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// padTo é o que sustenta a uniformidade de TEMPO da §3.12 — corpo e status
// iguais não bastam quando o relógio denuncia a diferença de trabalho.
func TestPadToEsperaOPiso(t *testing.T) {
	t.Parallel()

	inicio := time.Now()
	padTo(context.Background(), inicio, 80*time.Millisecond)
	decorrido := time.Since(inicio)

	assert.GreaterOrEqual(t, decorrido, 75*time.Millisecond)
	assert.Less(t, decorrido, 2*time.Second)
}

func TestPadToNaoEsperaQuandoJaPassou(t *testing.T) {
	t.Parallel()

	inicio := time.Now().Add(-time.Second)
	comeco := time.Now()
	padTo(context.Background(), inicio, 50*time.Millisecond)

	assert.Less(t, time.Since(comeco), 20*time.Millisecond)
}

func TestPadToComPisoZeroNaoEspera(t *testing.T) {
	t.Parallel()

	comeco := time.Now()
	padTo(context.Background(), time.Now(), 0)
	padTo(context.Background(), time.Now(), -time.Second)

	assert.Less(t, time.Since(comeco), 20*time.Millisecond)
}

// Risco 9 da §9: cliente que desiste não pode deixar goroutine dormindo.
func TestPadToRespeitaCancelamento(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	comeco := time.Now()
	padTo(ctx, time.Now(), 5*time.Second)

	assert.Less(t, time.Since(comeco), time.Second, "o contexto cancelado precisa liberar a espera")
}
