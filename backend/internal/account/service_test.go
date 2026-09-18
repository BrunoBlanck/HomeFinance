package account_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	minhaCasa  = "casa-1"
	outraCasa  = "casa-2"
	fusoDaCasa = "America/Sao_Paulo"
)

// ator monta quem age. Em produção a casa e o usuário vêm do token e o IP da
// borda HTTP; aqui basta a casa, que é o que as regras deste pacote usam.
func ator(casa string) account.Actor {
	return account.Actor{HouseholdID: casa, UserID: "user-1", IP: "203.0.113.10"}
}

var hoje = civil.MustNew(2026, 9, 12)

// --- dublês ---------------------------------------------------------------

// repoFake guarda as contas em memória, MAS mantém o contrato de escopo do
// repositório real: nada é devolvido sem bater household_id. Um dublê frouxo
// aqui esconderia justamente o que mais importa provar.
type repoFake struct {
	linhas   map[string]account.Account
	criadas  int
	proximo  int
	ordemIDs []string

	// palavras guarda as palavras-chave por conta (schema v4). O dublê
	// mantém os dois contratos do repositório real: escopo por casa e norm
	// ÚNICA por casa entre contas diferentes.
	palavras map[string][]account.Keyword

	// Ganchos dos testes de palavra-chave (spec 0005):
	//   - listagens conta as chamadas a ListKeywords — a prova de "uma
	//     consulta por resposta, nunca N+1";
	//   - ocultarDonasUmaVez faz a PRÓXIMA KeywordOwners devolver vazio,
	//     simulando a corrida em que a outra edição ainda não tinha
	//     confirmado na pré-checagem, mas já confirmou quando o índice
	//     único decide em ReplaceKeywords;
	//   - erroPalavras, quando preenchido, é devolvido por ReplaceKeywords
	//     (falha de infraestrutura);
	//   - colisoesForcadas faz as PRÓXIMAS N chamadas a ReplaceKeywords
	//     devolverem ErrKeywordTaken cru sem gravar nada — é o índice único
	//     recusando por uma linha de transação concorrente que a
	//     pré-checagem não viu E que já sumiu quando o serviço reconsulta a
	//     dona (a "dona sumida" de escreverComPalavras);
	//   - substituicoes conta as chamadas a ReplaceKeywords — a prova de
	//     "uma segunda tentativa, nunca uma terceira".
	listagens          int
	ocultarDonasUmaVez bool
	erroPalavras       error
	colisoesForcadas   int
	substituicoes      int
}

func novoRepo() *repoFake {
	return &repoFake{
		linhas:   map[string]account.Account{},
		palavras: map[string][]account.Keyword{},
	}
}

func (r *repoFake) Create(_ context.Context, a *account.Account) error {
	r.linhas[a.ID] = *a
	r.ordemIDs = append(r.ordemIDs, a.ID)
	r.criadas++
	return nil
}

func (r *repoFake) ByID(_ context.Context, householdID, id string) (*account.Account, error) {
	a, ok := r.linhas[id]
	if !ok || a.HouseholdID != householdID || a.DeletedAt != nil {
		return nil, account.ErrNotFound
	}
	copia := a
	return &copia, nil
}

