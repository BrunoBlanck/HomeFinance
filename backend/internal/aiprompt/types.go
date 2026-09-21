// Package aiprompt monta o TEXTO que a pessoa copia no menu IA e cola numa IA
// de terceiros (spec 0010, entrega E9a). Leitura pura.
//
// # O pacote não fala com IA, e não escreve
//
// Não existe aqui cliente de IA, chave de API nem saída de rede: a integração
// é FORA do processo — o produto produz texto e, na fatia irmã
// (`internal/aiimport`), consome JSON. O transporte é a pessoa.
//
// E, como o painel (ADR-031a), este pacote não tem `Transactor` nem `Auditor`
// no construtor. Isso não é economia: é a trava que torna "o export não
// escreve" VERIFICÁVEL PELO COMPILADOR em vez de convenção de arquivo
// (§10.3 da spec 0010). Um `internal/ai` com os dois lados no mesmo pacote
// teria a escrita a um import de distância.
//
// # Interfaces declaradas no CONSUMIDOR
//
// Como `internal/classify` e `internal/dashboard`, este pacote declara o que
// precisa de lançamento, de categoria e de conta — e nada mais. Os
// repositórios que já existem satisfazem as três interfaces sem uma linha
// nova: nenhum tipo do ORM atravessa a fronteira (ADR-008 — o portão é
// TEXTUAL, em gormstore/sqlsafety_test.go, e nem em comentário se cita o
// handle), e a montagem é em cmd/api/main.go.
//
// # Regras que o pacote sustenta
//
//   - a casa vem do TOKEN, sempre (docs/SEGURANCA.md §2; BOLA é o risco nº 1).
//     `fromMonth` e `toMonth` são os ÚNICOS parâmetros lidos da requisição, e
//     nenhum id de recurso é aceito de fora — não há o que apontar;
//   - MINIMIZAÇÃO (§3.1 da spec 0010, normativa): o texto gerado não contém
//     id da casa, nome ou e-mail de ninguém, saldo de conta, instituição,
//     agência, número de conta, dia de fechamento/vencimento de fatura nem id
//     de lançamento. Da conta saem QUATRO campos — id, nome, tipo, palavras —
//     e o teste é de VARREDURA NEGATIVA sobre o texto, não inspeção visual;
//   - conta e categoria ARQUIVADAS não são nomeadas em lugar nenhum do texto;
//   - `kindGroup` é derivado com transaction.MarcadaComoInvestimento —
//     IMPORTADO, nunca reescrito (ADR-031f). Uma segunda cópia do predicado
//     divergiria da do painel e da listagem, e o mesmo lançamento apareceria
//     como investimento numa tela e como despesa noutra;
//   - dinheiro é int64 em CENTAVOS (ADR-003) em todo o caminho: a conversão
//     para reais acontece UMA vez, na formatação do texto, e nenhum float
//     entra em nenhuma soma;
//   - o mês é COMPETÊNCIA (ADR-023c), nunca caixa, e nada é normalizado: um
//     mês malformado é recusado como veio.
package aiprompt

