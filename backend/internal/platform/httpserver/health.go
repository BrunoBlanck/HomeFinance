package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Pinger é o mínimo que o readiness precisa saber sobre a dependência.
type Pinger interface {
	Ping(ctx context.Context) error
}

type healthBody struct {
	Status string `json:"status"`
}

const readyProbeTimeout = 2 * time.Second

// Health responde a sonda de liveness.
//
// D17 da spec 0001: NÃO toca o banco. Um health que consulta o banco vira
// amplificador de negação de serviço. Também não expõe versão, driver nem
// qualquer detalhe de ambiente.
func Health() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, healthBody{Status: "ok"})
	}
}

// Ready responde a sonda de readiness: faz ping no banco com timeout curto.
// Em caso de falha devolve 503 sem nenhum detalhe do erro — o motivo vai só
// para o log (docs/SEGURANCA.md §4).
func Ready(p Pinger, lg *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p == nil {
			WriteError(w, http.StatusServiceUnavailable, CodeServiceUnavailable, MsgServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), readyProbeTimeout)
		defer cancel()

		if err := p.Ping(ctx); err != nil {
			if lg != nil {
				lg.WarnContext(ctx, "readiness falhou",
					slog.String("request_id", RequestIDFromContext(ctx)),
					slog.String("reason", err.Error()),
				)
			}
			WriteError(w, http.StatusServiceUnavailable, CodeServiceUnavailable, MsgServiceUnavailable)
			return
		}
		WriteJSON(w, http.StatusOK, healthBody{Status: "ok"})
	}
}
