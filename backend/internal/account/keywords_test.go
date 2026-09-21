package account_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Palavras-chave de conta (spec 0005 §4.1, critério de aceite 2 e casos de
// abuso da §9 do plano) — o espelho dos testes de categoria. Conta e categoria
// são conjuntos INDEPENDENTES por construção: tabelas, repositórios e índices
// únicos separados (o migrate_test do gormstore prova que a mesma norm cabe
// nas duas tabelas da mesma casa).

func palavras(itens ...string) *[]string { return &itens }

func criarContaComPalavras(t *testing.T, svc *account.Service, casa, nome string, kws ...string) account.View {
	t.Helper()
	entrada := entradaValida()
	entrada.Name = nome
	entrada.Keywords = kws
	v, err := svc.Create(t.Context(), ator(casa), entrada)
	require.NoError(t, err)
	return v
}

// --- serviço: gravação e leitura -------------------------------------------

func TestCriarContaComPalavrasChaveGravaNaOrdemEComOsCamposDoServidor(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	v := criarContaComPalavras(t, svc, minhaCasa, "Nubank", "Nubank", "  NU PAGAMENTOS  ", "Nu Financeira")

	assert.Equal(t, []string{"Nubank", "NU PAGAMENTOS", "Nu Financeira"}, v.Keywords)

	gravadas := repo.palavras[v.ID]
	require.Len(t, gravadas, 3)
	for i, k := range gravadas {
		assert.Equal(t, minhaCasa, k.HouseholdID)
		assert.Equal(t, v.ID, k.AccountID)
		assert.NotEmpty(t, k.ID)
		assert.Equal(t, i, k.Position)
		assert.False(t, k.CreatedAt.IsZero())
	}
	assert.Equal(t, "nu pagamentos", gravadas[1].Norm)
}

func TestCriarContaSemPalavrasChaveDevolveListaVaziaNuncaNula(t *testing.T) {
	t.Parallel()

	svc := novoServico(t, novoRepo())
	v, err := svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	require.NotNil(t, v.Keywords, "o campo é required no contrato: [] e não null")
	assert.Empty(t, v.Keywords)

	lida, err := svc.Get(t.Context(), ator(minhaCasa), v.ID)
	require.NoError(t, err)
	require.NotNil(t, lida.Keywords)
	assert.Empty(t, lida.Keywords)
}

func TestListaDeContasTrazAsPalavrasEmUmaConsulta(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criarContaComPalavras(t, svc, minhaCasa, "Nubank", "nubank")
	criarContaComPalavras(t, svc, minhaCasa, "C6", "c6 bank", "banco c6")
	criarContaComPalavras(t, svc, minhaCasa, "Carteira")
	criarContaComPalavras(t, svc, outraCasa, "Nubank", "nubank da outra casa")

	repo.listagens = 0
	lista, err := svc.List(ctx, ator(minhaCasa), true)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.listagens, "UMA consulta de palavras para a casa inteira, nunca N+1")

	porNome := map[string][]string{}
	for _, c := range lista.Items {
		porNome[c.Name] = c.Keywords
	}
	assert.Equal(t, []string{"nubank"}, porNome["Nubank"])
	assert.Equal(t, []string{"c6 bank", "banco c6"}, porNome["C6"])
	assert.Equal(t, []string{}, porNome["Carteira"])

	corpo, err := json.Marshal(lista)
	require.NoError(t, err)
	assert.NotContains(t, string(corpo), "outra casa")
}

// --- serviço: unicidade por casa (critério 2 da spec 0005) -----------------

