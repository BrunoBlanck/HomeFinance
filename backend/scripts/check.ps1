# Verificação completa do backend do HomeFinance (Windows / PowerShell).
#
# Roda tudo o que a spec 0001 exige antes de qualquer entrega: build, vet,
# testes com -race, build sem CGo (prova o "puro Go" do ADR-008),
# govulncheck e gosec.
#
# Uso:  .\scripts\check.ps1
#       .\scripts\check.ps1 -Quick    (pula govulncheck e gosec)

param(
    [switch]$Quick
)

$ErrorActionPreference = 'Continue'
Set-Location (Join-Path $PSScriptRoot '..')

# As ferramentas instaladas por "go install" ficam em $(go env GOPATH)\bin.
$goBin = Join-Path (& go env GOPATH) 'bin'
if (Test-Path $goBin) {
    $env:PATH = "$env:PATH;$goBin"
}

$falhas = 0

function Invoke-Etapa {
    param(
        [string]$Nome,
        [scriptblock]$Acao
    )
    Write-Output ''
    Write-Output "=== $Nome ==="
    & $Acao
    if ($LASTEXITCODE -eq 0) {
        Write-Output "--- OK: $Nome"
    }
    else {
        Write-Output "!!! FALHOU: $Nome (exit $LASTEXITCODE)"
        $script:falhas++
    }
}

Invoke-Etapa 'go build ./...' { go build ./... }
Invoke-Etapa 'go vet ./...' { go vet ./... }
Invoke-Etapa 'go test -race ./...' { go test -race ./... }

Write-Output ''
Write-Output '=== CGO_ENABLED=0 go build ./... ==='
$env:CGO_ENABLED = '0'
go build ./...
if ($LASTEXITCODE -eq 0) {
    Write-Output '--- OK: build sem CGo (ADR-008: driver SQLite puro Go)'
}
else {
    Write-Output '!!! FALHOU: build sem CGo'
    $falhas++
}
Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue

if (-not $Quick) {
    if (Get-Command govulncheck -ErrorAction SilentlyContinue) {
        Invoke-Etapa 'govulncheck ./...' { govulncheck ./... }
    }
    else {
        Write-Output ''
        Write-Output '!!! govulncheck nao encontrado. Instale com:'
        Write-Output '    go install golang.org/x/vuln/cmd/govulncheck@latest'
        $falhas++
    }

    if (Get-Command gosec -ErrorAction SilentlyContinue) {
        Invoke-Etapa 'gosec ./...' { gosec -quiet ./... }
    }
    else {
        Write-Output ''
        Write-Output '!!! gosec nao encontrado. Instale com:'
        Write-Output '    go install github.com/securego/gosec/v2/cmd/gosec@latest'
        $falhas++
    }
}

Write-Output ''
Write-Output '=================================================='
if ($falhas -eq 0) {
    Write-Output 'TUDO PASSOU'
    exit 0
}
Write-Output "$falhas etapa(s) falharam"
exit 1
