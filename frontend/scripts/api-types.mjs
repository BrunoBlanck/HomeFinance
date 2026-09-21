#!/usr/bin/env node
/** Gera (ou confere) os tipos TypeScript do frontend a partir do contrato
 *  OpenAPI do backend — ADR-015.
 *
 *  Uso:
 *    node scripts/api-types.mjs            gera src/api/schema.gen.ts
 *    node scripts/api-types.mjs --check    falha se o arquivo versionado estiver
 *                                          desatualizado em relação à spec
 *
 *  Por que `npx` isolado em vez de uma devDependency: o `openapi-typescript`
 *  declara `peerDependencies: { typescript: "^5.x" }` e este projeto usa
 *  TypeScript 7 (a porta nativa). Não existe versão publicada do gerador que
 *  aceite TS 7 — checado em 12/09/2026, a `latest` (7.13.0) ainda pede ^5.x.
 *  Instalar com `--legacy-peer-deps` faria o gerador rodar contra uma API de
 *  compilador para a qual ele não foi escrito, que é exatamente o tipo de
 *  "funciona até não funcionar" que não se coloca num gate de contrato.
 *
 *  Rodar por `npx` numa versão FIXA resolve os dois lados: o gerador traz o
 *  próprio TypeScript 5 no seu sandbox, o projeto continua em TS 7, e o
 *  `package.json` não ganha dependência nenhuma. O artefato gerado é
 *  versionado, então build, teste e `tsc` nunca precisam da ferramenta. */

import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

/** Fixa a versão para que a saída seja reproduzível: gerador flutuante produz
 *  diff espontâneo e o `--check` viraria ruído. Subir de versão é uma mudança
 *  deliberada — editar aqui e regerar. */
const GENERATOR = 'openapi-typescript@7.13.0'

const here = dirname(fileURLToPath(import.meta.url))
const frontendRoot = resolve(here, '..')
const specPath = resolve(frontendRoot, '..', 'backend', 'api', 'openapi.yaml')
const outPath = resolve(frontendRoot, 'src', 'api', 'schema.gen.ts')

/** Marcador do hash da spec dentro do arquivo gerado.
 *
 *  Ele existe para que a aderência spec↔tipos possa ser conferida **sem rede**:
 *  `src/api/schema-sync.test.ts` recalcula o SHA-256 de `openapi.yaml` e compara
 *  com esta linha. É o guarda do dia a dia, que roda em todo `npm test`.
 *  O `npm run api:check` continua existindo para o caso mais forte (alguém
 *  editou o gerado à mão, ou o gerador mudou de versão), mas esse precisa de
 *  rede e por isso NÃO entra no caminho do build. */
export const PREFIXO_HASH = ' *  sha256 da spec: '

function hashDaSpec() {
  // Lido como bytes: normalizar fim de linha aqui faria o hash mudar conforme
  // o checkout, que é exatamente o falso positivo que queremos evitar.
  return createHash('sha256').update(readFileSync(specPath)).digest('hex')
}

function cabecalho() {
  return `/** ARQUIVO GERADO — não edite à mão.
 *
 *  Fonte: backend/api/openapi.yaml (ADR-006 e ADR-015).
 *  Regerar: npm run api:gen        Conferir: npm run api:check
 *
 *  O contrato é do backend; este arquivo é a projeção dele em TypeScript. Se um
 *  campo aqui está errado, o conserto é na spec, nunca neste arquivo.
 *
${PREFIXO_HASH}${hashDaSpec()} */

`
}

/** Aspas em torno de cada caminho porque no Windows a chamada passa por um
 *  shell (`npx` é um `.cmd`, e o Node se recusa a executá-lo sem shell desde a
 *  correção do CVE-2024-27980). Sem as aspas, um diretório com espaço no nome
 *  quebra a linha de comando — e o que quebra por espaço também se dobra por
 *  metacaractere. */
function comAspas(caminho) {
  return `"${caminho}"`
}

function gerar(destino) {
  const noWindows = process.platform === 'win32'
  const argumentos = noWindows
    ? ['-y', GENERATOR, comAspas(specPath), '-o', comAspas(destino)]
    : ['-y', GENERATOR, specPath, '-o', destino]
  try {
    execFileSync('npx', argumentos, {
      stdio: ['ignore', 'ignore', 'pipe'],
      shell: noWindows,
    })
  } catch (erro) {
    const detalhe = erro.stderr ? String(erro.stderr).trim() : erro.message
    console.error(`\nFalha ao rodar ${GENERATOR}.\n${detalhe}\n`)
    console.error('Este comando precisa de rede na primeira execução (depois o npx usa o cache).')
    process.exit(1)
  }
  return cabecalho() + readFileSync(destino, 'utf8')
}

const conferir = process.argv.includes('--check')
const temp = mkdtempSync(join(tmpdir(), 'homefinance-api-'))

try {
  const gerado = gerar(join(temp, 'schema.ts'))

  if (!conferir) {
    writeFileSync(outPath, gerado, 'utf8')
    console.error(`tipos da API gerados em src/api/schema.gen.ts (a partir de ${GENERATOR})`)
    process.exit(0)
  }

  let atual = null
  try {
    atual = readFileSync(outPath, 'utf8')
  } catch {
    console.error('\nsrc/api/schema.gen.ts não existe. Rode: npm run api:gen\n')
    process.exit(1)
  }

  if (atual !== gerado) {
    console.error(
      '\nsrc/api/schema.gen.ts está DESATUALIZADO em relação a backend/api/openapi.yaml.' +
        '\nA spec mudou e os tipos do frontend não acompanharam. Rode: npm run api:gen\n',
    )
    process.exit(1)
  }

  console.error('src/api/schema.gen.ts confere com backend/api/openapi.yaml')
} finally {
  rmSync(temp, { recursive: true, force: true })
}
