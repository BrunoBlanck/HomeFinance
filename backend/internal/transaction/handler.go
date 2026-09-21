package transaction

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
)

// MaxAPIPageSize é o teto de `limit` publicado no contrato para
// GET /transactions e para as linhas de uma fatura.
//
// É MENOR que MaxPageSize (200), que é o teto do domínio: a API publica 100, e
// publicar um número é prometê-lo. Acima disso é 400 — nunca truncado em
// silêncio, porque truncar faria a tela mostrar um conjunto diferente do que
// pediu sem ninguém notar (S4).
const MaxAPIPageSize = 100

// msgKindGroup é a ÚNICA redação do 400 do filtro de tipo (spec 0004 §12,
// emenda E2d).
//
// Ela cita a ALLOWLIST e nunca o valor recebido. Ecoar a entrada numa
// mensagem de erro é como uma query mal formada vira XSS refletido no cliente
// que a renderiza sem escapar — e, aqui, seria ainda um convite a usar o campo
// como canal de teste. Pelo mesmo motivo nada é logado neste caminho: o valor
// recusado é entrada bruta de terceiro (S8).
//
// Mora numa constante porque a borda e o `fail` precisam dizer a MESMA coisa:
// duas redações divergiriam, e a segunda seria a que ninguém revisou.
const msgKindGroup = "Informe um destes tipos: income, expense, transfer ou investment."

// msgKindGroupRepetido é a redação do 400 da chave REPETIDA (spec 0004
// §12.5.5) — e é própria, não a de cima.
//
// Reaproveitar msgKindGroup diria "Informe um destes tipos: …" para quem
// informou DOIS tipos válidos: instrução errada para o defeito real, e a
// pessoa tentaria de novo com um valor que já estava certo. Esta aponta a
// AÇÃO, e é tudo o que ela faz: não ecoa nenhum dos valores recebidos e não
// diz quantas ocorrências chegaram — contar para o cliente o que ele mandou é
// devolver a entrada dele pela porta do erro.
const msgKindGroupRepetido = "Informe o tipo uma única vez."

// msgCategoriaRepetida é o 400 da chave `categoryId` repetida. Mesma recusa
// de ambiguidade de msgKindGroupRepetido, mesma economia: não ecoa nenhum dos
// ids recebidos e não diz quantos chegaram.
const msgCategoriaRepetida = "Informe a categoria uma única vez."

// Handler expõe os endpoints de lançamento.
type Handler struct {
	svc            *Service
	lg             *slog.Logger
	trustedProxies int
}

// NewHandler monta o handler.
func NewHandler(svc *Service, lg *slog.Logger, trustedProxyCount int) *Handler {
	return &Handler{svc: svc, lg: lg, trustedProxies: trustedProxyCount}
}

