#!/usr/bin/env bash
# Hook PostToolUse — formatação automática do backend (DV6 do PLANOS.md).
#
# Roda gofmt em todo arquivo .go que o Write/Edit acabou de tocar, para que
# diferença de formatação nunca chegue à revisão como ruído. O `gofmt -l` do
# `scripts/check.ps1` é a rede de segurança: o hook conserta, o check acusa.
#
# Recebe o payload do hook em stdin (JSON) e nunca falha a chamada da
# ferramenta: qualquer imprevisto sai com 0 e em silêncio.
set -u

payload=$(cat)

# Extrai tool_input.file_path do JSON sem depender de jq estar instalado.
file=$(printf '%s' "$payload" |
	grep -o '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' |
	head -n1 |
	sed 's/^.*:[[:space:]]*"//; s/"$//')

[ -n "$file" ] || exit 0

# O JSON escapa a barra invertida do Windows ("D:\\Golang\\..."); desfaz para
# um caminho que o shell consiga abrir. Colapsa a forma escapada primeiro e
# depois qualquer barra invertida solta, para não depender de como o payload
# veio codificado.
file=$(printf '%s' "$file" | sed 's|\\\\|/|g; s|\\|/|g')

case "$file" in
*.go) ;;
*) exit 0 ;;
esac

[ -f "$file" ] || exit 0
command -v gofmt >/dev/null 2>&1 || exit 0

# Só age se houver o que formatar — assim o hook fica mudo no caso comum.
# O `--` separa opções de operandos: sem ele, um caminho que comece com "-"
# seria lido como flag do gofmt.
[ -n "$(gofmt -l -- "$file" 2>/dev/null)" ] || exit 0

gofmt -w -- "$file" 2>/dev/null || exit 0

base=${file##*/}
printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"gofmt aplicado automaticamente em %s (hook do projeto). O arquivo em disco agora esta formatado; considere isso ao comparar com o que voce escreveu."}}\n' "$base"
