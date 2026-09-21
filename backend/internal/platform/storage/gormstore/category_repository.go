package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"gorm.io/gorm"
)

// CategoryRepository implementa category.Repository.
type CategoryRepository struct{ base }

// NewCategoryRepository monta o repositório.
func NewCategoryRepository(db *storage.DB) *CategoryRepository {
	return &CategoryRepository{base{db: db}}
}

var _ category.Repository = (*CategoryRepository)(nil)

// scope filtra pela casa e esconde o excluído logicamente — as duas condições
// que não podem faltar em nenhuma consulta (ver AccountRepository.scope).
func (r *CategoryRepository) scope(ctx context.Context, householdID string) *gorm.DB {
	return r.conn(ctx).
		Model(&Category{}).
		Where("household_id = ?", householdID).
		Where("deleted_at IS NULL")
}

// Create insere a categoria.
func (r *CategoryRepository) Create(ctx context.Context, c *category.Category) error {
	if err := r.conn(ctx).Create(toCategoryModel(c)).Error; err != nil {
		return fmt.Errorf("inserindo categoria: %w", err)
	}
	return nil
}

// ByID devolve a categoria da casa; de outra casa é ErrNotFound (S1).
func (r *CategoryRepository) ByID(ctx context.Context, householdID, id string) (*category.Category, error) {
	var m Category
	err := r.scope(ctx, householdID).Where("id = ?", id).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, category.ErrNotFound
		}
		return nil, fmt.Errorf("buscando categoria: %w", err)
	}
	return toCategoryEntity(&m), nil
}

// List devolve grupos e folhas juntos, ordenados por nome normalizado.
//
// Uma consulta só, plana: a árvore é montada no serviço, em memória. Com teto
// de 200 categorias por casa isso é irrelevante em custo, e evita
// completamente a consulta hierárquica — que teria sintaxe diferente em cada
// um dos quatro dialetos.
func (r *CategoryRepository) List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error) {
	q := r.scope(ctx, householdID)
	if !includeArchived {
		q = q.Where("archived_at IS NULL")
	}

	var rows []Category
	if err := q.Order("name_norm ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("listando categorias: %w", err)
	}

	out := make([]category.Category, 0, len(rows))
	for i := range rows {
		out = append(out, *toCategoryEntity(&rows[i]))
	}
	return out, nil
}

// Children devolve as filhas diretas, arquivadas inclusive — quem chama é a
// regra de exclusão e a de arquivamento em cascata, e as duas precisam
// enxergar tudo que está pendurado no grupo.
func (r *CategoryRepository) Children(ctx context.Context, householdID, parentID string) ([]category.Category, error) {
	var rows []Category
	err := r.scope(ctx, householdID).
		Where("parent_id = ?", parentID).
		Order("name_norm ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando filhas: %w", err)
	}

	out := make([]category.Category, 0, len(rows))
	for i := range rows {
		out = append(out, *toCategoryEntity(&rows[i]))
	}
	return out, nil
}