func (r *repoFake) List(_ context.Context, householdID string, includeArchived bool) ([]account.Account, error) {
	var out []account.Account
	for _, id := range r.ordemIDs {
		a := r.linhas[id]
		if a.HouseholdID != householdID || a.DeletedAt != nil {
			continue
		}
		if !includeArchived && a.ArchivedAt != nil {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *repoFake) Update(_ context.Context, a *account.Account) error {
	atual, ok := r.linhas[a.ID]
	if !ok || atual.HouseholdID != a.HouseholdID || atual.DeletedAt != nil {
		return account.ErrNotFound
	}
	r.linhas[a.ID] = *a
	return nil
}

func (r *repoFake) SoftDelete(_ context.Context, householdID, id string, at time.Time) error {
	a, ok := r.linhas[id]
	if !ok || a.HouseholdID != householdID || a.DeletedAt != nil {
		return account.ErrNotFound
	}
	a.DeletedAt = &at
	r.linhas[id] = a
	return nil
}

func (r *repoFake) CountAll(_ context.Context, householdID string) (int64, error) {
	var total int64
	for _, a := range r.linhas {
		if a.HouseholdID == householdID && a.DeletedAt == nil {
			total++
		}
	}
	return total, nil
}

func (r *repoFake) ListKeywords(_ context.Context, householdID string) ([]account.Keyword, error) {
	r.listagens++
	var out []account.Keyword
	for _, id := range r.ordemIDs {
		for _, k := range r.palavras[id] {
			if k.HouseholdID == householdID {
				out = append(out, k)
			}
		}
	}
	return out, nil
}

func (r *repoFake) KeywordOwners(_ context.Context, householdID string, norms []string) (map[string]string, error) {
	out := map[string]string{}
	if r.ocultarDonasUmaVez {
		r.ocultarDonasUmaVez = false
		return out, nil
	}
	for _, lista := range r.palavras {
		for _, k := range lista {
			if k.HouseholdID != householdID {
				continue
			}
			for _, n := range norms {
				if n == k.Norm {
					out[n] = k.AccountID
				}
			}
		}
	}
	return out, nil
}

func (r *repoFake) ReplaceKeywords(_ context.Context, householdID, accountID string, kws []account.Keyword) error {
	r.substituicoes++
	if r.erroPalavras != nil {
		return r.erroPalavras
	}
	if r.colisoesForcadas > 0 {
		r.colisoesForcadas--
		return account.ErrKeywordTaken
	}
	// O índice único (household_id, keyword_norm) do banco, reproduzido.
	for dona, lista := range r.palavras {
		if dona == accountID {
			continue
		}
		for _, k := range lista {
			if k.HouseholdID != householdID {
				continue
			}
			for _, nova := range kws {
				if nova.Norm == k.Norm {
					return account.ErrKeywordTaken
				}
			}
		}
	}
	novas := make([]account.Keyword, 0, len(kws))
	for _, k := range kws {
		k.HouseholdID = householdID
		k.AccountID = accountID
		novas = append(novas, k)
	}
	r.palavras[accountID] = novas
	return nil
}

func (r *repoFake) DeleteKeywords(_ context.Context, householdID, accountID string) error {
	restantes := r.palavras[accountID][:0:0]
	for _, k := range r.palavras[accountID] {
		if k.HouseholdID != householdID {
			restantes = append(restantes, k)
		}
	}
	r.palavras[accountID] = restantes
	return nil
}

func (r *repoFake) NameTaken(_ context.Context, householdID, nameNorm, exceptID string) (bool, error) {
	for _, a := range r.linhas {
		if a.HouseholdID != householdID || a.DeletedAt != nil || a.ArchivedAt != nil {
			continue
		}
		if a.ID != exceptID && a.NameNorm == nameNorm {
			return true, nil
		}
	}
	return false, nil
}

func (r *repoFake) proximoID() string {
	r.proximo++
	return fmt.Sprintf("acc-%03d", r.proximo)
}

// calendarFake devolve "hoje" fixo, no fuso da casa.
type calendarFake struct {
	dia civil.Date
	err error
}

func (c calendarFake) Today(context.Context, string) (civil.Date, error) {
	return c.dia, c.err
}

// txDireto executa sem transação de verdade — o comportamento transacional é
// exercitado nos testes de repositório, contra o SQLite.
type txDireto struct{}

func (txDireto) Do(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

// txComRollback desfaz o estado do dublê quando a operação falha — o que o
// UnitOfWork real faz com a transação. Só os testes da segunda tentativa
// (escreverComPalavras) precisam disso: sem rollback, a conta da primeira
// tentativa ficaria no dublê e a segunda tomaria ErrNameTaken, um artefato do
// teste que o banco real não produz.
//
// Clonar os mapas basta: o dublê nunca muda uma fatia no lugar, sempre
// atribui uma nova. Os ganchos (contadores, colisões) ficam fora do rollback
// de propósito — eles descrevem o que aconteceu, não o estado. `criadas`
// também fica: conta tentativas de INSERT, não linhas que sobreviveram.
type txComRollback struct{ repo *repoFake }

func (t txComRollback) Do(ctx context.Context, fn func(context.Context) error) error {
	linhas := maps.Clone(t.repo.linhas)
	palavras := maps.Clone(t.repo.palavras)
	ordem := slices.Clone(t.repo.ordemIDs)
	if err := fn(ctx); err != nil {
		t.repo.linhas, t.repo.palavras, t.repo.ordemIDs = linhas, palavras, ordem
		return err
	}
	return nil
}

// usoFake responde se a conta está em uso; simula o que a E2 vai registrar.
type usoFake struct {
	emUso bool
	err   error
}

func (u usoFake) AccountInUse(context.Context, string, string) (bool, error) {
	return u.emUso, u.err
}

func novoServico(t *testing.T, repo *repoFake, opts ...account.Option) *account.Service {
	t.Helper()
	return novoServicoComTx(t, repo, txDireto{}, opts...)
}

func novoServicoComTx(t *testing.T, repo *repoFake, tx account.Transactor, opts ...account.Option) *account.Service {
	t.Helper()
	base := []account.Option{
		account.WithIDs(repo.proximoID),
		account.WithClock(func() time.Time { return time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC) }),
	}
	return account.NewService(repo, calendarFake{dia: hoje}, tx, append(base, opts...)...)
}

func entradaValida() account.CreateInput {
	return account.CreateInput{
		Name:                "Conta Corrente",
		Kind:                account.KindChecking,
		OpeningBalanceCents: 150_00,
		OpeningDate:         civil.MustNew(2026, 9, 1),
	}
}

// --- criação --------------------------------------------------------------

func TestCriaContaComOsCamposDerivadosNoServidor(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	view, err := svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	assert.Equal(t, "acc-001", view.ID)
	assert.Equal(t, "Conta Corrente", view.Name)
	assert.EqualValues(t, 150_00, view.OpeningBalanceCents)
	// Saldo é derivado (ADR-017); sem lançamento, é a abertura.
	assert.EqualValues(t, 150_00, view.BalanceCents)
	assert.Nil(t, view.ArchivedAt)

	// household_id e name_norm vêm do servidor, nunca do cliente (S2).
	gravada := repo.linhas["acc-001"]
	assert.Equal(t, minhaCasa, gravada.HouseholdID)
	assert.Equal(t, "conta corrente", gravada.NameNorm)
}

func TestNomeEhNormalizadoParaComparacaoMasPreservadoParaExibicao(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	entrada := entradaValida()
	entrada.Name = "  Conta   Poupança  "
	view, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
	require.NoError(t, err)

	// O usuário vê o que escolheu, com o espaçamento arrumado...
	assert.Equal(t, "Conta Poupança", view.Name)
	// ...e a comparação usa a forma dobrada.
	assert.Equal(t, "conta poupanca", repo.linhas[view.ID].NameNorm)
}

func TestNomeDuplicadoEhRecusadoIgnorandoAcentoECaixa(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	primeira := entradaValida()
	primeira.Name = "Poupança"
	_, err := svc.Create(t.Context(), ator(minhaCasa), primeira)
	require.NoError(t, err)

	for _, variante := range []string{"poupanca", "POUPANÇA", "  Poupança  ", "Poupanca"} {
		segunda := entradaValida()
		segunda.Name = variante
		_, err := svc.Create(t.Context(), ator(minhaCasa), segunda)
		assert.ErrorIs(t, err, account.ErrNameTaken, "variante %q deveria colidir", variante)
	}
}

// O nome de OUTRA casa não pode bloquear o meu — e este teste prova a regra no
// serviço, não só no repositório.
func TestNomeDeOutraCasaNaoBloqueia(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	_, err := svc.Create(t.Context(), ator(outraCasa), entradaValida())
	require.NoError(t, err)

	_, err = svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	assert.NoError(t, err)
}

func TestTipoForaDaAllowlistEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	for _, tipo := range []string{"", "poupanca", "CASH", "cripto", "checking ", "'; DROP TABLE accounts--"} {
		entrada := entradaValida()
		entrada.Kind = tipo
		_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
		assert.ErrorIs(t, err, account.ErrInvalidKind, "tipo %q deveria ser recusado", tipo)
	}

	for _, tipo := range account.Kinds() {
		entrada := entradaValida()
		entrada.Kind = tipo
		entrada.Name = "Conta " + tipo
		_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
		assert.NoError(t, err, "tipo %q deveria ser aceito", tipo)
	}
}

func TestNomeVazioOuLongoDemaisEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	for _, nome := range []string{"", "   ", "\t\n"} {
		entrada := entradaValida()
		entrada.Name = nome
		_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
		assert.ErrorIs(t, err, account.ErrInvalidName, "nome %q deveria ser recusado", nome)
	}

	// O limite é em RUNAS, não em bytes: 80 acentuadas passam, 81 não.
	entrada := entradaValida()
	entrada.Name = strings.Repeat("ç", account.MaxNameLen)
	_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
	assert.NoError(t, err, "80 runas acentuadas cabem")

	entrada.Name = strings.Repeat("ç", account.MaxNameLen+1)
	_, err = svc.Create(t.Context(), ator(minhaCasa), entrada)
	assert.ErrorIs(t, err, account.ErrInvalidName)
}

func TestValorForaDaFaixaEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	// A faixa é simétrica: o cartão nasce devendo.
	for _, cents := range []int64{0, 1, -1, account.MaxAmountCents, -account.MaxAmountCents} {
		entrada := entradaValida()
		entrada.OpeningBalanceCents = cents
		entrada.Name = fmt.Sprintf("Conta %d", cents)
		_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
		assert.NoError(t, err, "valor %d deveria ser aceito", cents)
	}

	for _, cents := range []int64{account.MaxAmountCents + 1, -account.MaxAmountCents - 1, 1 << 62} {
		entrada := entradaValida()
		entrada.OpeningBalanceCents = cents
		entrada.Name = fmt.Sprintf("Absurda %d", cents)
		_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
		assert.ErrorIs(t, err, account.ErrInvalidAmount, "valor %d deveria ser recusado", cents)
	}
}

func TestDataDeAberturaForaDaJanelaEhRecusada(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	casos := map[string]civil.Date{
		"data zero":         {},
		"antes de 1970":     civil.MustNew(1969, 12, 31),
		"11 anos no futuro": civil.MustNew(2037, 10, 1),
		"muito no futuro":   civil.MustNew(2099, 1, 1),
	}
	for nome, data := range casos {
		entrada := entradaValida()
		entrada.OpeningDate = data
		entrada.Name = "Conta " + nome
		_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
		assert.ErrorIs(t, err, account.ErrInvalidDate, "caso %q", nome)
	}

	// As bordas aceitas.
	for nome, data := range map[string]civil.Date{
		"1º de janeiro de 1970": civil.MustNew(1970, 1, 1),
		"hoje":                  hoje,
		"dentro de 10 anos":     civil.MustNew(2036, 9, 1),
	} {
		entrada := entradaValida()
		entrada.OpeningDate = data
		entrada.Name = "Conta ok " + nome
		_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
		assert.NoError(t, err, "caso %q", nome)
	}
}