func TestMesmaPalavraEmOutraContaDaMesmaCasaEh409ComADona(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	nubank := criarContaComPalavras(t, svc, minhaCasa, "Nubank", "Nubank")

	entrada := entradaValida()
	entrada.Name = "Cartão Nubank"
	entrada.Keywords = []string{"NUBANK"}
	_, err := svc.Create(ctx, ator(minhaCasa), entrada)

	var tomada *account.KeywordTakenError
	require.ErrorAs(t, err, &tomada, "a comparação é pela forma normalizada")
	assert.Equal(t, nubank.ID, tomada.OwnerID, "a dona é a conta da MESMA casa")
	assert.Equal(t, "NUBANK", tomada.Keyword)
	assert.ErrorIs(t, err, account.ErrKeywordTaken)
	assert.NotContains(t, strings.ToLower(err.Error()), "nubank", "a mensagem do erro vai para o log: sem a palavra")

	c6 := criarContaComPalavras(t, svc, minhaCasa, "C6")
	_, err = svc.Update(ctx, ator(minhaCasa), c6.ID, account.UpdateInput{Keywords: palavras("c6", "nubank")})
	require.ErrorAs(t, err, &tomada)
	assert.Equal(t, nubank.ID, tomada.OwnerID)
	assert.Empty(t, repo.palavras[c6.ID], "a lista inteira é recusada: nem 'c6' entra")
}

func TestMesmaPalavraEmContaDeOutraCasaNaoColide(t *testing.T) {
	t.Parallel()

	svc := novoServico(t, novoRepo())
	criarContaComPalavras(t, svc, outraCasa, "Nubank", "nubank")
	v := criarContaComPalavras(t, svc, minhaCasa, "Nubank", "nubank")
	assert.Equal(t, []string{"nubank"}, v.Keywords)
}

func TestReenviarAsPropriasPalavrasDaContaNaoColideConsigoMesma(t *testing.T) {
	t.Parallel()

	svc := novoServico(t, novoRepo())
	v := criarContaComPalavras(t, svc, minhaCasa, "Nubank", "nubank", "nu pagamentos")

	atualizada, err := svc.Update(t.Context(), ator(minhaCasa), v.ID,
		account.UpdateInput{Keywords: palavras("nubank", "nu pagamentos", "roxinho")})
	require.NoError(t, err)
	assert.Equal(t, []string{"nubank", "nu pagamentos", "roxinho"}, atualizada.Keywords)
}

func TestCorridaDecididaPeloIndiceUnicoAindaCitaADonaDaConta(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	vencedora := criarContaComPalavras(t, svc, minhaCasa, "Nubank", "nubank")
	perdedora := criarContaComPalavras(t, svc, minhaCasa, "C6")

	repo.ocultarDonasUmaVez = true
	repo.substituicoes = 0
	_, err := svc.Update(t.Context(), ator(minhaCasa), perdedora.ID, account.UpdateInput{Keywords: palavras("nubank")})

	var tomada *account.KeywordTakenError
	require.ErrorAs(t, err, &tomada)
	assert.Equal(t, vencedora.ID, tomada.OwnerID)
	assert.Empty(t, repo.palavras[perdedora.ID])
	assert.Equal(t, 1, repo.substituicoes, "com a dona encontrada não há segunda tentativa")
}