// Update grava os campos mutáveis.
//
// parent_id NÃO está na lista: mover categoria de grupo não existe no v1
// (invariante 6 da spec 0003), e deixá-lo fora do SET torna isso verdade na
// camada de dados, não só na de serviço.
func (r *CategoryRepository) Update(ctx context.Context, c *category.Category) error {
	m := toCategoryModel(c)
	res := r.scope(ctx, c.HouseholdID).
		Where("id = ?", c.ID).
		Select("name", "name_norm", "kind", "archived_at", "updated_at").
		Updates(m)
	if res.Error != nil {
		return fmt.Errorf("atualizando categoria: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return category.ErrNotFound
	}
	return nil
}

// SoftDelete marca a exclusão lógica.
func (r *CategoryRepository) SoftDelete(ctx context.Context, householdID, id string, at time.Time) error {
	res := r.scope(ctx, householdID).
		Where("id = ?", id).
		Updates(map[string]any{"deleted_at": at, "updated_at": at})
	if res.Error != nil {
		return fmt.Errorf("excluindo categoria: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return category.ErrNotFound
	}
	return nil
}

// CountAll conta grupos e folhas não excluídos.
func (r *CategoryRepository) CountAll(ctx context.Context, householdID string) (int64, error) {
	var total int64
	if err := r.scope(ctx, householdID).Count(&total).Error; err != nil {
		return 0, fmt.Errorf("contando categorias: %w", err)
	}
	return total, nil
}

// NameTaken informa se já existe categoria ATIVA com este nome entre os
// irmãos.
//
// O parentID nulo vira `parent_id IS NULL` de forma EXPLÍCITA, e não
// `parent_id = ?` com nil: em SQL, `= NULL` nunca é verdadeiro, então a
// consulta com nil não encontraria nenhum grupo e a unicidade dos grupos
// simplesmente não valeria — o tipo de bug que passa em revisão porque o
// código "parece" certo.
func (r *CategoryRepository) NameTaken(ctx context.Context, householdID string, parentID *string, nameNorm, exceptID string) (bool, error) {
	q := r.scope(ctx, householdID).
		Where("archived_at IS NULL").
		Where("name_norm = ?", nameNorm)

	if parentID == nil {
		q = q.Where("parent_id IS NULL")
	} else {
		q = q.Where("parent_id = ?", *parentID)
	}
	if exceptID != "" {
		q = q.Where("id <> ?", exceptID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return false, fmt.Errorf("verificando nome de categoria: %w", err)
	}
	return total > 0, nil
}

// maxLiveStateIDs é o teto de ids por reconferência. É o mesmo
// category.MaxPerHousehold: o conjunto relido de uma execução nunca passa da
// taxonomia da casa, e passar disso significa que o teto foi violado —
// situação em que a resposta segura é falhar, não fatiar (ADR-027f, ADR-029
// j.2). Fatiar uma reconferência seria aprovar parte dos ids sem olhar o resto.
const maxLiveStateIDs = 200

// LiveStates devolve o estado ATUAL das categorias vivas da casa, dentre os ids
// informados — em UMA consulta. Id ausente do mapa não existe mais (excluído,
// ou de outra casa).
//
// Existe por causa da janela TOCTOU do POST /investments/detect: o plano é
// calculado FORA da transação (achado A2 da entrega anterior, de propósito) e
// entre o plano e o UPDATE cabe uma requisição inteira. Quem chama reconfere
// DENTRO da transação, antes de escrever.
//
// Três coisas podem ter mudado na janela, e as três saem daqui:
//
//   - a categoria foi EXCLUÍDA. A checagem `inUse` de DELETE /categories/{id}
//     não encontra nada, porque a escrita em massa ainda não gravou; sem esta
//     releitura o UPDATE penduraria lançamentos numa categoria excluída, que é
//     o estado que category.ErrInUse existe para impedir;
//   - a NATUREZA mudou. Trocar de natureza DENTRO do mesmo lado do dinheiro é
//     permitido mesmo com a categoria em uso (ADR-029c), então `investment`
//     vira `expense` sem nenhuma recusa — e quem gravasse sem reler passaria a
//     escrever categoria comum por cima de categoria comum;
//   - o grupo GANHOU subcategoria ativa e deixou de receber lançamento (spec
//     0005 §12/§13);
//   - a categoria foi ARQUIVADA (campo Archived).
//
// ARQUIVADA continua no MAPA, e não some: quem decide o que fazer com ela é o
// CHAMADOR, porque o produto trata os dois lados de formas opostas — marcação
// existente sobrevive ao arquivamento, atribuição nova não. Ver o doc de
// category.LiveState. Só a EXCLUSÃO tira do mapa.
//
// Uma consulta, e não uma por id: `(id IN ? OR parent_id IN ?)` traz, no mesmo
// resultado, as categorias pedidas e as filhas delas. O pior caso são 2 × 200 =
// 400 parâmetros, abaixo do piso histórico de 999 do SQLite e muito abaixo dos
// 2100 do SQL Server. Os parênteses estão escritos no texto da condição para
// que a precedência do OR não dependa de o GORM embrulhar a expressão.
//
// ⚠️ Esta é a ÚNICA fachada de releitura, e ela NÃO ganha uma "mais
// conveniente" ao lado. Existiu aqui um `AssignableCategoryIDs` que devolvia só
// os ids vivos e sem subcategoria ativa: nome convidativo, semântica estreita —
// ele não olhava natureza nem arquivamento, e o próprio doc dele precisava
// avisar disso. Foi REMOVIDO ao ficar sem nenhum chamador (as duas rotas de
// escrita em massa migraram para LiveStates), e a remoção é deliberada, não
// faxina: o que qualifica um destino MUDA por rota — o `detect` de
// investimentos exige natureza `investment`/`redemption` compatível com o lado
// do lote, o `auto-categorize` exige AceitaLancamento para o `kind` da linha —,
// e uma revisão de segurança já provou com PoC EXECUTADO o preço de decidir
// isso na camada errada: com a natureza trocada dentro da janela do plano,
// aquela rota gravava despesa dentro de categoria de RECEITA, com resposta 200,
// um estado que todas as outras portas do produto recusam.
//
// Quem precisa reler, então, recebe o estado INTEIRO e decide com
// category.DestinoAindaQualifica mais a exigência da própria rota. Um helper
// que responda "atribuível" com menos eixos do que a rota precisa é uma
// armadilha para o próximo, que vai acreditar no nome.
func (r *CategoryRepository) LiveStates(ctx context.Context, householdID string, ids []string) (map[string]category.LiveState, error) {
	if householdID == "" {
		return nil, fmt.Errorf("reconferir categorias exige casa")
	}
	unicos := dedupeStrings(ids)
	if len(unicos) == 0 {
		return nil, nil
	}
	if len(unicos) > maxLiveStateIDs {
		return nil, category.ErrTooManyToCheck
	}

	// archived_at entra no SELECT porque a decisão sobre a FILHA depende dele:
	// sem a coluna, toda filha voltaria com ArchivedAt nil e um grupo cujas
	// filhas foram todas arquivadas seria recusado como se ainda tivesse
	// subcategoria ativa. kind entra porque é a natureza relida — o campo cuja
	// ausência era explorável.
	var rows []Category
	err := r.scope(ctx, householdID).
		Where("(id IN ? OR parent_id IN ?)", unicos, unicos).
		Select("id", "parent_id", "kind", "archived_at").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reconferindo categorias: %w", err)
	}

	pedidos := make(map[string]struct{}, len(unicos))
	for _, id := range unicos {
		pedidos[id] = struct{}{}
	}

	out := make(map[string]category.LiveState, len(unicos))
	comFilhaAtiva := make(map[string]struct{}, len(rows))
	for i := range rows {
		c := rows[i]
		if _, pedido := pedidos[c.ID]; pedido {
			out[c.ID] = category.LiveState{ID: c.ID, Kind: c.Kind, Archived: c.ArchivedAt != nil}
		}
		// A filha entra na consulta pelo MESMO scope (casa + viva). Arquivada
		// não conta como filha ativa — é a regra do recusarGrupoComFilhas do
		// domínio de lançamentos, e as duas precisam concordar.
		if c.ParentID == nil || c.ArchivedAt != nil {
			continue
		}
		if _, pedido := pedidos[*c.ParentID]; pedido {
			comFilhaAtiva[*c.ParentID] = struct{}{}
		}
	}
	// Segunda passagem: a filha pode vir ANTES do pai no resultado, e marcar o
	// pai numa passagem só perderia o caso em que ele ainda não entrou no mapa.
	for id := range comFilhaAtiva {
		if st, ok := out[id]; ok {
			st.HasActiveChild = true
			out[id] = st
		}
	}
	return out, nil
}

// --- palavras-chave (schema v4, spec 0005, ADR-026d) ------------------------

// keywordBatchSize é quantas palavras-chave vão em cada INSERT. Com 7 colunas
// e no máximo 20 por dona, uma fatia de 30 cobre a lista inteira em um comando
// (210 parâmetros — muito abaixo dos 999 do SQLite e 2100 do SQL Server).
const keywordBatchSize = 30

// keywordScope filtra as palavras-chave pela casa. Não há deleted_at aqui: a
// lista é substituída ou apagada fisicamente — palavra-chave é configuração,
// não dado financeiro.
func (r *CategoryRepository) keywordScope(ctx context.Context, householdID string) *gorm.DB {
	return r.conn(ctx).Model(&CategoryKeyword{}).Where("household_id = ?", householdID)
}

// ListKeywords devolve todas as palavras-chave da casa em UMA consulta.
//
// A ordem é constante no código (P6): por dona, depois pela posição de
// cadastro, com o id desempatando — é a ordem em que a tela mostra a lista.
func (r *CategoryRepository) ListKeywords(ctx context.Context, householdID string) ([]category.Keyword, error) {
	if householdID == "" {
		return nil, fmt.Errorf("listar palavras-chave exige a casa")
	}
	var rows []CategoryKeyword
	err := r.keywordScope(ctx, householdID).
		Order("category_id ASC, position ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando palavras-chave de categoria: %w", err)
	}
	out := make([]category.Keyword, 0, len(rows))
	for i := range rows {
		out = append(out, *toCategoryKeywordEntity(&rows[i]))
	}
	return out, nil
}

// keywordOwnerRow é a projeção de KeywordOwners: duas colunas, nenhuma delas
// a forma exibível — só o que responde "quem tem esta norm".
type keywordOwnerRow struct {
	KeywordNorm string
	OwnerID     string
}

// KeywordOwners devolve norm -> categoria dona, para as norms que já existem
// na casa. Usa o prefixo do índice único (household_id, keyword_norm).
//
// A lista é fatiada em idChunkSize pelo mesmo motivo de RowsByIDs: o IN (...)
// vira um parâmetro por norm, e o teto por comando é 2100 no SQL Server e 999
// no SQLite.
func (r *CategoryRepository) KeywordOwners(ctx context.Context, householdID string, norms []string) (map[string]string, error) {
	if householdID == "" {
		return nil, fmt.Errorf("consultar donas de palavra-chave exige a casa")
	}
	unicas := dedupeStrings(norms)
	out := make(map[string]string, len(unicas))
	for inicio := 0; inicio < len(unicas); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(unicas))

		var rows []keywordOwnerRow
		err := r.keywordScope(ctx, householdID).
			Where("keyword_norm IN ?", unicas[inicio:fim]).
			Select("keyword_norm AS keyword_norm, category_id AS owner_id").
			Scan(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("consultando donas de palavra-chave: %w", err)
		}
		for i := range rows {
			out[rows[i].KeywordNorm] = rows[i].OwnerID
		}
	}
	return out, nil
}