// List responde GET /api/v1/transactions.
//
// `month` é OBRIGATÓRIO e filtra COMPETÊNCIA (ADR-023c). Sem ele a consulta
// viraria "todos os lançamentos da casa", que é a página que fica lenta
// primeiro e a que ninguém pediu.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	limite, err := PageLimit(q.Get("limit"))
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{
			"limit": "Informe um número inteiro de 1 a " + strconv.Itoa(MaxAPIPageSize) + ".",
		})
		return
	}

	// O filtro de TIPO é conferido AQUI, na borda, em duas perguntas
	// independentes e nesta ordem.
	//
	// (1) A chave é INEQUÍVOCA? `?kindGroup=expense&kindGroup=income` é 400
	// (spec 0004 §12.5.5). `url.Values.Get` devolveria o PRIMEIRO valor em
	// silêncio, e "o primeiro vence" é uma opinião que proxy, WAF, coletor de
	// log e cliente não são obrigados a compartilhar: a MESMA URL viraria
	// listas diferentes conforme quem a lê. A recusa é da AMBIGUIDADE, não da
	// discordância — valores iguais e segunda ocorrência vazia também são 400,
	// e a segunda vazia é a pior das três (quem lê a última obtém "Tudo", o
	// filtro evapora e a tela mostra o mês inteiro sob o rótulo "Despesas").
	// UMA ocorrência vazia continua sendo Tudo, 200: uma ocorrência não é
	// ambígua.
	//
	// A mensagem é PRÓPRIA, e não a da allowlist: quem mandou dois tipos
	// VÁLIDOS precisa ler "informe uma única vez", e não "informe um destes
	// tipos" — que seria instrução errada para o defeito real. Ela não ecoa
	// valor e não diz quantas ocorrências chegaram.
	//
	// (2) O valor está na ALLOWLIST fechada do domínio (spec 0004 §12)?
	// Ausente e vazio passam — os dois são "Tudo" —, e qualquer outra coisa
	// para no 400 sem chegar ao serviço.
	//
	// Nenhuma das duas repete o que veio: `?kindGroup=transfer_out` e
	// `?kindGroup=expense' OR 1=1 --` recebem exatamente a mesma mensagem, que
	// é a lista dos quatro valores aceitos. O valor recusado também não vai
	// para o log.
	grupo, err := httpserver.SoleQueryValue(q, "kindGroup")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"kindGroup": msgKindGroupRepetido})
		return
	}
	if !ValidKindGroup(grupo) {
		httpserver.WriteValidationError(w, map[string]string{"kindGroup": msgKindGroup})
		return
	}

	// O filtro de CATEGORIA passa pela mesma pergunta (1) do de tipo: a chave
	// é inequívoca? `?categoryId=A&categoryId=B` é 400 pelo mesmo motivo —
	// "o primeiro vence" é opinião de quem lê, e a MESMA URL viraria listas
	// diferentes. A pergunta (2), "existe e é desta casa?", não tem allowlist
	// para ser feita aqui: ela é do serviço, que responde 404 (S1) sem dizer
	// se o id existe em outra casa.
	categoria, err := httpserver.SoleQueryValue(q, "categoryId")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"categoryId": msgCategoriaRepetida})
		return
	}

	view, err := h.svc.List(r.Context(), ator, ListInput{
		Month:      q.Get("month"),
		AccountID:  q.Get("accountId"),
		KindGroup:  grupo,
		CategoryID: categoria,
		Cursor:     q.Get("cursor"),
		Limit:      limite,
	})
	if err != nil {
		h.fail(w, r, err, "listando lançamentos")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Get responde GET /api/v1/transactions/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}
	view, err := h.svc.ByID(r.Context(), ator, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err, "buscando lançamento")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Delete responde DELETE /api/v1/transactions/{id}.
//
// Exclusão LÓGICA, e em transferência o PAR INTEIRO sai (ADR-016) — a tela
// precisa dizer isso em texto antes de confirmar.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}
	if err := h.svc.SoftDelete(r.Context(), ator, r.PathValue("id")); err != nil {
		h.fail(w, r, err, "excluindo lançamento")
		return
	}
	httpserver.WriteNoContent(w)
}

// updateCategoryRequest é o corpo de PATCH /transactions/{id} (schema
// UpdateTransactionCategoryRequest, spec 0005 §11).
//
// UM campo, de propósito: a emenda só troca categoria. Valor, data, descrição
// e conta não existem aqui, e mandá-los é 400 pelo DisallowUnknownFields —
// não são ignorados (S2, mass assignment). CategoryID é PONTEIRO para
// distinguir "ausente" de "vazio": os dois são 400 no campo, mas o contrato
// declara o campo obrigatório e sem default, e ausente precisa ser recusado
// como tal.
type updateCategoryRequest struct {
	CategoryID *string `json:"categoryId"`
}