// A corrida em que a dona SOME: o índice único recusou por uma linha
// concorrente que já não existe quando o serviço reconsulta (a vencedora
// largou a palavra). A colisão desapareceu, e a operação inteira roda uma
// segunda vez — que passa.
func TestCorridaComDonaSumidaNaContaTentaDeNovoUmaVezEPassa(t *testing.T) {
	t.Parallel()

	t.Run("edição", func(t *testing.T) {
		t.Parallel()

		repo := novoRepo()
		svc := novoServico(t, repo)
		c6 := criarContaComPalavras(t, svc, minhaCasa, "C6")

		repo.colisoesForcadas = 1
		v, err := svc.Update(t.Context(), ator(minhaCasa), c6.ID, account.UpdateInput{Keywords: palavras("C6 Bank", "Banco C6")})
		require.NoError(t, err)
		assert.Equal(t, []string{"C6 Bank", "Banco C6"}, v.Keywords)
		assert.Equal(t, 2, repo.substituicoes, "exatamente uma segunda tentativa")
		assert.Equal(t, []string{"C6 Bank", "Banco C6"}, []string{repo.palavras[c6.ID][0].Keyword, repo.palavras[c6.ID][1].Keyword})
	})

	t.Run("criação", func(t *testing.T) {
		t.Parallel()

		// Com rollback: a conta da primeira tentativa não pode sobrar, senão a
		// segunda tomaria ErrNameTaken — coisa que o banco real não faz.
		repo := novoRepo()
		svc := novoServicoComTx(t, repo, txComRollback{repo: repo})

		repo.colisoesForcadas = 1
		entrada := entradaValida()
		entrada.Name = "C6"
		entrada.Keywords = []string{"C6 Bank"}
		v, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
		require.NoError(t, err)
		assert.Equal(t, []string{"C6 Bank"}, v.Keywords)
		assert.Equal(t, 2, repo.substituicoes)

		total, err := repo.CountAll(t.Context(), minhaCasa)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total, "só a conta da tentativa que passou existe")
		assert.Len(t, repo.palavras[v.ID], 1)
	})
}

// A corrida TRIPLA: colide de novo na segunda tentativa e de novo ninguém
// aparece como dona. Não há terceira tentativa, e o 409 sai com a palavra —
// o contrato exige `fields.keyword` — mas sem dona, que o serviço não tem
// como saber (melhor esforço: a primeira palavra enviada).
func TestCorridaTriplaNaContaDevolve409ComAPalavraESemDona(t *testing.T) {
	t.Parallel()

	t.Run("edição", func(t *testing.T) {
		t.Parallel()

		repo := novoRepo()
		svc := novoServico(t, repo)
		c6 := criarContaComPalavras(t, svc, minhaCasa, "C6")

		repo.colisoesForcadas = 2
		_, err := svc.Update(t.Context(), ator(minhaCasa), c6.ID, account.UpdateInput{Keywords: palavras("C6 Bank", "Banco C6")})

		var tomada *account.KeywordTakenError
		require.ErrorAs(t, err, &tomada, "nunca um ErrKeywordTaken cru: o handler precisa da palavra")
		assert.Equal(t, "C6 Bank", tomada.Keyword, "a primeira palavra enviada, como foi digitada")
		assert.Empty(t, tomada.OwnerID, "dona desconhecida fica vazia, nunca inventada")
		assert.ErrorIs(t, err, account.ErrKeywordTaken)
		assert.NotContains(t, strings.ToLower(err.Error()), "c6 bank", "a mensagem pode ir para o log")
		assert.Equal(t, 2, repo.substituicoes, "duas tentativas, nunca uma terceira")
		assert.Empty(t, repo.palavras[c6.ID])
	})

	t.Run("criação", func(t *testing.T) {
		t.Parallel()

		repo := novoRepo()
		svc := novoServicoComTx(t, repo, txComRollback{repo: repo})

		repo.colisoesForcadas = 2
		entrada := entradaValida()
		entrada.Name = "C6"
		entrada.Keywords = []string{"C6 Bank"}
		_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)

		var tomada *account.KeywordTakenError
		require.ErrorAs(t, err, &tomada)
		assert.Equal(t, "C6 Bank", tomada.Keyword)
		assert.Empty(t, tomada.OwnerID)
		assert.Equal(t, 2, repo.substituicoes)

		total, err := repo.CountAll(t.Context(), minhaCasa)
		require.NoError(t, err)
		assert.Zero(t, total, "nenhuma das duas tentativas deixou conta")
	})
}

// --- serviço: tri-estado do PATCH -----------------------------------------