// ReplaceKeywords apaga a lista atual da categoria e grava a nova, na
// transação em curso.
//
// A guarda de casa e de dona é defesa em profundidade, como em CreateBatch:
// uma palavra que chegue apontando para outra casa ou outra categoria derruba
// a escrita inteira em vez de ser gravada onde não devia. HouseholdID e
// CategoryID vazios na entidade são preenchidos pelos argumentos — o serviço
// não precisa repeti-los em cada item.
func (r *CategoryRepository) ReplaceKeywords(ctx context.Context, householdID, categoryID string, kws []category.Keyword) error {
	if householdID == "" || categoryID == "" {
		return fmt.Errorf("substituir palavras-chave exige casa e categoria")
	}

	models := make([]CategoryKeyword, 0, len(kws))
	for i := range kws {
		k := kws[i]
		if k.HouseholdID != "" && k.HouseholdID != householdID {
			return fmt.Errorf("palavra-chave de outra casa na lista")
		}
		if k.CategoryID != "" && k.CategoryID != categoryID {
			return fmt.Errorf("palavra-chave de outra categoria na lista")
		}
		// NOT NULL não barra string vazia, e norm vazia é uma palavra que
		// nunca casa com nada — ocupando uma vaga do índice único da casa.
		if k.ID == "" || k.Keyword == "" || k.Norm == "" {
			return fmt.Errorf("palavra-chave sem id, texto ou forma normalizada")
		}
		k.HouseholdID = householdID
		k.CategoryID = categoryID
		models = append(models, *toCategoryKeywordModel(&k))
	}

	if err := r.DeleteKeywords(ctx, householdID, categoryID); err != nil {
		return err
	}
	if len(models) == 0 {
		return nil
	}
	if err := r.conn(ctx).CreateInBatches(&models, keywordBatchSize).Error; err != nil {
		// O índice único (household_id, keyword_norm) decidiu a corrida que
		// escapou da pré-checagem do serviço. O erro nativo NÃO é embrulhado:
		// a mensagem do banco ecoa a palavra recusada, e iria parar no log.
		if storage.IsDuplicate(err) {
			return category.ErrKeywordTaken
		}
		return fmt.Errorf("gravando palavras-chave de categoria: %w", err)
	}
	return nil
}