// UpdateCategory responde PATCH /api/v1/transactions/{id} (spec 0005 §11).
//
// A forma do id da categoria é conferida AQUI, na borda: vazio, ausente ou
// fora da forma canônica de UUID é 400 em `fields.categoryId`, sem tocar no
// serviço. Existência, casa, arquivamento e natureza são do serviço, dentro
// da transação — e categoria de outra casa é o MESMO 404 de inexistente (S1).
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	body, err := httpserver.DecodeJSON[updateCategoryRequest](w, r)
	if err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}
	if body.CategoryID == nil || !looksLikeUUID(*body.CategoryID) {
		httpserver.WriteValidationError(w, map[string]string{
			"categoryId": "Informe a categoria.",
		})
		return
	}

	view, err := h.svc.UpdateCategory(r.Context(), ator, r.PathValue("id"), *body.CategoryID)
	if err != nil {
		h.fail(w, r, err, "categorizando lançamento")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// autoCategorizeRequest é o corpo de POST /transactions/auto-categorize
// (schema AutoCategorizeRequest). DryRun é PONTEIRO de propósito: o contrato
// o declara obrigatório e sem default, e é o ponteiro nulo que distingue
// "ausente" de "false" — com bool, um corpo sem o campo seria lido como
// "gravar", e gravar por campo esquecido é o que não pode acontecer.
type autoCategorizeRequest struct {
	Month  string `json:"month"`
	DryRun *bool  `json:"dryRun"`
}

// AutoCategorize responde POST /api/v1/transactions/auto-categorize
// (spec 0005 §4.3).
//
// A prévia (dryRun) e a gravação passam pelo MESMO cálculo no servidor; a
// gravação nunca confia na prévia. O log da execução real leva request_id, o
// mês e as contagens — nunca descrição, valor ou palavra-chave (S8).
func (h *Handler) AutoCategorize(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	body, err := httpserver.DecodeJSON[autoCategorizeRequest](w, r)
	if err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}
	if body.DryRun == nil {
		httpserver.WriteValidationError(w, map[string]string{
			"dryRun": "Informe true para a prévia ou false para gravar.",
		})
		return
	}

	view, err := h.svc.AutoCategorize(r.Context(), ator, AutoCategorizeInput{
		Month:  body.Month,
		DryRun: *body.DryRun,
	})
	if err != nil {
		h.fail(w, r, err, "auto-categorizando lançamentos")
		return
	}

	// `match_work` é quanto do orçamento de casamento por palavra-chave a
	// operação gastou (achado A6): é o número que diz se a folga de
	// textmatch.MaxMatchWork é real contra o tráfego, e não só contra os
	// cenários do teste. Vai nas DUAS linhas porque a prévia gasta o mesmo
	// tanto que a gravação — e é ela que um abuso repetiria.
	if *body.DryRun {
		h.lg.InfoContext(r.Context(), "prévia de auto-categorização calculada",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("month", view.Month),
			slog.Int64("match_work", view.MatchWorkSpent),
		)
	} else {
		h.lg.InfoContext(r.Context(), "lançamentos categorizados automaticamente",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("month", view.Month),
			slog.Int64("categorized", view.Categorized),
			slog.Int64("unmatched", view.Unmatched),
			slog.Int64("match_work", view.MatchWorkSpent),
		)
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// detectTransfersRequest é o corpo de POST /transfers/detect (schema
// TransferDetectRequest). DryRun é PONTEIRO pelo mesmo motivo de
// autoCategorizeRequest: o contrato o declara obrigatório e sem default, e é
// o ponteiro nulo que distingue "ausente" de "false" — com bool, um corpo sem
// o campo seria lido como "converter". Sem ids: o cliente não escolhe o que
// converter (spec 0005 §13.4).
type detectTransfersRequest struct {
	Month  string `json:"month"`
	DryRun *bool  `json:"dryRun"`
}

// DetectTransfers responde POST /api/v1/transfers/detect (spec 0005 §13,
// ADR-028).
//
// A prévia (dryRun) e a conversão passam pelo MESMO cálculo no servidor; a
// conversão nunca confia na prévia. O log da execução real leva request_id,
// o mês e as contagens — nunca descrição, valor, palavra-chave ou id de
// lançamento (S8).
func (h *Handler) DetectTransfers(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	body, err := httpserver.DecodeJSON[detectTransfersRequest](w, r)
	if err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}
	if body.DryRun == nil {
		httpserver.WriteValidationError(w, map[string]string{
			"dryRun": "Informe true para a prévia ou false para converter.",
		})
		return
	}

	view, err := h.svc.DetectTransfers(r.Context(), ator, TransferDetectInput{
		Month:  body.Month,
		DryRun: *body.DryRun,
	})
	if err != nil {
		h.fail(w, r, err, "reprocessando transferências")
		return
	}

	if !*body.DryRun {
		h.lg.InfoContext(r.Context(), "transferências reprocessadas",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("month", view.Month),
			slog.Int64("paired", view.Paired),
			slog.Int64("unpaired", view.Unpaired),
		)
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// ListTransfers responde GET /api/v1/transfers (spec 0005 §4.4).
//
// Os ids de conta vão para o serviço como vieram: quem decide se são da casa
// é ele, e conta de outra casa ou inexistente é o MESMO 404 (S1). O cursor é
// o de GET /transactions e, malformado, é 400 sem detalhe (S5).
func (h *Handler) ListTransfers(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	limite, err := PageLimit(q.Get("limit"))
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{
			"limit": "Informe um número inteiro de 1 a " + strconv.Itoa(MaxAPIPageSize) + ".",
		})
		return
	}

	view, err := h.svc.ListTransfers(r.Context(), ator, TransferListInput{
		Month:                q.Get("month"),
		AccountID:            q.Get("accountId"),
		CounterpartAccountID: q.Get("counterpartAccountId"),
		Cursor:               q.Get("cursor"),
		Limit:                limite,
	})
	if err != nil {
		h.fail(w, r, err, "listando transferências")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// PageLimit lê e valida o parâmetro `limit`.
//
// Ausente devolve zero, que o serviço traduz no tamanho padrão. Presente e fora
// da faixa é ERRO: o contrato publica `maximum: 100`, e reduzir em silêncio
// entregaria menos linhas do que o cliente pediu sem dizer nada.
func PageLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if n < 1 || n > MaxAPIPageSize {
		return 0, errors.New("limit fora da faixa")
	}
	return n, nil
}

// ator monta quem está agindo: casa e usuário saem do contexto publicado pelo
// RequireAuth (ou seja, do token assinado), e o IP sai da borda HTTP. O
// household NUNCA vem de corpo, query ou path (docs/SEGURANCA.md §2).
func (h *Handler) ator(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	ident, ok := session.FromContext(r.Context())
	if !ok || ident.HouseholdID == "" {
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)
		return Actor{}, false
	}
	return Actor{
		HouseholdID: ident.HouseholdID,
		UserID:      ident.UserID,
		IP:          httpserver.ClientIP(r, h.trustedProxies),
	}, true
}

// fail traduz erro de domínio para HTTP.
//
// Só os erros conhecidos viram resposta específica; qualquer outro é 500 com
// mensagem genérica e o detalhe apenas no log (docs/SEGURANCA.md §4). O log
// registra o id do lançamento, NUNCA o valor em centavos nem a descrição:
// log é superfície de vazamento, e dinheiro e nome de terceiro não entram nele
// (S8).
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	switch {
	case errors.Is(err, ErrNotFound):
		// 404, e não 403: 403 confirmaria que o recurso existe em outra casa.
		httpserver.WriteError(w, http.StatusNotFound, httpserver.CodeNotFound, httpserver.MsgNotFound)

	case errors.Is(err, ErrInvalidCursor):
		// SEM detalhe (S5): o cursor é opaco, e explicar por que ele não serve
		// é ensinar a forjá-lo.
		httpserver.WriteError(w, http.StatusBadRequest, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed)

	case errors.Is(err, ErrInvalidMonth):
		httpserver.WriteValidationError(w, map[string]string{
			"month": "Informe o mês no formato AAAA-MM.",
		})

	case errors.Is(err, ErrUnknownKindGroup):
		// 400 em `fields.kindGroup`, com a MESMA mensagem da borda e sem o
		// valor recebido.
		//
		// A borda já recusou antes de chamar o serviço, então este caso é
		// defesa em profundidade: ele existe para o dia em que outro caminho
		// montar um ListFilter por conta própria. É 400, e não 500, porque a
		// origem do valor é sempre o cliente — diferente de
		// ErrEmptyCategoryFilter, logo abaixo, que só pode ser defeito de
		// ligação nosso.
		httpserver.WriteValidationError(w, map[string]string{
			"kindGroup": msgKindGroup,
		})

	case errors.Is(err, ErrAccountArchived):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"accountId": "Esta conta está arquivada. Desarquive-a para usá-la."})

	case errors.Is(err, ErrCategoryArchived):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"categoryId": "Esta categoria está arquivada. Desarquive-a para usá-la."})

	case errors.Is(err, ErrCategoryKindMismatch):
		// 422, não 400: o corpo está bem formado e a categoria existe na casa;
		// é a regra de negócio (relatório soma por natureza) que recusa.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"categoryId": "A natureza da categoria não combina com o lançamento."})

	case errors.Is(err, ErrCategoryIsParentGroup):
		// 422 no campo da categoria (spec 0005 §13): o grupo existe nesta casa
		// e o corpo está bem formado — o que não existe é "pendurar lançamento
		// num grupo que tem subcategorias". A mensagem diz a AÇÃO (escolher uma
		// subcategoria) sem citar nome nem id de categoria nenhuma.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"categoryId": "Este grupo tem subcategorias. Escolha uma subcategoria."})

	case errors.Is(err, ErrCategoryOnTransfer):
		// Perna de transferência nunca tem categoria (ADR-016). O campo é
		// `id` — o problema é o lançamento apontado, não a categoria.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"id": "Transferência não tem categoria."})

	case errors.Is(err, ErrCategoryRequired):
		httpserver.WriteValidationError(w, map[string]string{
			"categoryId": "Informe a categoria.",
		})

	case errors.Is(err, ErrCounterpartNeedsAccount):
		httpserver.WriteValidationError(w, map[string]string{
			"counterpartAccountId": "Informe também accountId para filtrar pela contraparte.",
		})

	case errors.Is(err, ErrSameAccountFilter):
		httpserver.WriteValidationError(w, map[string]string{
			"counterpartAccountId": "A contraparte precisa ser uma conta diferente.",
		})

	case errors.Is(err, ErrTooManyUncategorized):
		// 422, e não 400: o pedido está bem formado, mas o mês não cabe numa
		// execução. Sem o número exato de linhas na mensagem — o teto está no
		// contrato, e a contagem do mês é dado da casa.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"month": "Este mês tem lançamentos sem categoria demais para categorizar de uma vez."})

	case errors.Is(err, ErrKeywordMatchTooCostly):
		// 422 em month, como os outros tetos desta feature. A mensagem aponta
		// a AÇÃO (menos palavras-chave, ou menos linhas de uma vez) sem citar
		// palavra, contagem ou descrição — tudo isso é dado da casa.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"month": "Não consegui comparar as palavras-chave com este mês de uma vez. Reduza as palavras-chave ou faça por período menor."})

	case errors.Is(err, ErrTooManyTransfers):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"month": "Este mês tem transferências demais para somar de uma vez."})

	case errors.Is(err, ErrTooManyTransferCandidates):
		// 422 em month, sem contagem na mensagem — o teto está no contrato, e
		// a contagem do mês é dado da casa.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"month": "Este mês tem lançamentos demais para reprocessar de uma vez."})

	case errors.Is(err, ErrPlanTimeout):
		// O prazo da fase de cálculo (PlanTimeout) cumpriu o papel para o qual
		// existe. É limite de TRABALHO, não falha do servidor: 422 em `month`,
		// como todo outro teto desta família, com a mesma orientação ("período
		// menor"). Sem esta tradução a pessoa receberia 500 INTERNAL_ERROR por
		// um limite previsto — e a rota irmã POST /investments/detect, com o
		// MESMO PlanTimeout, já respondia 422 (achado A12 da revisão).
		//
		// Vale para as DUAS rotas que varrem o mês: auto-categorize e
		// detect-transfers.
		//
		// ⚠️ A condição é a SENTINELA DO DOMÍNIO, e NUNCA
		// `context.DeadlineExceeded`. O gormstore soma o motivo do contexto ao
		// erro do driver (platform/storage/ctxerr.go), então, com o contexto
		// morto, QUALQUER falha de banco casa o erro de contexto: casá-lo aqui
		// responderia "este mês demorou demais" para falhas do SERVIDOR —
		// culpando o mês de quem pediu — e, de quebra, as faria sumir do log de
		// ERROR, que é o único registro delas. Quem sabe que o prazo venceu é
		// quem o impôs (conferirPrazo, em autocategorize.go), e é de lá que a
		// sentinela vem. Mesma forma de importer.ErrAnalyzeTimeout e de
		// investment.ErrPlanTimeout.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"month": "Este mês demorou demais para processar de uma vez. Tente por período menor."})

	case errors.Is(err, ErrTransferConversionConflict), errors.Is(err, ErrCategoryChanged):
		// 409 CONFLICT sem campos (ADR-028d): a transação foi desfeita, nada
		// gravado, e a ação da tela é pedir a prévia de novo. Não é 500 —
		// não foi o servidor que falhou — e não vai para o log de erro.
		//
		// Os dois juntos porque são o MESMO acontecimento em dois pontos: o
		// estado mudou debaixo da operação entre a leitura e a escrita — o
		// lançamento, no primeiro; a categoria do plano, no segundo (achado
		// A4). É também o mesmo desfecho de investment.ErrDestinationChanged.
		httpserver.WriteConflict(w, httpserver.CodeConflict, httpserver.MsgConflict, nil)

	case errors.Is(err, ErrTooManyCategories), errors.Is(err, ErrEmptyCategoryFilter), errors.Is(err, errResumoInconsistente):
		// 500 GENÉRICO, nunca 4xx — e o caso está escrito ANTES de
		// IsValidationError de propósito, porque o nome convida ao engano
		// (ADR-029 j.2).
		//
		// ErrTooManyUncategorized é 422 em `fields.month`, mas aquele conta
		// LANÇAMENTOS, que a pessoa pode dividir em dois meses. Estes contam
		// CATEGORIAS DA PRÓPRIA CASA, que já têm teto próprio: passar de 200
		// significa taxonomia violada, e o servidor não sabe mais o que é a
		// taxonomia da casa. Um 422 apontaria um campo que a pessoa não tem
		// como corrigir naquele pedido. Mesma coisa para o conjunto vazio, que
		// é defeito de ligação (quem chama faz o curto-circuito em Go —
		// ADR-029f), e para o resumo fora da faixa publicável (ADR-029 j.1).
		//
		// O log leva as CONTAGENS que o erro embrulhou; nunca os ids, nunca
		// centavos, nunca a descrição.
		//
		// `invarianteViolada` é o que impede este log de ser REBAIXADO a INFO
		// quando a conexão cai (achado B-A12.2): ver erroInterno.
		h.erroInterno(w, r, err, contexto, invarianteViolada)

	case IsValidationError(err):
		httpserver.WriteError(w, http.StatusBadRequest, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed)

	default:
		h.erroInterno(w, r, err, contexto, falhaDeExecucao)
	}
}