func TestPatchDeContaSemKeywordsNaoMexeVaziaLimpaPresenteSubstitui(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	v := criarContaComPalavras(t, svc, minhaCasa, "Nubank", "nubank", "nu pagamentos")

	nome := "Nubank Conta"
	depois, err := svc.Update(ctx, ator(minhaCasa), v.ID, account.UpdateInput{Name: &nome})
	require.NoError(t, err)
	assert.Equal(t, []string{"nubank", "nu pagamentos"}, depois.Keywords, "ausente não mexe")

	depois, err = svc.Update(ctx, ator(minhaCasa), v.ID, account.UpdateInput{Keywords: palavras("roxinho", "nubank")})
	require.NoError(t, err)
	assert.Equal(t, []string{"roxinho", "nubank"}, depois.Keywords, "presente substitui a lista inteira")

	depois, err = svc.Update(ctx, ator(minhaCasa), v.ID, account.UpdateInput{Keywords: palavras()})
	require.NoError(t, err)
	require.NotNil(t, depois.Keywords)
	assert.Empty(t, depois.Keywords, "vazia limpa")
	assert.Empty(t, repo.palavras[v.ID])

	outra := criarContaComPalavras(t, svc, minhaCasa, "Cartão", "nubank")
	assert.Equal(t, []string{"nubank"}, outra.Keywords, "a palavra ficou livre")
}

// --- serviço: validação da lista -------------------------------------------

func TestListaDePalavrasChaveDaContaEhValidadaSemGravarNada(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	v := criarContaComPalavras(t, svc, minhaCasa, "Nubank")

	vinteEUma := make([]string, 0, account.MaxKeywordsPerOwner+1)
	for i := range account.MaxKeywordsPerOwner + 1 {
		vinteEUma = append(vinteEUma, fmt.Sprintf("banco%02d", i))
	}

	casos := []struct {
		nome   string
		lista  []string
		erro   error
		indice int
	}{
		{"21 itens é recusa, nunca truncamento", vinteEUma, account.ErrTooManyKeywords, -1},
		{"repetida pela forma normalizada", []string{"Nubank", "nubank"}, account.ErrDuplicateKeyword, 1},
		{"caractere fora da allowlist", []string{"<script>"}, account.ErrInvalidKeyword, 0},
		{"só palavra vazia", []string{"de"}, account.ErrInvalidKeyword, 0},
		{"só letras soltas", []string{"c & a"}, account.ErrInvalidKeyword, 0},
		{"41 runas", []string{strings.Repeat("ç", textmatch.MaxKeywordRunes+1)}, account.ErrInvalidKeyword, 0},
		{"o índice aponta o item ruim", []string{"nubank", "c6", "'; DROP TABLE accounts--"}, account.ErrInvalidKeyword, 2},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			_, err := svc.Update(ctx, ator(minhaCasa), v.ID, account.UpdateInput{Keywords: &caso.lista})
			require.ErrorIs(t, err, caso.erro)
			assert.True(t, account.IsValidationError(err))

			var item *account.KeywordValidationError
			if caso.indice < 0 {
				assert.False(t, errors.As(err, &item))
			} else {
				require.ErrorAs(t, err, &item)
				assert.Equal(t, caso.indice, item.Index)
			}
			for _, palavra := range caso.lista {
				if p := strings.TrimSpace(palavra); p != "" {
					assert.NotContains(t, err.Error(), p)
				}
			}
			assert.Empty(t, repo.palavras[v.ID])
		})
	}

	vinte := vinteEUma[:account.MaxKeywordsPerOwner]
	_, err := svc.Update(ctx, ator(minhaCasa), v.ID, account.UpdateInput{Keywords: &vinte})
	require.NoError(t, err)
	assert.Len(t, repo.palavras[v.ID], account.MaxKeywordsPerOwner)
}

// --- serviço: ciclo de vida da dona ---------------------------------------

func TestExcluirContaApagaAsPalavrasELiberaAPalavra(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	v := criarContaComPalavras(t, svc, minhaCasa, "Nubank", "nubank")
	require.NoError(t, svc.Delete(ctx, ator(minhaCasa), v.ID))

	assert.NotNil(t, repo.linhas[v.ID].DeletedAt, "a exclusão da conta é lógica")
	assert.Empty(t, repo.palavras[v.ID], "a das palavras é física, na mesma transação")

	outra := criarContaComPalavras(t, svc, minhaCasa, "Nubank de novo", "nubank")
	assert.Equal(t, []string{"nubank"}, outra.Keywords)
}