import (
	"context"
	"errors"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// maxDescriptionsInPrompt é o teto de DESCRIÇÕES DISTINTAS que o prompt lista
// na seção 8.
//
// O número é MEDIDO, não arbitrado (lição de 18/09/2026 — o custo do corte é
// medido, nunca estimado). Medição de 21/09/2026 sobre a janela máxima de 3
// meses de competência:
//
//   - banco de desenvolvimento real ....... 78 descrições distintas (7,68 KB
//     de tabela de movimentações);
//   - casa pesada realista (750 lançamentos na janela) ..... 264 distintas,
//     56 KB de tabela.
//
// O teto de 500 corta ZERO nos dois corpora — quase o dobro da casa pesada e
// mais de seis vezes o banco real —, e existe para o caso que nenhum dos dois
// mede: a casa que importa anos de extrato de uma vez. Ele é diferente, e
// menor, do que transaction.MaxDescriptionGroupRows (5.000): aquele é o rail
// de MEMÓRIA do processo na agregação e responde 422; este é o tamanho do
// TEXTO que uma IA consegue ler com atenção, e responde CORTANDO — por
// ocorrências decrescentes, e declarando no próprio prompt quantas ficaram de
// fora (§3.1 da spec 0010).
//
// Corte silencioso seria o pior desfecho: a IA responderia com confiança sobre
// um extrato que não é o da pessoa. Por isso o número aparece nos DOIS lugares
// — no texto do prompt e em `stats.truncatedDescriptions` —, e a tela não é a
// única a saber.
const maxDescriptionsInPrompt = 500

// Erros de domínio. Nenhum carrega dado da casa nem detalhe interno.
var (
	// ErrUnauthenticated — ator sem casa. O handler já barrou antes; a guarda
	// no serviço é defesa em profundidade, para que nenhuma consulta rode com
	// household_id vazio.
	ErrUnauthenticated = errors.New("autenticação necessária")

	// ErrInvalidFromMonth e ErrInvalidToMonth — mês ausente, vazio ou fora de
	// "AAAA-MM". São DOIS erros, e não um, porque o contrato manda apontar o
	// campo errado (`fields.fromMonth` / `fields.toMonth`) e a tela destaca um
	// dos dois seletores.
	ErrInvalidFromMonth = errors.New("mês inicial inválido")
	ErrInvalidToMonth   = errors.New("mês final inválido")

	// ErrWindowInverted — `toMonth` anterior a `fromMonth`. Recusa, nunca
	// troca silenciosa dos dois: inverter por conta própria devolveria um
	// prompt de um período que ninguém pediu.
	ErrWindowInverted = errors.New("mês final anterior ao inicial")

	// ErrWindowTooLong — a janela passa de transaction.MaxCompetenceMonthsInWindow
	// meses inclusive (emenda §10, achado A2 da spec 0010).
	ErrWindowTooLong = errors.New("janela de competência longa demais")
)

// --- Interfaces declaradas no CONSUMIDOR (ADR-027a) ------------------------

// Ledger é o que o prompt precisa do domínio de lançamentos: UMA leitura
// agregada. Satisfeita por gormstore.TransactionRepository sem nenhuma adição
// à interface transaction.Repository — o método é dele, não do domínio.
//
// Repare no que NÃO está aqui: não há ByID, não há listagem de lançamento,
// não há nada que devolva uma linha individual. O prompt não tem como citar um
// lançamento porque não tem como obter um.
type Ledger interface {
	// GroupByDescription agrega os lançamentos VIVOS da casa, na janela de
	// competência, por (descrição normalizada, kind, conta, categoria).
	//
	// A semântica é TUDO OU NADA acima de `limit`:
	// transaction.ErrTooManyDescriptionGroups, nunca uma resposta parcial.
	GroupByDescription(ctx context.Context, householdID string,
		competenceMonths []string, limit int) ([]transaction.DescriptionGroup, error)
}

// Categories é o que o prompt precisa do domínio de categorias.
// gormstore.CategoryRepository já a satisfaz.
type Categories interface {
	// List devolve as categorias da casa. Este pacote chama SEMPRE com
	// includeArchived = true, e usa a lista para DUAS coisas deliberadamente
	// diferentes:
	//
	//   - NOMEAR (seção 7 do prompt e a coluna "categoria atual"): só as
	//     ATIVAS. Arquivada não aparece no texto — critério 6 da spec 0010;
	//   - derivar `kindGroup` pelo predicado de investimento: com as
	//     ARQUIVADAS, como no painel, no relatório e na listagem. Arquivar uma
	//     categoria não desfaz a marcação do passado (PLANOS.md §4.4), e o
	//     conjunto que marca os lançamentos tem de ser o MESMO em todas as
	//     telas. O `kindGroup` é uma PALAVRA ("investimento"), não um nome:
	//     derivá-lo da arquivada não a nomeia.
	List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error)

	// ListKeywords devolve TODAS as palavras-chave de categoria da casa, em
	// uma consulta. As de dona arquivada são descartadas aqui, porque a dona
	// não aparece no prompt.
	ListKeywords(ctx context.Context, householdID string) ([]category.Keyword, error)
}

