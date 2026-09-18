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
$raceIndisponivel = $false

# O detector de corrida do Go depende do ThreadSanitizer, que reserva uma regiao
# enorme e contigua de shadow memory logo na subida do processo. Em shell com
# sandbox de memoria (e o caso da ferramenta Bash do Claude Code neste projeto,
# verificado em 12/09/2026) essa reserva falha e o binario aborta ANTES de rodar
# qualquer teste: "ThreadSanitizer failed to allocate ... error code: 87".
#
# Comprovadamente NAO e limitacao da maquina: o mesmo comando passa quando
# lancado de um PowerShell normal, inclusive num teste trivial 1+1==2. O que o
# log nao deixa distinguir e "TSan nao subiu" de "achei um data race" - dai a
# sonda: roda um binario de teste com -race sem executar nenhum caso (-run '^$').
# Se o TSan nao sobe nem assim, o detector nao esta disponivel nesta sessao.
function Test-RaceDisponivel {
    $saida = & go test -race -count=1 -run '^$' ./internal/id 2>&1 | Out-String
    return ($saida -notmatch 'ThreadSanitizer failed to allocate')
}

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

# gofmt vem antes do build: e o mais barato e o que mais suja revisao. O hook
# de PostToolUse (.claude/hooks/gofmt-backend.sh) conserta na hora da edicao;
# esta etapa e a rede que pega arquivo escrito por fora do Claude Code.
Write-Output ''
Write-Output '=== gofmt -l . ==='
$naoFormatados = & gofmt -l .
if (-not $naoFormatados) {
    Write-Output '--- OK: gofmt -l .'
}
else {
    Write-Output '!!! FALHOU: arquivos fora do gofmt (rode: gofmt -w .)'
    $naoFormatados | ForEach-Object { Write-Output $_ }
    $falhas++
}

Invoke-Etapa 'go build ./...' { go build ./... }
Invoke-Etapa 'go vet ./...' { go vet ./... }
# -count=1 e obrigatorio e nao e preciosismo: sem ele o Go serve resultado do
# cache, e um "ok (cached)" gravado quando o -race ainda funcionava continua
# aparecendo verde depois que o detector parou de subir. O gate mentia.
if (Test-RaceDisponivel) {
    Invoke-Etapa 'go test -race -count=1 ./...' { go test -race -count=1 ./... }
}
else {
    $raceIndisponivel = $true
    Write-Output ''
    Write-Output '=== go test -race ./... ==='
    Write-Output '!!! DETECTOR DE CORRIDA INDISPONIVEL NESTA SESSAO'
    Write-Output '    O ThreadSanitizer nao conseguiu reservar a shadow memory (erro 87).'
    Write-Output '    Causa tipica: shell com sandbox de memoria. NAO e falha do projeto e'
    Write-Output '    NAO e limitacao da maquina - o mesmo comando passa num PowerShell'
    Write-Output '    normal, ate num teste trivial 1+1==2.'
    Write-Output '    A suite roda a seguir SEM -race: os testes valem, mas o gate de'
    Write-Output '    corrida NAO foi verificado. Refaca em terminal normal antes de'
    Write-Output '    concluir a entrega:  powershell -File scripts/check.ps1'
    Invoke-Etapa 'go test -count=1 ./...   (SEM -race - ver aviso acima)' { go test -count=1 ./... }
}

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
    if ($raceIndisponivel) {
        # Nunca imprimir "TUDO PASSOU" limpo quando um gate nao rodou: e assim
        # que uma verificacao ausente vira verificacao esquecida.
        Write-Output 'PASSOU COM RESSALVA - o gate de -race NAO rodou neste ambiente.'
        Write-Output 'Antes de considerar uma entrega concluida, refaca em terminal normal.'
        exit 0
    }
    Write-Output 'TUDO PASSOU'
    exit 0
}
Write-Output "$falhas etapa(s) falharam"
if ($raceIndisponivel) {
    Write-Output 'Alem disso, o gate de -race NAO rodou neste ambiente.'
}
exit 1
