#!/usr/bin/env bash
# Verificação completa do backend do HomeFinance.
#
# Roda tudo o que a spec 0001 exige antes de qualquer entrega:
# build, vet, testes com -race, build sem CGo (prova o "puro Go" do ADR-008),
# govulncheck e gosec.
#
# Uso:  ./scripts/check.sh          (a partir de backend/)
#       ./scripts/check.sh --quick  (pula govulncheck e gosec)
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

QUICK=0
[ "${1:-}" = "--quick" ] && QUICK=1

# As ferramentas instaladas por "go install" ficam em $(go env GOPATH)/bin.
export PATH="$PATH:$(go env GOPATH)/bin"

FALHAS=0
etapa() {
  local nome="$1"; shift
  printf '\n=== %s ===\n' "$nome"
  if "$@"; then
    printf '--- OK: %s\n' "$nome"
  else
    printf '!!! FALHOU: %s\n' "$nome"
    FALHAS=$((FALHAS + 1))
  fi
}

etapa "go build ./..." go build ./...
etapa "go vet ./..." go vet ./...
etapa "go test -race ./..." go test -race ./...

printf '\n=== CGO_ENABLED=0 go build ./... ===\n'
if CGO_ENABLED=0 go build ./...; then
  printf -- '--- OK: build sem CGo (ADR-008: driver SQLite puro Go)\n'
else
  printf '!!! FALHOU: build sem CGo\n'
  FALHAS=$((FALHAS + 1))
fi

if [ "$QUICK" -eq 0 ]; then
  if command -v govulncheck >/dev/null 2>&1; then
    etapa "govulncheck ./..." govulncheck ./...
  else
    printf '\n!!! govulncheck não encontrado. Instale com:\n'
    printf '    go install golang.org/x/vuln/cmd/govulncheck@latest\n'
    FALHAS=$((FALHAS + 1))
  fi

  if command -v gosec >/dev/null 2>&1; then
    etapa "gosec ./..." gosec -quiet ./...
  else
    printf '\n!!! gosec não encontrado. Instale com:\n'
    printf '    go install github.com/securego/gosec/v2/cmd/gosec@latest\n'
    FALHAS=$((FALHAS + 1))
  fi
fi

printf '\n==================================================\n'
if [ "$FALHAS" -eq 0 ]; then
  printf 'TUDO PASSOU\n'
  exit 0
fi
printf '%d etapa(s) falharam\n' "$FALHAS"
exit 1
