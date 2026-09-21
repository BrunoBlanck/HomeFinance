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

// IsCanonical é a conferência de FORMA que a borda aplica a todo id de recurso
// que entra numa escrita. Ela fecha espaço, controle, aspas, percent-encoding e
// id maior que a coluna `varchar(36)` — o que o `WHERE id = ?` do MSSQL
// (padding ANSI) e do MySQL 8 (`utf8mb4_0900_ai_ci`) casariam mesmo assim.
//
// O que ela NÃO fecha, de propósito: a caixa trocada, que é forma canônica
// válida. Essa metade é fechada gravando o `.ID` que veio do banco.
func TestIsCanonicalAceitaSoAFormaDeTrintaESeisComHifensNoLugar(t *testing.T) {
	t.Parallel()

	validos := map[string]string{
		"minúsculo":        "0199e4d0-7c3a-7e21-9f4b-3a2b1c0d9e8f",
		"maiúsculo":        "0199E4D0-7C3A-7E21-9F4B-3A2B1C0D9E8F",
		"caixa misturada":  "0199e4d0-7C3A-7e21-9F4B-3a2b1c0d9e8f",
		"o que New() gera": id.New(),
		"só dígitos":       "00000000-0000-7000-8000-000000000001",
	}
	for nome, v := range validos {
		t.Run("aceita/"+nome, func(t *testing.T) {
			t.Parallel()
			assert.True(t, id.IsCanonical(v), "%q", v)
		})
	}

	invalidos := map[string]string{
		"vazio":                             "",
		"curto":                             "acc-1",
		"espaço à direita":                  "0199e4d0-7c3a-7e21-9f4b-3a2b1c0d9e8f ",
		"espaço à esquerda":                 " 0199e4d0-7c3a-7e21-9f4b-3a2b1c0d9e8f",
		"sem hífen":                         "0199e4d07c3a7e219f4b3a2b1c0d9e8f",
		"hífen fora do lugar":               "0199e4d0-7c3a-7e21-9f4b3-a2b1c0d9e8",
		"caractere não hex":                 "0199e4d0-7c3a-7e21-9f4b-3a2b1c0d9e8g",
		"aspas":                             "0199e4d0-7c3a-7e21-9f4b-3a2b1c0d9e8'",
		"com chaves (uuid.Parse aceitaria)": "{0199e4d0-7c3a-7e21-9f4b-3a2b1c0d9e8f}",
		"urn (uuid.Parse aceitaria)":        "urn:uuid:0199e4d0-7c3a-7e21-9f4b-3a2b1c0d9e8f",
		"longo demais":                      "0199e4d0-7c3a-7e21-9f4b-3a2b1c0d9e8ff",
		"nulo embutido":                     "0199e4d0-7c3a-7e21-9f4b-3a2b1c0d9e\x00f",
	}
	for nome, v := range invalidos {
		t.Run("recusa/"+nome, func(t *testing.T) {
			t.Parallel()
			assert.False(t, id.IsCanonical(v), "%q", v)
		})
	}
}