// classeDeFalha diz POR QUE aquele 500 existe. É a ÚNICA coisa que decide o
// nível do log em erroInterno — a resposta ao cliente é a mesma nas duas.
//
// A classificação nasce no ponto em que o erro é RECONHECIDO (o `case` do
// fail), e não dentro de erroInterno, porque é lá que se sabe do que se trata:
// deduzi-la do erro no fim seria repetir o casamento de sentinelas em dois
// lugares, e dois lugares divergem.
type classeDeFalha int

const (
	// falhaDeExecucao: alguma coisa não funcionou nesta execução — driver,
	// rede, banco fora do ar, cliente que foi embora no meio. O estado da casa
	// continua coerente; o que falhou foi ESTE pedido.
	falhaDeExecucao classeDeFalha = iota

	// invarianteViolada: os dados da casa contradizem uma regra que o servidor
	// dá por garantida — mais categorias do que o teto do domínio permite
	// (ADR-029 j.2), resumo fora da faixa publicável (ADR-029 j.1), filtro de
	// categorias vazio por defeito de ligação (ADR-029f). Nada "falhou": o
	// servidor DESCOBRIU que não sabe mais o que é a taxonomia da casa, e
	// fechou. É sinal de CORRUPÇÃO, e por isso nunca é rebaixado a INFO.
	invarianteViolada
)