func TestArquivarContaMantemAsPalavras(t *testing.T) {
	t.Parallel()

	svc := novoServico(t, novoRepo())
	ctx := t.Context()

	v := criarContaComPalavras(t, svc, minhaCasa, "Nubank", "nubank")

	arquivada, err := svc.Archive(ctx, ator(minhaCasa), v.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"nubank"}, arquivada.Keywords)

	entrada := entradaValida()
	entrada.Name = "Outra"
	entrada.Keywords = []string{"nubank"}
	_, err = svc.Create(ctx, ator(minhaCasa), entrada)
	assert.ErrorIs(t, err, account.ErrKeywordTaken, "arquivada continua dona (§4.1.4)")

	voltou, err := svc.Unarchive(ctx, ator(minhaCasa), v.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"nubank"}, voltou.Keywords)
}

func TestPalavrasDeContaDeOutraCasaNaoSaoEditaveis(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	daOutra := criarContaComPalavras(t, svc, outraCasa, "Nubank", "nubank")

	_, err := svc.Update(t.Context(), ator(minhaCasa), daOutra.ID, account.UpdateInput{Keywords: palavras("sequestrada")})
	assert.ErrorIs(t, err, account.ErrNotFound)
	assert.Equal(t, "nubank", repo.palavras[daOutra.ID][0].Keyword)
}

// --- serviço: auditoria e log ----------------------------------------------

// §4.1.5 da spec 0005: alterar palavras usa o evento que já existe, e a
// palavra NUNCA entra no rastro.
func TestAuditoriaDeContaNaoGuardaAPalavraChave(t *testing.T) {
	t.Parallel()

	auditor := &auditorFake{}
	svc := novoServico(t, novoRepo(), account.WithAudit(auditor))
	ctx := t.Context()

	v := criarContaComPalavras(t, svc, minhaCasa, "Nubank", "nubank")
	_, err := svc.Update(ctx, ator(minhaCasa), v.ID, account.UpdateInput{Keywords: palavras("roxinho")})
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, ator(minhaCasa), v.ID))

	for _, r := range auditor.registros {
		serializado, err := json.Marshal(r)
		require.NoError(t, err)
		assert.NotContains(t, string(serializado), "nubank")
		assert.NotContains(t, string(serializado), "roxinho")
	}
	assert.Equal(t, []string{
		audit.ActionAccountCreated,
		audit.ActionAccountUpdated,
		audit.ActionAccountDeleted,
	}, auditor.acoes(), "nenhum evento novo: os existentes cobrem a edição de palavras")
}

func TestFalhaDeInfraNasPalavrasDaContaNaoEcoaAPalavra(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	repo.erroPalavras = errors.New("banco fora do ar")
	svc := novoServico(t, repo)

	entrada := entradaValida()
	entrada.Keywords = []string{"nubank"}
	_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
	require.Error(t, err)
	assert.NotErrorIs(t, err, account.ErrKeywordTaken)
	assert.False(t, account.IsValidationError(err))
	assert.NotContains(t, err.Error(), "nubank")
}

// --- handler: o contrato --------------------------------------------------

const corpoComPalavras = `{"name":"Nubank","kind":"checking","institution":"nubank",` +
	`"openingBalanceCents":0,"openingDate":"2026-09-01","keywords":["Nubank","nu pagamentos"]}`