func TestTetoDeContasPorCasa(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	for i := range account.MaxPerHousehold {
		entrada := entradaValida()
		entrada.Name = fmt.Sprintf("Conta %03d", i)
		_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
		require.NoError(t, err, "a de número %d deveria caber", i)
	}

	entrada := entradaValida()
	entrada.Name = "A que passa do limite"
	_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
	assert.ErrorIs(t, err, account.ErrTooMany)

	// O teto é POR CASA: a outra casa continua livre.
	_, err = svc.Create(t.Context(), ator(outraCasa), entradaValida())
	assert.NoError(t, err)
}

// --- leitura --------------------------------------------------------------

func TestListaSomaOTotalDoQueMostra(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criar := func(nome string, cents int64) account.View {
		entrada := entradaValida()
		entrada.Name, entrada.OpeningBalanceCents = nome, cents
		v, err := svc.Create(ctx, ator(minhaCasa), entrada)
		require.NoError(t, err)
		return v
	}
	criar("Carteira", 100_00)
	criar("Conta", 250_00)
	cartao := criar("Cartão", -80_00)

	lista, err := svc.List(ctx, ator(minhaCasa), false)
	require.NoError(t, err)
	assert.Len(t, lista.Items, 3)
	assert.EqualValues(t, 270_00, lista.TotalBalanceCents, "100 + 250 - 80")

	// Arquivada sai da lista e SAI DO TOTAL — o total sempre soma as linhas
	// exibidas, senão a tela mostra um número que não bate com o que se vê.
	_, err = svc.Archive(ctx, ator(minhaCasa), cartao.ID)
	require.NoError(t, err)

	semArquivadas, err := svc.List(ctx, ator(minhaCasa), false)
	require.NoError(t, err)
	assert.Len(t, semArquivadas.Items, 2)
	assert.EqualValues(t, 350_00, semArquivadas.TotalBalanceCents)

	comArquivadas, err := svc.List(ctx, ator(minhaCasa), true)
	require.NoError(t, err)
	assert.Len(t, comArquivadas.Items, 3)
	assert.EqualValues(t, 270_00, comArquivadas.TotalBalanceCents)
}

