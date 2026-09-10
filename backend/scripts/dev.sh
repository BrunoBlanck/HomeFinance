#!/usr/bin/env bash
# Sobe a API em desenvolvimento carregando o .env para o ambiente do PROCESSO.
#
# A aplicação NÃO lê arquivo de configuração — internal/platform/config usa
# apenas o ambiente do processo, de propósito (docs/SEGURANCA.md §7). Quem
# traduz arquivo -> ambiente é este script, e só em desenvolvimento.
#
# Uso:  ./scripts/dev.sh              (procura backend/.env, depois ../.env)
#       ENV_FILE=/caminho/outro.env ./scripts/dev.sh
#       NO_RUN=1 ./scripts/dev.sh     (só exporta, não sobe a API)
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
backend_dir="$(dirname "$script_dir")"
repo_root="$(dirname "$backend_dir")"

env_file="${ENV_FILE:-}"
if [[ -z "$env_file" ]]; then
  for candidate in "$backend_dir/.env" "$repo_root/.env"; do
    if [[ -f "$candidate" ]]; then env_file="$candidate"; break; fi
  done
fi
if [[ -z "$env_file" ]]; then
  echo "erro: nenhum .env encontrado em '$backend_dir' nem em '$repo_root'." >&2
  exit 1
fi
if [[ ! -f "$env_file" ]]; then
  echo "erro: arquivo não encontrado: $env_file" >&2
  exit 1
fi

echo "carregando $env_file"

loaded=()
skipped=()
lineno=0

while IFS= read -r raw || [[ -n "$raw" ]]; do
  lineno=$((lineno + 1))
  line="${raw%$'\r'}"                      # arquivo pode vir com CRLF do Windows
  line="$(printf '%s' "$line" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
  [[ -z "$line" || "$line" == \#* ]] && continue
  [[ "$line" == export\ * ]] && line="${line#export }"

  if [[ "$line" != *=* ]]; then
    echo "aviso: linha $lineno: sem '=', ignorada" >&2
    continue
  fi

  key="${line%%=*}"
  value="${line#*=}"
  key="$(printf '%s' "$key" | sed -e 's/[[:space:]]*$//')"
  value="$(printf '%s' "$value" | sed -e 's/^[[:space:]]*//')"

  if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    echo "aviso: linha $lineno: nome de variável inválido, ignorada" >&2
    continue
  fi

  # Valor entre aspas fica literal (pode conter '#'); sem aspas, o que vem
  # depois de ' #' é comentário. Sem essa distinção, os comentários em linha
  # do .env.example entrariam no valor e a validação do boot recusaria.
  case "$value" in
    \"*\") value="${value#\"}"; value="${value%\"}" ;;
    \'*\') value="${value#\'}"; value="${value%\'}" ;;
    \#*)
      # Valor vazio seguido de comentario (ex.: `COOKIE_DOMAIN=  # ...`).
      value=""
      ;;
    *)
      value="${value%% #*}"
      value="$(printf '%s' "$value" | sed -e 's/[[:space:]]*$//')"
      ;;
  esac

  # Convenção dotenv: o ambiente real vence o arquivo.
  if [[ -n "${!key:-}" ]]; then
    skipped+=("$key")
    continue
  fi

  export "$key=$value"
  loaded+=("$key")
done < "$env_file"

# Só nomes de chave — nunca o valor: este script imprime no terminal.
[[ ${#loaded[@]}  -gt 0 ]] && echo "exportadas: ${loaded[*]}"
[[ ${#skipped[@]} -gt 0 ]] && echo "já definidas no shell (arquivo ignorado): ${skipped[*]}"

# Diagnóstico antecipado dos dois segredos obrigatórios, para a falha apontar
# o arquivo em vez de só repetir a mensagem do boot.
problemas=()
for nome in JWT_SECRET OTP_PEPPER; do
  valor="${!nome:-}"
  if [[ -z "$valor" ]]; then
    problemas+=("$nome ausente")
  elif [[ "$(printf '%s' "$valor" | wc -c)" -lt 32 ]]; then
    problemas+=("$nome tem menos de 32 bytes")
  fi
done
if [[ -n "${JWT_SECRET:-}" && "${JWT_SECRET:-}" == "${OTP_PEPPER:-}" ]]; then
  problemas+=("JWT_SECRET e OTP_PEPPER são iguais")
fi
if [[ ${#problemas[@]} -gt 0 ]]; then
  echo "" >&2
  echo "aviso: configuração incompleta em $env_file: ${problemas[*]}" >&2
  echo "Gere segredos novos com: openssl rand -base64 48" >&2
  echo "" >&2
fi

[[ -n "${NO_RUN:-}" ]] && exit 0

cd "$backend_dir"
exec go run ./cmd/api
