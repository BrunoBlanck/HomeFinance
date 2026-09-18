package transaction

import (
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// View é o lançamento no formato da API.
//
// DTO próprio de propósito: a entidade NUNCA é serializada (docs/SEGURANCA.md
// §4). É por isso que dedupKey, dedupOrdinal, externalId, householdId,
// descriptionNorm e deletedAt não estão aqui — nenhum deles interessa à tela, e
// dedupKey e externalId são exatamente o tipo de campo que vaza sem que
// ninguém perceba, porque "estava na struct".
//
// A forma é a do schema Transaction do backend/api/openapi.yaml, que é
// additionalProperties:false — campo a mais aqui é divergência de contrato, não
// bônus.
type View struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`

	// CategoryID e CategoryName são anuláveis: lançamento importado nasce SEM
	// categoria (D3 da spec 0004), e transferência não tem categoria por
	// desenho (ADR-016). A tela mostra o estado "sem categoria" como pendência,
	// e o summary o conta.
	CategoryID   *string `json:"categoryId"`
	CategoryName *string `json:"categoryName"`

	// AmountCents é sempre não negativo — o sinal vem do Kind (ADR-003).
	AmountCents int64      `json:"amountCents"`
	Description string     `json:"description"`
	OccurredOn  civil.Date `json:"occurredOn"`

	// YearMonth é o mês de CAIXA, derivado de OccurredOn. Vai no payload por
	// completude; nenhuma tela do v1 o usa, porque o mês do app é a competência
	// (ADR-023c).
	YearMonth string `json:"yearMonth"`

	// CompetenceMonth é o mês pelo qual GET /transactions?month= filtra.
	CompetenceMonth string `json:"competenceMonth"`

	TransferGroupID *string `json:"transferGroupId"`
	StatementID     *string `json:"statementId"`
	Source          string  `json:"source"`
	ImportBatchID   *string `json:"importBatchId"`
	CreatedBy       string  `json:"createdBy"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
}

// SummaryView são os totais do MESMO filtro da listagem.
//
// Vêm no mesmo payload de propósito (§7.2 do PLANOS.md): somar centavos também
// no cliente criaria duas fontes para o mesmo número, e duas fontes divergem.
type SummaryView struct {
	IncomeCents  int64 `json:"incomeCents"`
	ExpenseCents int64 `json:"expenseCents"`
	NetCents     int64 `json:"netCents"`
	Count        int64 `json:"count"`

	// UncategorizedCount é o gancho da faixa "N lançamentos sem categoria".
	// Ele existe por causa da D3 (importado nasce sem categoria): sem o número
	// na tela, a dívida que a D3 cria vira invisível.
	UncategorizedCount int64 `json:"uncategorizedCount"`

	// InvestedCents e RedeemedCents são o dinheiro que SAIU de ExpenseCents e
	// de IncomeCents por ter categoria de natureza `investment`/`redemption`
	// (ADR-029e). Estão SEMPRE presentes — zero quando a casa não marca nada —
	// porque o contrato os declara required e a tela não distingue "ausente"
	// de "nenhum".
	//
	// A tela é OBRIGADA a mostrá-los quando forem maiores que zero: o total do
	// mês encolheu por causa deles, e um total que encolhe sem explicação é
	// mentira por omissão. Os dois nunca são negativos — conferido em
	// conferirResumo antes de chegar aqui.
	InvestedCents int64 `json:"investedCents"`
	RedeemedCents int64 `json:"redeemedCents"`
}

// ListView é a resposta de GET /transactions.
type ListView struct {
	Items []View `json:"items"`

	// NextCursor é nulo quando não há mais página, e está SEMPRE presente
	// (nulo, se for o caso) para a tela não precisar distinguir "ausente" de
	// "acabou".
	NextCursor *string `json:"nextCursor"`

	Summary SummaryView `json:"summary"`
}

// toView monta o DTO a partir da entidade, resolvendo os nomes de conta e de
// categoria pelos mapas já carregados.
//
// Nome que não está no mapa vira vazio (ou nulo, na categoria) em vez de erro:
// a conta pode ter sido excluída logicamente depois do lançamento, e o
// lançamento continua existindo e valendo dinheiro. Falhar a listagem inteira
// por causa de um rótulo seria trocar um defeito cosmético por uma tela vazia.
func toView(t Transaction, contas map[string]string, categorias map[string]string) View {
	v := View{
		ID:              t.ID,
		Kind:            t.Kind,
		AccountID:       t.AccountID,
		AccountName:     contas[t.AccountID],
		AmountCents:     t.AmountCents,
		Description:     t.Description,
		OccurredOn:      t.OccurredOn,
		YearMonth:       t.OccurredOn.YearMonth(),
		CompetenceMonth: t.CompetenceMonth,
		TransferGroupID: t.TransferGroupID,
		StatementID:     t.StatementID,
		Source:          t.Source,
		ImportBatchID:   t.ImportBatchID,
		CreatedBy:       t.CreatedBy,
		CreatedAt:       t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:       t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.CategoryID != nil {
		id := *t.CategoryID
		v.CategoryID = &id
		if nome, ok := categorias[id]; ok {
			v.CategoryName = &nome
		}
	}
	return v
}

// toSummaryView converte o agregado do repositório no DTO.
func toSummaryView(s Summary) SummaryView {
	return SummaryView{
		IncomeCents:        s.IncomeCents,
		ExpenseCents:       s.ExpenseCents,
		NetCents:           s.NetCents,
		Count:              s.Count,
		UncategorizedCount: s.Uncategorized,
		InvestedCents:      s.InvestedCents,
		RedeemedCents:      s.RedeemedCents,
	}
}
