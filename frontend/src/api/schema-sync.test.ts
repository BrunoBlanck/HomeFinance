import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/** Guarda **offline** da aderência entre `backend/api/openapi.yaml` e os tipos
 *  gerados em `src/api/schema.gen.ts` (ADR-015).
 *
 *  Por que existe, já havendo o `npm run api:check`: aquele comando roda o
 *  gerador de novo e compara byte a byte, o que é mais forte — mas precisa de
 *  **rede** (o `openapi-typescript` vem por `npx` isolado, porque exige
 *  TypeScript 5 e o projeto está no 7). Pendurar o build numa busca ao registro
 *  npm troca um problema por dois: build que falha sem internet, e uma
 *  dependência baixada no momento do build, que é justamente o tipo de coisa
 *  que um projeto com dado financeiro não quer no caminho crítico.
 *
 *  Este teste faz a checagem que cobre o caso real — "a spec mudou e ninguém
 *  regerou os tipos" — comparando o SHA-256 que o gerador carimbou no cabeçalho
 *  com o hash atual do arquivo da spec. Custa milissegundos, roda em todo
 *  `npm test` e não fala com a rede.
 *
 *  O que ele NÃO pega: alguém editar `schema.gen.ts` à mão sem mexer na spec.
 *  Esse caso é do `npm run api:check`, que deve rodar no CI e antes da entrega. */

const RAIZ_FRONTEND = process.cwd()
const CAMINHO_SPEC = resolve(RAIZ_FRONTEND, '..', 'backend', 'api', 'openapi.yaml')
const CAMINHO_GERADO = resolve(RAIZ_FRONTEND, 'src', 'api', 'schema.gen.ts')

const PREFIXO_HASH = ' *  sha256 da spec: '

describe('tipos gerados x contrato OpenAPI', () => {
  it('o hash carimbado no arquivo gerado é o da spec atual', () => {
    const gerado = readFileSync(CAMINHO_GERADO, 'utf8')

    const linha = gerado.split('\n').find((l) => l.startsWith(PREFIXO_HASH))
    expect(
      linha,
      'src/api/schema.gen.ts não tem o carimbo de hash. Rode: npm run api:gen',
    ).toBeDefined()

    const carimbado = (linha ?? '').slice(PREFIXO_HASH.length).replace(' */', '').trim()
    const atual = createHash('sha256').update(readFileSync(CAMINHO_SPEC)).digest('hex')

    expect(
      carimbado,
      'backend/api/openapi.yaml mudou e src/api/schema.gen.ts não acompanhou. Rode: npm run api:gen',
    ).toBe(atual)
  })

  it('o arquivo gerado se declara gerado, para ninguém editar por engano', () => {
    const gerado = readFileSync(CAMINHO_GERADO, 'utf8')
    expect(gerado).toContain('ARQUIVO GERADO — não edite à mão')
  })
})
