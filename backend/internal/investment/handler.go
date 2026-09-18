package investment

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Handler expõe as duas rotas de investimento.
type Handler struct {
	svc            *Service
	lg             *slog.Logger
	trustedProxies int
}

// NewHandler monta o handler.
//
// trustedProxyCount é obrigatório porque o `detect` AUDITA, e a auditoria
// guarda o IP: lê-lo errado atrás de proxy gravaria o endereço do balanceador
// em todo rastro do mês.
func NewHandler(svc *Service, lg *slog.Logger, trustedProxyCount int) *Handler {
	if lg == nil {
		lg = slog.Default()
	}
	return &Handler{svc: svc, lg: lg, trustedProxies: trustedProxyCount}
}

// Overview responde GET /api/v1/investments.
//
// Três parâmetros são lidos: `month`, `limit` e `cursor`. Qualquer outro
// (`householdId=`, `pageSize=`, `categoryId=`…) é ignorado sem efeito — a casa
// vem do token e a série tem tamanho fixo. Nada é normalizado: " 2026-09" é
// recusado como veio, para o cliente não aprender nada sobre o servidor pela
// recusa. `Cache-Control: no-store` é global (httpserver.SecurityHeaders).
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	// O NOME do parâmetro é `limit`, e o teto é o mesmo de GET /transactions e
	// de GET /transfers — um só vocabulário de paginação no app inteiro.
	// Acima do teto é 400, nunca truncado em silêncio: truncar faria a tela
	// mostrar um conjunto diferente do que pediu sem ninguém notar (S4).
	limite, err := transaction.PageLimit(q.Get("limit"))
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{
			"limit": "Informe um número inteiro de 1 a " + strconv.Itoa(transaction.MaxAPIPageSize) + ".",
		})
		return
	}

	view, err := h.svc.Overview(r.Context(), ator, OverviewInput{
		Month:  q.Get("month"),
		Cursor: q.Get("cursor"),
		Limit:  limite,
	})
	if err != nil {
		h.fail(w, r, err, "resumindo investimentos")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// detectRequest é o corpo de POST /investments/detect (schema
// InvestmentDetectRequest).
//
// DryRun é PONTEIRO de propósito: o contrato o declara obrigatório e SEM
// default, e é o ponteiro nulo que distingue "ausente" de "false". Com um bool
// simples, um corpo sem o campo seria lido como "gravar", e gravar por campo
// esquecido é exatamente o que não pode acontecer.
//
// OverwriteCategorized também é ponteiro, por um motivo diferente: o contrato
// lhe dá `default: false`, e o ponteiro deixa o código DIZER que ausente é
// falso em vez de depender do zero value. Ele é a primeira flag do projeto que
// autoriza substituir escolha humana, e o caminho seguro tem de ser o
// explícito.
//
// Campo fora deste conjunto é 400 pelo DisallowUnknownFields — não é ignorado
// (S2, mass assignment).
type detectRequest struct {
	Month                string `json:"month"`
	DryRun               *bool  `json:"dryRun"`
	OverwriteCategorized *bool  `json:"overwriteCategorized"`
}

// Detect responde POST /api/v1/investments/detect.
//
// A prévia (dryRun) e a gravação passam pelo MESMO cálculo no servidor; a
// gravação nunca confia na prévia. O log da execução real leva request_id, o
// mês, a flag e as contagens — nunca descrição, valor, palavra-chave ou id de
// lançamento (S8).
func (h *Handler) Detect(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	body, err := httpserver.DecodeJSON[detectRequest](w, r)
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
	// Ausente é FALSE, sempre — e o ausente é o seguro.
	sobrescrever := body.OverwriteCategorized != nil && *body.OverwriteCategorized

	view, err := h.svc.Detect(r.Context(), ator, DetectInput{
		Month:                body.Month,
		DryRun:               *body.DryRun,
		OverwriteCategorized: sobrescrever,
	})
	if err != nil {
		h.fail(w, r, err, "detectando investimentos")
		return
	}

	if !*body.DryRun {
		h.lg.InfoContext(r.Context(), "investimentos detectados",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("month", view.Month),
			slog.Bool("overwrite_categorized", sobrescrever),
			slog.Int64("marked", view.Marked),
			slog.Int64("unmatched", view.Unmatched),
			slog.Int64("already_categorized", view.AlreadyCategorized),
		)
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// ator monta quem está pedindo: casa e usuário saem do contexto publicado pelo
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
// mensagem genérica e o detalhe apenas no log (docs/SEGURANCA.md §4). O log do
// 500 leva request_id, operação e a razão — NUNCA centavos, descrição, nome de
// categoria ou palavra-chave (S8).
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)

	case errors.Is(err, transaction.ErrInvalidMonth):
		httpserver.WriteValidationError(w, map[string]string{
			"month": "Informe o mês no formato AAAA-MM.",
		})

	case errors.Is(err, transaction.ErrInvalidCursor):
		// SEM detalhe (S5): o cursor é opaco, e explicar por que ele não serve
		// é ensinar a forjá-lo.
		httpserver.WriteError(w, http.StatusBadRequest, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed)

	case errors.Is(err, ErrTooManyCandidates):
		// 422, e não 400: o pedido está bem formado, mas o mês não cabe numa
		// execução. Sem o número de linhas na mensagem — o teto está no
		// contrato, e a contagem do mês é dado da casa.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"month": "Este mês tem lançamentos demais para detectar de uma vez."})

	case errors.Is(err, transaction.ErrKeywordMatchTooCostly):
		// 422 em month, como os outros tetos desta família. A mensagem aponta
		// a AÇÃO sem citar palavra, contagem ou descrição — tudo isso é dado
		// da casa.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"month": "Não consegui comparar as palavras-chave com este mês de uma vez. Reduza as palavras-chave ou faça por período menor."})

	case errors.Is(err, ErrDestinationChanged):
		// 409 CONFLICT sem campos (ADR-028d), exatamente como
		// ErrTransferConversionConflict: a categoria de destino deixou de ser
		// atribuível entre o cálculo e a escrita, a transação foi desfeita e
		// NADA foi gravado. Não é 500 — não foi o servidor que falhou — e não
		// vai para o log de erro. A ação da tela é pedir a prévia de novo.
		httpserver.WriteConflict(w, httpserver.CodeConflict, httpserver.MsgConflict, nil)

	case errors.Is(err, ErrPlanTimeout):
		// O prazo da fase de cálculo (transaction.PlanTimeout) cumpriu o papel
		// para o qual existe. É limite de TRABALHO, não falha do servidor: 422
		// em `month`, como todo outro teto desta família, com a mesma
		// orientação do importador ("período menor"). Sem esta tradução a
		// pessoa receberia 500 INTERNAL_ERROR por um limite previsto.
		//
		// ⚠️ A condição é a SENTINELA DO DOMÍNIO, e NUNCA
		// `context.DeadlineExceeded`. O gormstore soma o motivo do contexto ao
		// erro do driver (platform/storage/ctxerr.go), então, com o contexto
		// morto, QUALQUER falha de banco casa o erro de contexto: a versão
		// anterior desta linha respondia "a detecção deste mês demorou demais"
		// para falhas do SERVIDOR — culpando o mês de quem pediu — e, de
		// quebra, as fazia sumir do log de ERROR, que é o único registro delas.
		// Quem sabe que o prazo venceu é quem o impôs (conferirPrazo, em
		// detect.go), e é de lá que a sentinela vem. Mesma forma de
		// importer.ErrAnalyzeTimeout.
		//
		// É por isso, também, que este ramo é só do `detect`: o Overview não
		// impõe prazo nenhum, então um erro de contexto vindo DELE não é limite
		// que o servidor cumpriu — é falha, e cai no `default`.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"month": "A detecção deste mês demorou demais. Tente por período menor."})

	case errors.Is(r.Context().Err(), context.Canceled):
		// A pessoa fechou a aba no meio da execução. Isso é comportamento
		// NORMAL de cliente, não incidente: registrar em ERROR poluiria com
		// ruído o log que existe para mostrar sinal de segurança. Fica em INFO,
		// com a mesma forma do ramo de 500 e sem nada da casa.
		//
		// ⚠️ Quem decide é o CONTEXTO DA REQUISIÇÃO, e NUNCA
		// `errors.Is(err, context.Canceled)`. É o gêmeo do defeito do prazo,
		// logo acima, e era o pior dos dois: o ctxerr.go soma o motivo do
		// contexto ao erro do driver, então uma falha de banco ocorrida
		// enquanto o cliente ia embora — "database disk image is malformed" com
		// o contexto já cancelado — casava esta condição e virava
		// "cliente desistiu no meio". A pergunta "a pessoa desistiu?" é um fato
		// do AMBIENTE da requisição; `r.Context().Err()` responde a ela, o erro
		// só diz que passou perto de um contexto morto.
		//
		// E o `reason` vai JUNTO, de propósito: a desistência do cliente muda o
		// NÍVEL do registro, nunca pode fazer o registro sumir. Sem ele — como
		// esta linha era antes — a falha de servidor não só era classificada
		// errado: desaparecia por completo do log, que é o único lugar onde ela
		// existia. A regra vale para os dois ramos: nenhuma falha do servidor é
		// reportada como ação do cliente, e nenhuma some.
		//
		// A resposta continua sendo o 500 genérico do enum fechado do
		// contrato: ninguém a lê (a conexão já foi embora) e inventar um
		// status fora do enum publicado seria pior do que escrever um que não
		// chega a lugar nenhum.
		h.lg.InfoContext(r.Context(), "investimentos: cliente desistiu no meio",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)

	default:
		// transaction.ErrTooManyCategories cai AQUI de propósito, e o ADR-029
		// (j.2) manda escrevê-lo: o nome convida ao engano, mas ele conta
		// CATEGORIAS da própria casa, que já têm teto próprio (200) — passar
		// disso significa que a taxonomia foi violada, o servidor não sabe
		// mais o que é a taxonomia da casa, e qualquer resposta seria
		// invenção. Um 422 apontaria um campo que a pessoa não tem como
		// corrigir naquele pedido. 500 genérico, e a razão só no log.
		h.lg.ErrorContext(r.Context(), "falha em investimentos",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
	}
}