func TestPostDeContaSemKeywordsRespondeListaVaziaEComKeywordsGrava(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpoValido, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"keywords":[]`)

	rec = amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpoComPalavras, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var v account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, []string{"Nubank", "nu pagamentos"}, v.Keywords)

	rec = amb.chamar(t, minhaCasa, http.MethodGet, "/accounts/"+v.ID, "", amb.handler.Get, v.ID)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, []string{"Nubank", "nu pagamentos"}, v.Keywords)
}

func TestPatchKeywordsDeContaTriEstadoPelaAPI(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	v := criarContaComPalavras(t, amb.svc, minhaCasa, "Nubank", "nubank", "nu pagamentos")

	rec := amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+v.ID, `{"name":"Nubank Conta"}`, amb.handler.Update, v.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var depois account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &depois))
	assert.Equal(t, []string{"nubank", "nu pagamentos"}, depois.Keywords, "ausente não mexe")

	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+v.ID, `{"keywords":["roxinho"]}`, amb.handler.Update, v.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &depois))
	assert.Equal(t, []string{"roxinho"}, depois.Keywords, "presente substitui")

	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+v.ID, `{"keywords":[]}`, amb.handler.Update, v.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"keywords":[]`, "[] limpa")

	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+v.ID, `{"keywords":null}`, amb.handler.Update, v.ID)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "null não existe no contrato")

	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+v.ID, `{"keywords":[1]}`, amb.handler.Update, v.ID)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestKeywordsInvalidasDeContaDevolvem400ApontandoOItem(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	vinteEUma := make([]string, 0, 21)
	for i := range 21 {
		vinteEUma = append(vinteEUma, fmt.Sprintf("banco%02d", i))
	}
	listaDe21, err := json.Marshal(vinteEUma)
	require.NoError(t, err)

	casos := []struct {
		nome  string
		lista string
		campo string
	}{
		{"21 itens", string(listaDe21), "keywords"},
		{"repetida", `["Nubank","nubank"]`, "keywords[1]"},
		{"script", `["<script>alert(1)</script>"]`, "keywords[0]"},
		{"só stopword", `["de"]`, "keywords[0]"},
		{"só letras soltas", `["c & a"]`, "keywords[0]"},
		{"41 runas", `["` + strings.Repeat("a", 41) + `"]`, "keywords[0]"},
		{"terceiro item", `["nubank","c6","x"]`, "keywords[2]"},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			corpo := `{"name":"` + caso.nome + `","kind":"checking","openingBalanceCents":0,` +
				`"openingDate":"2026-09-01","keywords":` + caso.lista + `}`
			rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpo, amb.handler.Create, "")
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, "VALIDATION_FAILED", codigo)
			assert.Contains(t, campos, caso.campo)
			assert.NotContains(t, rec.Body.String(), "<script>")
			assert.NotContains(t, rec.Body.String(), "banco00")
		})
	}
	assert.Zero(t, amb.repo.criadas, "nenhuma conta pode ter sido criada")
}

func TestPalavraChaveDeContaTomadaDevolve409ComADonaDaCasa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	dona := criarContaComPalavras(t, amb.svc, minhaCasa, "Nubank", "Nubank")
	criarContaComPalavras(t, amb.svc, outraCasa, "C6", "c6 bank")

	corpo := `{"name":"Cartão","kind":"credit_card","openingBalanceCents":0,"openingDate":"2026-09-01","keywords":["nubank"]}`
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpo, amb.handler.Create, "")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, "KEYWORD_TAKEN", codigo)
	assert.Equal(t, "nubank", campos["keyword"])
	assert.Equal(t, dona.ID, campos["ownerId"])

	// A palavra da OUTRA casa está livre aqui: 201. (Nome diferente porque o
	// dublê de transação não desfaz a conta do 409 acima.)
	corpo = `{"name":"C6","kind":"checking","openingBalanceCents":0,"openingDate":"2026-09-01","keywords":["c6 bank"]}`
	rec = amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpo, amb.handler.Create, "")
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	carteira := criarContaComPalavras(t, amb.svc, minhaCasa, "Carteira")
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+carteira.ID,
		`{"keywords":["NUBANK"]}`, amb.handler.Update, carteira.ID)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	_, campos = corpoDeErro(t, rec)
	assert.Equal(t, dona.ID, campos["ownerId"])
}