func TestContaDeOutraCasaNaoEhLegivel(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	daOutra, err := svc.Create(ctx, ator(outraCasa), entradaValida())
	require.NoError(t, err)

	_, err = svc.Get(ctx, ator(minhaCasa), daOutra.ID)
	assert.ErrorIs(t, err, account.ErrNotFound)

	lista, err := svc.List(ctx, ator(minhaCasa), true)
	require.NoError(t, err)
	assert.Empty(t, lista.Items)
	assert.Zero(t, lista.TotalBalanceCents)
}

// --- edição ---------------------------------------------------------------

func TestEdicaoParcialSoMexeNoQueVeio(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criada, err := svc.Create(ctx, ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	novoNome := "Conta Nova"
	atualizada, err := svc.Update(ctx, ator(minhaCasa), criada.ID, account.UpdateInput{Name: &novoNome})
	require.NoError(t, err)

	assert.Equal(t, "Conta Nova", atualizada.Name)
	assert.Equal(t, criada.Kind, atualizada.Kind, "tipo não veio, não muda")
	assert.Equal(t, criada.OpeningBalanceCents, atualizada.OpeningBalanceCents)
	assert.Equal(t, criada.OpeningDate, atualizada.OpeningDate)
	assert.Equal(t, "conta nova", repo.linhas[criada.ID].NameNorm, "name_norm acompanha")
}

func TestRenomearParaOProprioNomeNaoColideConsigoMesma(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criada, err := svc.Create(ctx, ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	mesmo := "conta corrente" // só a caixa muda
	_, err = svc.Update(ctx, ator(minhaCasa), criada.ID, account.UpdateInput{Name: &mesmo})
	assert.NoError(t, err)
}

func TestEditarContaDeOutraCasaNaoAcontece(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	daOutra, err := svc.Create(ctx, ator(outraCasa), entradaValida())
	require.NoError(t, err)

	nome := "Sequestrada"
	_, err = svc.Update(ctx, ator(minhaCasa), daOutra.ID, account.UpdateInput{Name: &nome})
	assert.ErrorIs(t, err, account.ErrNotFound)
	assert.Equal(t, "Conta Corrente", repo.linhas[daOutra.ID].Name)
}

// --- arquivamento ---------------------------------------------------------

func TestArquivarEhIdempotente(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criada, err := svc.Create(ctx, ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	primeira, err := svc.Archive(ctx, ator(minhaCasa), criada.ID)
	require.NoError(t, err)
	require.NotNil(t, primeira.ArchivedAt)

	// Dois cliques não são um erro para explicar ao usuário.
	segunda, err := svc.Archive(ctx, ator(minhaCasa), criada.ID)
	require.NoError(t, err)
	assert.Equal(t, *primeira.ArchivedAt, *segunda.ArchivedAt, "a data não é reescrita")
}

func TestDesarquivarRecusaSeONomeFoiTomado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	original, err := svc.Create(ctx, ator(minhaCasa), entradaValida())
	require.NoError(t, err)
	_, err = svc.Archive(ctx, ator(minhaCasa), original.ID)
	require.NoError(t, err)

	// Com a primeira arquivada, o nome ficou livre e outra o tomou.
	_, err = svc.Create(ctx, ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	_, err = svc.Unarchive(ctx, ator(minhaCasa), original.ID)
	assert.ErrorIs(t, err, account.ErrNameTaken,
		"desarquivar é voltar a ser ativa, e a unicidade vale entre as ativas")
}

func TestDesarquivarVoltaAContaParaAListaPadrao(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criada, err := svc.Create(ctx, ator(minhaCasa), entradaValida())
	require.NoError(t, err)
	_, err = svc.Archive(ctx, ator(minhaCasa), criada.ID)
	require.NoError(t, err)

	voltou, err := svc.Unarchive(ctx, ator(minhaCasa), criada.ID)
	require.NoError(t, err)
	assert.Nil(t, voltou.ArchivedAt)

	lista, err := svc.List(ctx, ator(minhaCasa), false)
	require.NoError(t, err)
	assert.Len(t, lista.Items, 1)
}

// --- exclusão -------------------------------------------------------------

func TestExcluirContaSemUso(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criada, err := svc.Create(ctx, ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	require.NoError(t, svc.Delete(ctx, ator(minhaCasa), criada.ID))

	_, err = svc.Get(ctx, ator(minhaCasa), criada.ID)
	assert.ErrorIs(t, err, account.ErrNotFound)
	assert.NotNil(t, repo.linhas[criada.ID].DeletedAt, "a exclusão é LÓGICA: a linha continua lá")
}

func TestExcluirContaEmUsoEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo, account.WithUsageCheckers(usoFake{emUso: true}))
	ctx := t.Context()

	criada, err := svc.Create(ctx, ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	assert.ErrorIs(t, svc.Delete(ctx, ator(minhaCasa), criada.ID), account.ErrInUse)
	assert.Nil(t, repo.linhas[criada.ID].DeletedAt)
}

// Falha do verificador NÃO pode ser lida como "não está em uso": isso
// transformaria uma indisponibilidade momentânea do banco em perda de dado —
// o usuário excluiria uma conta que tem lançamento.
func TestVerificadorDeUsoQueFalhaAbortaAExclusao(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo, account.WithUsageCheckers(usoFake{err: errors.New("timeout")}))
	ctx := t.Context()

	criada, err := svc.Create(ctx, ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	err = svc.Delete(ctx, ator(minhaCasa), criada.ID)
	require.Error(t, err)
	assert.NotErrorIs(t, err, account.ErrInUse, "é falha de infra, não regra de negócio")
	assert.Nil(t, repo.linhas[criada.ID].DeletedAt, "na dúvida, não exclui")
}

func TestExcluirContaDeOutraCasaNaoAcontece(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	daOutra, err := svc.Create(ctx, ator(outraCasa), entradaValida())
	require.NoError(t, err)

	assert.ErrorIs(t, svc.Delete(ctx, ator(minhaCasa), daOutra.ID), account.ErrNotFound)
	assert.Nil(t, repo.linhas[daOutra.ID].DeletedAt)
}

// --- fuso -----------------------------------------------------------------

// A janela de datas válidas depende de "hoje", e "hoje" é o da CASA. Este
// teste fixa a consequência: às 21h em São Paulo, o dia seguinte em UTC ainda
// não chegou para a casa, e uma data que seria "hoje" no servidor pode estar
// no futuro para ela.
func TestJanelaDeDataUsaOHojeDaCasa(t *testing.T) {
	t.Parallel()

	sp, err := time.LoadLocation(fusoDaCasa)
	require.NoError(t, err)

	// 13/09/2026 00:30 UTC = 12/09/2026 21:30 em São Paulo.
	instante := time.Date(2026, 9, 13, 0, 30, 0, 0, time.UTC)
	assert.Equal(t, "2026-09-13", civil.FromTime(instante, time.UTC).String())
	assert.Equal(t, "2026-09-12", civil.FromTime(instante, sp).String())

	repo := novoRepo()
	svc := account.NewService(repo,
		calendarFake{dia: civil.FromTime(instante, sp)},
		txDireto{},
		account.WithIDs(repo.proximoID),
		account.WithClock(func() time.Time { return instante }),
	)

	// Dez anos a partir do "hoje" da casa continua dentro da janela.
	entrada := entradaValida()
	entrada.OpeningDate = civil.MustNew(2036, 9, 1)
	_, err = svc.Create(t.Context(), ator(minhaCasa), entrada)
	assert.NoError(t, err)
}

func TestCalendarioIndisponivelNaoViraDataAceita(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := account.NewService(repo,
		calendarFake{err: errors.New("casa sumiu")},
		txDireto{},
		account.WithIDs(repo.proximoID),
	)

	_, err := svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	require.Error(t, err)
	assert.Zero(t, repo.criadas, "sem saber que dia é hoje, não se valida data — e não se grava")
}