// keywordTally é a projeção de UMA consulta agregada sobre as palavras de uma
// dona: quantas há e a maior posição — o que AppendKeywords precisa para
// continuar a numeração e reconferir o teto na transação em curso.
type keywordTally struct {
	Total       int64
	MaxPosition int
}

// keywordTallyProjecao é constante: nenhuma entrada do usuário entra no
// Select (docs/SEGURANCA.md §3). COALESCE(MAX(..), -1) devolve -1 numa dona
// sem palavra, para a primeira posição ser 0.
const keywordTallyProjecao = "COUNT(*) AS total, COALESCE(MAX(position), -1) AS max_position"

// AppendKeywords ACRESCENTA palavras-chave à categoria, na transação em
// curso — sem apagar nada. É a escrita do import de IA (spec 0010 §4.2), e
// existe ao lado de ReplaceKeywords por um motivo de corrida (achado A2 da
// revisão de segurança da E9b):
//
// ReplaceKeywords é DELETE + INSERT da lista inteira montada de um índice
// lido antes. Em READ COMMITTED (PostgreSQL, MySQL, MSSQL), uma palavra
// comitada por OUTRA transação entre a leitura e a escrita é apagada pelo
// DELETE e não volta no INSERT — some sem erro, numa operação anunciada como
// aditiva. Inserindo só as novas, não há DELETE, e o índice único
// (household_id, keyword_norm) decide a corrida: colisão volta como
// ErrKeywordTaken, nunca como perda silenciosa.
//
// Position CONTINUA a numeração atual: MAX(position)+1 é lido aqui, na
// mesma transação, e não do índice do chamador — o que também fecha a
// numeração contra a mesma corrida. O teto por dona (MaxKeywordsPerOwner) é
// reconferido contra COUNT(*) vivo; passar dele é ErrTooManyKeywords.
//
// Os testes de corrida do import rodam com MaxOpenConns: 1 (SQLite, em
// arquivo): as transações serializam no Begin e a intercalação real leitura →
// escrita alheia → escrita não é reproduzida ali. A prova da intercalação
// fica para a suíte de testcontainers com PostgreSQL.
func (r *CategoryRepository) AppendKeywords(ctx context.Context, householdID, categoryID string, kws []category.Keyword) error {
	if householdID == "" || categoryID == "" {
		return fmt.Errorf("acrescentar palavras-chave exige casa e categoria")
	}
	if len(kws) == 0 {
		return nil
	}

	models := make([]CategoryKeyword, 0, len(kws))
	for i := range kws {
		k := kws[i]
		if k.HouseholdID != "" && k.HouseholdID != householdID {
			return fmt.Errorf("palavra-chave de outra casa na lista")
		}
		if k.CategoryID != "" && k.CategoryID != categoryID {
			return fmt.Errorf("palavra-chave de outra categoria na lista")
		}
		if k.ID == "" || k.Keyword == "" || k.Norm == "" {
			return fmt.Errorf("palavra-chave sem id, texto ou forma normalizada")
		}
		k.HouseholdID = householdID
		k.CategoryID = categoryID
		models = append(models, *toCategoryKeywordModel(&k))
	}

	var atual keywordTally
	err := r.keywordScope(ctx, householdID).
		Where("category_id = ?", categoryID).
		Select(keywordTallyProjecao).
		Scan(&atual).Error
	if err != nil {
		return fmt.Errorf("contando palavras-chave da categoria: %w", err)
	}
	if atual.Total+int64(len(models)) > category.MaxKeywordsPerOwner {
		return category.ErrTooManyKeywords
	}
	for i := range models {
		models[i].Position = atual.MaxPosition + 1 + i
	}

	if err := r.conn(ctx).CreateInBatches(&models, keywordBatchSize).Error; err != nil {
		// Erro nativo não embrulhado: a mensagem do banco ecoa a palavra.
		if storage.IsDuplicate(err) {
			return category.ErrKeywordTaken
		}
		return fmt.Errorf("acrescentando palavras-chave de categoria: %w", err)
	}
	return nil
}

// DeleteKeywords apaga fisicamente as palavras-chave da categoria.
func (r *CategoryRepository) DeleteKeywords(ctx context.Context, householdID, categoryID string) error {
	if householdID == "" || categoryID == "" {
		return fmt.Errorf("apagar palavras-chave exige casa e categoria")
	}
	err := r.conn(ctx).
		Where("household_id = ?", householdID).
		Where("category_id = ?", categoryID).
		Delete(&CategoryKeyword{}).Error
	if err != nil {
		return fmt.Errorf("apagando palavras-chave de categoria: %w", err)
	}
	return nil
}

// dedupeStrings devolve os valores não vazios, sem repetição, na ordem em que
// apareceram. Repetição num IN (...) não erra o resultado, mas gasta parâmetro
// — e o teto de parâmetros por comando é o que fatia a consulta.
func dedupeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	vistos := make(map[string]struct{}, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := vistos[v]; ok {
			continue
		}
		vistos[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
