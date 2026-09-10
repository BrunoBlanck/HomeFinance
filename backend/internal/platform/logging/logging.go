// Package logging monta o logger estruturado da aplicação.
//
// A rede de segurança principal deste pacote é a REDAÇÃO: mesmo que alguém
// logue um atributo sensível por engano, o valor não chega ao destino
// (docs/SEGURANCA.md §4 — "logs nunca com senha, token, hash, cookie").
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// Redacted é o valor que substitui qualquer atributo sensível.
const Redacted = "[REDACTED]"

// sensitiveFragments são fragmentos que, se aparecerem na CHAVE de um
// atributo (em qualquer caixa), fazem o valor ser substituído.
//
// A lista é propositalmente ampla: um falso positivo custa um log menos
// informativo; um falso negativo custa um segredo em disco.
var sensitiveFragments = []string{
	"password",
	"senha",
	"secret",
	"pepper",
	"token",
	"cookie",
	"authorization",
	"credential",
	"dsn",
	"hash",
	"otp",
	"code",
	"codigo",
	"código",
	"pin",
	"session_id",
	"sid",
	"jwt",
	"bearer",
	"apikey",
	"api_key",
	"private",
}

// IsSensitiveKey informa se uma chave de atributo deve ter o valor redigido.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, frag := range sensitiveFragments {
		if strings.Contains(k, frag) {
			return true
		}
	}
	return false
}

// Options descreve o logger desejado sem acoplar este pacote a config
// (config é lido só por cmd/api).
type Options struct {
	Level  string // debug | info | warn | error
	Format string // json | text
	// AddSource inclui arquivo/linha. Custa performance; use em dev.
	AddSource bool
}

// ParseLevel traduz o nível textual, caindo em info quando desconhecido.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New devolve um *slog.Logger que escreve em w já com a redação instalada.
func New(w io.Writer, opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{
		Level:       ParseLevel(opts.Level),
		AddSource:   opts.AddSource,
		ReplaceAttr: redact,
	}

	var h slog.Handler
	if strings.EqualFold(opts.Format, "text") {
		h = slog.NewTextHandler(w, handlerOpts)
	} else {
		h = slog.NewJSONHandler(w, handlerOpts)
	}
	return slog.New(h)
}

// redact é o ReplaceAttr instalado em todo handler criado aqui.
func redact(groups []string, a slog.Attr) slog.Attr {
	// Grupos entram recursivamente; o próprio nome do grupo também conta.
	for _, g := range groups {
		if IsSensitiveKey(g) {
			return slog.String(a.Key, Redacted)
		}
	}
	if IsSensitiveKey(a.Key) {
		// Grupos não podem virar string sem perder a estrutura: redigimos
		// cada folha ao descer, então basta marcar o valor escalar.
		if a.Value.Kind() == slog.KindGroup {
			return slog.String(a.Key, Redacted)
		}
		return slog.String(a.Key, Redacted)
	}
	return a
}

// Discard devolve um logger que não escreve nada. Útil em testes.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}