// Accounts é o que o prompt precisa do domínio de contas.
// gormstore.AccountRepository já a satisfaz.
type Accounts interface {
	// List devolve as contas da casa. Este pacote chama SEMPRE com
	// includeArchived = false — e a assimetria com Categories é deliberada: a
	// conta entra no prompt só para ser NOMEADA (seção 6 e a coluna "contas"),
	// e nenhuma regra do texto depende do tipo de uma conta arquivada. Pedir
	// as arquivadas só criaria a chance de nomear uma.
	List(ctx context.Context, householdID string, includeArchived bool) ([]account.Account, error)

	// ListKeywords devolve TODAS as palavras-chave de conta da casa, em uma
	// consulta.
	ListKeywords(ctx context.Context, householdID string) ([]account.Keyword, error)
}

// --- Entradas --------------------------------------------------------------

// Actor é quem pede o prompt. Casa e usuário vêm do TOKEN, nunca da
// requisição. Sem IP: leitura não audita (ADR-031a).
type Actor struct {
	HouseholdID string
	UserID      string
}

// ExportInput é a entrada de GET /ai/export-prompt, como veio da query. A
// validação acontece no serviço, antes de qualquer consulta, e nada é
// normalizado: " 2026-07" e "2026-7" são recusados como vieram, para a recusa
// não ensinar nada sobre o servidor.
type ExportInput struct {
	FromMonth string
	ToMonth   string
}

// --- DTO de resposta (docs/SEGURANCA.md §4: nunca a entidade direto) -------

// StatsView são as contagens ao lado do prompt (schema AiExportStats).
//
// Elas existem para a pessoa ver o TAMANHO do que está prestes a colar em
// outro lugar ANTES de colar — e para `truncatedDescriptions` não viver só
// dentro do texto.
type StatsView struct {
	// Accounts e Categories contam o que foi LISTADO: contas e categorias
	// ativas, grupos inclusive. Arquivada não entra em nenhuma das duas.
	Accounts   int `json:"accounts"`
	Categories int `json:"categories"`

	// Descriptions são as descrições distintas que SOBRARAM no prompt, já
	// descontado o corte; Transactions são os lançamentos vivos agrupados
	// NESSAS descrições — as duas contam o texto entregue, não o corpus.
	Descriptions int `json:"descriptions"`
	Transactions int `json:"transactions"`

	// TruncatedDescriptions é quanto ficou de fora por maxDescriptionsInPrompt,
	// cortado por ocorrências decrescentes. Zero quando o corpus coube inteiro.
	TruncatedDescriptions int `json:"truncatedDescriptions"`
}

// PromptView é a resposta de GET /ai/export-prompt (schema AiExportPrompt).
//
// CINCO campos, todos obrigatórios e sempre presentes: o schema é
// `additionalProperties: false`, e a tela exibe, copia e baixa o MESMO texto,
// byte a byte. A rota não devolve lançamento, valor solto nem estrutura para a
// tela remontar o prompt — remontar seria uma segunda fonte do mesmo texto.
type PromptView struct {
	Prompt string `json:"prompt"`

	// FromMonth e ToMonth voltam como vieram (já validados), para a tela
	// nomear o arquivo do download sem inventar nada.
	FromMonth string `json:"fromMonth"`
	ToMonth   string `json:"toMonth"`

	// GeneratedAt é o instante ISO 8601 UTC da geração: o prompt é um retrato,
	// e ele diz de quando.
	GeneratedAt string `json:"generatedAt"`

	Stats StatsView `json:"stats"`
}
