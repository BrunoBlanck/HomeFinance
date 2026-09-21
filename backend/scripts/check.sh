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
RACE_INDISPONIVEL=0

etapa() {
  local nome="$1"; shift
  printf '\n=== %s ===\n' "$nome"
  if "$@"; then
    printf -- '--- OK: %s\n' "$nome"
  else
    printf '!!! FALHOU: %s\n' "$nome"
    FALHAS=$((FALHAS + 1))
  fi
}

# O detector de corrida do Go depende do ThreadSanitizer, que reserva uma região
# enorme e contígua de shadow memory logo na subida do processo. Em shell com
# sandbox de memória (é o caso da ferramenta Bash do Claude Code neste projeto,
# verificado em 12/09/2026) essa reserva falha e o binário aborta ANTES de rodar
# qualquer teste: "ThreadSanitizer failed to allocate ... error code: 87".
#
# Comprovadamente NÃO é limitação da máquina: o mesmo comando passa quando
# lançado de um PowerShell normal, inclusive num teste trivial 1+1==2. O que o
# log não deixa distinguir é "TSan não subiu" de "achei um data race" — daí a
# sonda: roda um binário de teste com -race sem executar nenhum caso (-run '^$').
# Se o TSan não sobe nem assim, o detector não está disponível nesta sessão.
race_disponivel() {
  local saida
  saida=$(go test -race -count=1 -run '^$' ./internal/id 2>&1)
  if printf '%s' "$saida" | grep -q 'ThreadSanitizer failed to allocate'; then
    return 1
  fi
  return 0
}

# gofmt vem antes do build: é o mais barato e o que mais suja revisão. O hook
# de PostToolUse (.claude/hooks/gofmt-backend.sh) conserta na hora da edição;
# esta etapa é a rede que pega arquivo escrito por fora do Claude Code.
printf '\n=== gofmt -l . ===\n'
NAO_FORMATADOS=$(gofmt -l . 2>/dev/null)
if [ -z "$NAO_FORMATADOS" ]; then
  printf -- '--- OK: gofmt -l .\n'
else
  printf '!!! FALHOU: arquivos fora do gofmt (rode: gofmt -w .)\n'
  printf '%s\n' "$NAO_FORMATADOS"
  FALHAS=$((FALHAS + 1))
fi

etapa "go build ./..." go build ./...
etapa "go vet ./..." go vet ./...
# -count=1 é obrigatório e não é preciosismo: sem ele o Go serve resultado do
# cache, e um `ok (cached)` gravado quando o -race ainda funcionava continua
# aparecendo verde depois que o detector parou de subir. O gate mentia.
if race_disponivel; then
  etapa "go test -race -count=1 ./..." go test -race -count=1 ./...
else
  RACE_INDISPONIVEL=1
  printf '\n=== go test -race ./... ===\n'
  printf '!!! DETECTOR DE CORRIDA INDISPONIVEL NESTA SESSAO\n'
  printf '    O ThreadSanitizer nao conseguiu reservar a shadow memory (erro 87).\n'
  printf '    Causa tipica: shell com sandbox de memoria. NAO e falha do projeto e\n'
  printf '    NAO e limitacao da maquina - o mesmo comando passa num PowerShell\n'
  printf '    normal, ate num teste trivial 1+1==2.\n'
  printf '    A suite roda a seguir SEM -race: os testes valem, mas o gate de\n'
  printf '    corrida NAO foi verificado. Refaca em terminal normal antes de\n'
  printf '    concluir a entrega:  powershell -File scripts/check.ps1\n'
  etapa "go test -count=1 ./...   (SEM -race — ver aviso acima)" go test -count=1 ./...
fi

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
  if [ "$RACE_INDISPONIVEL" -eq 1 ]; then
    # Nunca imprimir "TUDO PASSOU" limpo quando um gate nao rodou: e assim que
    # uma verificacao ausente vira verificacao esquecida.
    printf 'PASSOU COM RESSALVA — o gate de -race NAO rodou neste ambiente.\n'
    printf 'Antes de considerar uma entrega concluida, refaca em terminal normal.\n'
    exit 0
  fi
  printf 'TUDO PASSOU\n'
  exit 0
fi
printf '%d etapa(s) falharam\n' "$FALHAS"
[ "$RACE_INDISPONIVEL" -eq 1 ] && printf 'Alem disso, o gate de -race NAO rodou neste ambiente.\n'
exit 1
