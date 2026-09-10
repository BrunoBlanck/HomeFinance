# Sobe a API em desenvolvimento carregando o .env para o ambiente do PROCESSO.
#
# A aplicação NÃO lê arquivo de configuração — `internal/platform/config` usa
# apenas o ambiente do processo, de propósito (docs/SEGURANCA.md §7). Quem
# traduz arquivo -> ambiente é este script, e só em desenvolvimento.
#
# Uso:  .\scripts\dev.ps1            (procura backend\.env, depois ..\.env)
#       .\scripts\dev.ps1 -EnvFile C:\caminho\outro.env
#       .\scripts\dev.ps1 -NoRun     (só exporta, não sobe a API)

[CmdletBinding()]
param(
    [string]$EnvFile,
    [switch]$NoRun
)

$ErrorActionPreference = 'Stop'

$backendDir = Split-Path -Parent $PSScriptRoot
$repoRoot   = Split-Path -Parent $backendDir

if (-not $EnvFile) {
    foreach ($candidate in @((Join-Path $backendDir '.env'), (Join-Path $repoRoot '.env'))) {
        if (Test-Path -LiteralPath $candidate) { $EnvFile = $candidate; break }
    }
}
if (-not $EnvFile) {
    throw "Nenhum .env encontrado em '$backendDir' nem em '$repoRoot'. Copie o .env.example e preencha."
}
if (-not (Test-Path -LiteralPath $EnvFile)) {
    throw "Arquivo nao encontrado: $EnvFile"
}

Write-Host "carregando $EnvFile" -ForegroundColor DarkGray

$loaded  = New-Object System.Collections.Generic.List[string]
$skipped = New-Object System.Collections.Generic.List[string]
$lineNo  = 0

foreach ($raw in (Get-Content -LiteralPath $EnvFile -Encoding UTF8)) {
    $lineNo++
    $line = $raw.Trim()
    if ($line -eq '' -or $line.StartsWith('#')) { continue }
    if ($line.StartsWith('export ')) { $line = $line.Substring(7).Trim() }

    $split = $line.IndexOf('=')
    if ($split -lt 1) {
        Write-Warning "linha ${lineNo}: sem '=', ignorada"
        continue
    }

    $key   = $line.Substring(0, $split).Trim()
    $value = $line.Substring($split + 1).Trim()

    if ($key -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
        Write-Warning "linha ${lineNo}: nome de variavel invalido, ignorada"
        continue
    }

    # Valor entre aspas fica literal (pode conter '#'); sem aspas, o que vem
    # depois de ' #' e comentario. Sem essa distincao, os comentarios em linha
    # do .env.example entrariam no valor e a validacao do boot recusaria.
    if ($value.Length -ge 2 -and
        (($value.StartsWith('"') -and $value.EndsWith('"')) -or
         ($value.StartsWith("'") -and $value.EndsWith("'")))) {
        $value = $value.Substring(1, $value.Length - 2)
    } elseif ($value.StartsWith('#')) {
        # Valor vazio seguido de comentario (ex.: `COOKIE_DOMAIN=  # ...`).
        $value = ''
    } else {
        $hash = $value.IndexOf(' #')
        if ($hash -ge 0) { $value = $value.Substring(0, $hash).TrimEnd() }
    }

    # Convencao dotenv: o ambiente real vence o arquivo.
    if ([Environment]::GetEnvironmentVariable($key, 'Process')) {
        $skipped.Add($key) | Out-Null
        continue
    }

    Set-Item -LiteralPath "env:$key" -Value $value
    $loaded.Add($key) | Out-Null
}

# Só nomes de chave — nunca o valor: este script imprime no terminal.
if ($loaded.Count  -gt 0) { Write-Host ("exportadas: " + ($loaded  -join ', ')) -ForegroundColor DarkGray }
if ($skipped.Count -gt 0) { Write-Host ("ja definidas no shell (arquivo ignorado): " + ($skipped -join ', ')) -ForegroundColor DarkGray }

# Diagnostico antecipado dos dois segredos obrigatorios, para a falha apontar
# o arquivo em vez de so repetir a mensagem do boot.
$problemas = @()
foreach ($nome in @('JWT_SECRET', 'OTP_PEPPER')) {
    $v = [Environment]::GetEnvironmentVariable($nome, 'Process')
    if (-not $v) {
        $problemas += "$nome ausente"
    } elseif ([Text.Encoding]::UTF8.GetByteCount($v) -lt 32) {
        $problemas += "$nome tem menos de 32 bytes"
    }
}
if ($env:JWT_SECRET -and $env:JWT_SECRET -eq $env:OTP_PEPPER) {
    $problemas += 'JWT_SECRET e OTP_PEPPER sao iguais'
}
if ($problemas.Count -gt 0) {
    Write-Host ''
    Write-Warning ("configuracao incompleta em ${EnvFile}: " + ($problemas -join '; '))
    Write-Host 'Gere segredos novos com:' -ForegroundColor Yellow
    Write-Host '  $rng = [System.Security.Cryptography.RNGCryptoServiceProvider]::new()' -ForegroundColor Yellow
    Write-Host '  $b = [byte[]]::new(48); $rng.GetBytes($b); [Convert]::ToBase64String($b)' -ForegroundColor Yellow
    Write-Host ''
}

if ($NoRun) { return }

Push-Location $backendDir
try {
    & go run ./cmd/api
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