// erroInterno é a resposta 500 do pacote: UMA linha de log com o contexto e a
// razão, e uma mensagem genérica para o cliente (docs/SEGURANCA.md §4).
//
// Existe como função para que os erros que falham FECHADOS por decisão
// explícita (ADR-029 j.1/j.2) usem exatamente o mesmo caminho do `default` —
// sem uma segunda forma de escrever 500 que possa divergir dele. A resposta é
// escrita em UM lugar só, no fim, justamente para isso.
//
// O NÍVEL do log é o que esta função decide, e só ele.
func (h *Handler) erroInterno(w http.ResponseWriter, r *http.Request, err error, contexto string, classe classeDeFalha) {
	// O cliente foi embora no meio (aba fechada, app encerrado, rede caída).
	//
	// ⚠️ Quem decide isto é o CONTEXTO DA REQUISIÇÃO, e JAMAIS o erro. Com o
	// embrulho do gormstore (platform/storage/ctxerr.go), qualquer falha de
	// driver sob contexto morto casa `errors.Is(err, context.Canceled)`, e
	// perguntar ao ERRO rebaixaria para INFO um defeito de servidor — é o
	// achado B4 que a revisão abriu contra a outra entrega. O `context`
	// importado aqui serve a UMA coisa: comparar o motivo do contexto da
	// REQUISIÇÃO. `errors.Is(err, context.…)` é proibido nesta borda.
	//
	// ⚠️ E o motivo tem de ser `context.Canceled`, não "qualquer motivo"
	// (achado B-A12.1): cancelamento é o cliente indo embora; prazo vencido é
	// orçamento do SERVIDOR estourando. Hoje só o primeiro chega aqui, porque o
	// net/http não põe prazo em `r.Context()` e o único prazo desta fase é o
	// PlanTimeout imposto lá dentro (conferido em internal/platform/httpserver
	// e cmd/api) — é a MESMA invariante anotada em conferirPrazo
	// (autocategorize.go), e os dois pontos quebram juntos no dia em que
	// existir middleware de prazo por requisição. `!= nil` gravaria esse
	// estouro como "cliente desistiu no meio": uma afirmação FALSA sobre o
	// cliente, em INFO, sobre um incidente real. Mesma condição de
	// importer/handler.go.
	clienteFoiEmbora := errors.Is(r.Context().Err(), context.Canceled)

	// INFO só para RUÍDO DE CLIENTE, e por isso a classe entra na condição
	// (achado B-A12.2). Os dois conjuntos são tratados diferente de propósito:
	//
	//   - falha de EXECUÇÃO com o cliente já fora é comportamento NORMAL de
	//     cliente. Uma linha de ERROR por aba fechada polui exatamente o log
	//     que existe para mostrar sinal de segurança — e a prévia destas rotas
	//     é repetível até 60×/h por casa, ou seja, é ruído SOB DEMANDA do
	//     cliente;
	//   - INVARIANTE VIOLADA não. Ela afirma que a taxonomia da casa está
	//     quebrada, e isso não deixa de ser verdade porque o socket fechou. Sem
	//     esta distinção, bastaria provocar a condição e fechar a conexão logo
	//     em seguida para a quebra sair como INFO — e todo alerta apoiado em
	//     `level=ERROR` ou na mensagem "falha em lançamento" perderia o evento.
	//
	// A falha continua registrada nos dois casos, com o MESMO `reason`: nada
	// some do log, só muda de nível. Mesma forma de importer/handler.go.
	if clienteFoiEmbora && classe == falhaDeExecucao {
		h.lg.InfoContext(r.Context(), "lançamento: cliente desistiu no meio",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
	} else {
		// `client_gone` preserva no ERROR a informação que o ramo de INFO
		// carregava no texto: ninguém leu esta resposta. Sem ele, a quebra de
		// invariante com a conexão já fechada ficaria indistinguível de uma com
		// o cliente esperando — e é útil saber que houve um socket fechado
		// junto. É um booleano: não carrega dado da casa.
		h.lg.ErrorContext(r.Context(), "falha em lançamento",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
			slog.Bool("client_gone", clienteFoiEmbora),
		)
	}

	// A resposta é a MESMA nos dois ramos, e é escrita uma vez só: o 500
	// genérico do enum FECHADO do contrato. Quando o cliente já foi, ninguém a
	// lê — e inventar um status fora do enum publicado seria pior do que
	// escrever um que não chega a lugar nenhum.
	httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
}
