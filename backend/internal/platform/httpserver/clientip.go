package httpserver

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP devolve o IP do cliente para efeito de rate limit e auditoria.
//
// D16 da spec 0001: a origem de verdade é sempre RemoteAddr.
// X-Forwarded-For só é considerado quando trustedProxyCount > 0, e mesmo
// assim pegamos a N-ésima entrada A PARTIR DA DIREITA — as entradas à
// esquerda são escritas pelo cliente e não valem nada.
//
// Confiar em XFF por padrão anularia todo o rate limit por IP: qualquer
// atacante mandaria um header diferente por requisição.
func ClientIP(r *http.Request, trustedProxyCount int) string {
	remote := remoteIP(r.RemoteAddr)
	if trustedProxyCount <= 0 {
		return remote
	}

	entries := forwardedEntries(r)
	if len(entries) == 0 {
		return remote
	}

	// A cadeia efetiva inclui o peer direto (RemoteAddr) como último salto.
	// Com N proxies confiáveis, o cliente é a entrada de índice
	// len(entries)-N (contando a partir da direita, já descontando o peer).
	idx := len(entries) - trustedProxyCount
	if idx < 0 {
		idx = 0
	}
	if idx >= len(entries) {
		return remote
	}
	if ip := normalizeIP(entries[idx]); ip != "" {
		return ip
	}
	return remote
}

func forwardedEntries(r *http.Request) []string {
	raw := r.Header.Values("X-Forwarded-For")
	if len(raw) == 0 {
		return nil
	}
	joined := strings.Join(raw, ",")
	parts := strings.Split(joined, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if ip := normalizeIP(host); ip != "" {
		return ip
	}
	return "unknown"
}

func normalizeIP(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "[]")
	// Uma entrada pode vir como "ip:porta".
	if host, _, err := net.SplitHostPort(v); err == nil {
		v = host
	}
	addr := net.ParseIP(strings.Trim(v, "[]"))
	if addr == nil {
		return ""
	}
	return addr.String()
}