// A corrida do índice único na borda: com a dona sumida a segunda tentativa
// responde 200 como se nada tivesse acontecido; na corrida tripla o 409 sai
// com `fields.keyword` (obrigatório no contrato KeywordConflict) e SEM
// `ownerId` — nunca um 409 sem `fields`. E nada disso passa pelo log.
func TestCorridaDoIndiceUnicoNaContaPelaAPINuncaOmiteFields(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	repo := novoRepo()
	svc := novoServico(t, repo)
	amb := &ambiente{
		handler: account.NewHandler(svc, logging.New(&logs, logging.Options{Level: "debug", Format: "json"}), 0),
		repo:    repo,
		svc:     svc,
	}
	c6 := criarContaComPalavras(t, amb.svc, minhaCasa, "C6")

	// (a) dona sumida: a segunda tentativa passa.
	amb.repo.colisoesForcadas = 1
	rec := amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+c6.ID,
		`{"keywords":["C6 Bank","Banco C6"]}`, amb.handler.Update, c6.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var v account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, []string{"C6 Bank", "Banco C6"}, v.Keywords)

	// (b) corrida tripla: 409 com a palavra e sem dona.
	amb.repo.colisoesForcadas = 2
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+c6.ID,
		`{"keywords":["Cartão C6","Banco C6"]}`, amb.handler.Update, c6.ID)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, "KEYWORD_TAKEN", codigo)
	require.NotNil(t, campos, "o contrato exige `fields` no 409")
	assert.Equal(t, "Cartão C6", campos["keyword"], "a primeira palavra enviada")
	_, temDona := campos["ownerId"]
	assert.False(t, temDona, "dona desconhecida é omitida, nunca enviada vazia (formato uuid)")
	assert.NotContains(t, rec.Body.String(), "ownerId")

	// A lista anterior ficou como estava: a tentativa recusada não gravou.
	assert.Equal(t, []string{"C6 Bank", "Banco C6"}, []string{repo.palavras[c6.ID][0].Keyword, repo.palavras[c6.ID][1].Keyword})

	// Nem a tentativa repetida nem o 409 passam pelo log (S8).
	for _, palavra := range []string{"c6 bank", "banco c6", "cartão c6"} {
		assert.NotContains(t, strings.ToLower(logs.String()), palavra)
	}
}

// S8: a palavra-chave é dado da casa e não entra no log — nem no 400, nem no
// 409, nem no 500 de infraestrutura.
func TestNenhumaPalavraChaveDeContaVaiParaOLog(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	repo := novoRepo()
	svc := novoServico(t, repo)
	amb := &ambiente{
		handler: account.NewHandler(svc, logging.New(&logs, logging.Options{Level: "debug", Format: "json"}), 0),
		repo:    repo,
		svc:     svc,
	}
	criarContaComPalavras(t, amb.svc, minhaCasa, "Nubank", "nubank")

	const marcador = "palavrasecreta"
	base := `"kind":"checking","openingBalanceCents":0,"openingDate":"2026-09-01"`
	corpos := []string{
		`{"name":"A",` + base + `,"keywords":["<` + marcador + `>"]}`,
		`{"name":"B",` + base + `,"keywords":["` + marcador + `","` + marcador + `"]}`,
		`{"name":"C",` + base + `,"keywords":["nubank"]}`,
	}
	for _, corpo := range corpos {
		amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpo, amb.handler.Create, "")
	}

	amb.repo.erroPalavras = errors.New("banco fora do ar")
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts",
		`{"name":"D",`+base+`,"keywords":["`+marcador+`"]}`, amb.handler.Create, "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "banco fora do ar")

	assert.Contains(t, logs.String(), "banco fora do ar")
	assert.NotContains(t, logs.String(), marcador)
	assert.NotContains(t, logs.String(), "nubank")
}
