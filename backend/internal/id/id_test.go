package id_test

import (
	"sync"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGeraUUIDv7Canonico(t *testing.T) {
	t.Parallel()

	got := id.New()
	require.Len(t, got, 36, "ID precisa caber em varchar(36)")

	parsed, err := uuid.Parse(got)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), parsed.Version())
	assert.Equal(t, uuid.RFC4122, parsed.Variant())
}

func TestNewEhUnicoSobConcorrencia(t *testing.T) {
	t.Parallel()

	const n = 500
	var (
		mu   sync.Mutex
		seen = make(map[string]struct{}, n)
		wg   sync.WaitGroup
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := id.New()
			mu.Lock()
			defer mu.Unlock()
			seen[v] = struct{}{}
		}()
	}
	wg.Wait()
	assert.Len(t, seen, n)
}

func TestNewEhAproximadamenteOrdenado(t *testing.T) {
	t.Parallel()

	anterior := id.New()
	for range 50 {
		atual := id.New()
		assert.LessOrEqual(t, anterior, atual, "UUID v7 deve crescer com o tempo")
		anterior = atual
	}
}

func TestFixedRepeteOUltimo(t *testing.T) {
	t.Parallel()

	g := id.Fixed("a", "b")
	assert.Equal(t, "a", g())
	assert.Equal(t, "b", g())
	assert.Equal(t, "b", g())
}
