import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { describe, expect, it } from 'vitest'

/** Duas regras do `AGENTS.md` que nenhum teste de comportamento pega, e que a
 *  entrega da emenda §11 acabou de exercitar: ela precisou de
 *  `palavrasParaAprender` em duas features (`import` e `transactions`) e a
 *  saída certa foi mover a função para `lib/` — a errada seria um import de
 *  uma feature na outra.
 *
 *  - **Sem imports entre features**: `features/a` nunca importa de
 *    `features/b`. O que as duas precisam mora em `lib/` ou em
 *    `components/`.
 *  - **Sem barrel files**: nada de `index.ts` reexportando uma feature — o
 *    import aponta o arquivo.
 *
 *  Varremos o disco de propósito: é a única forma de a regra valer para o
 *  arquivo que alguém escrever amanhã. */

const RAIZ = join(process.cwd(), 'src')
const FEATURES = join(RAIZ, 'features')

function arquivosDe(diretorio: string): string[] {
  const achados: string[] = []
  for (const entrada of readdirSync(diretorio)) {
    const caminho = join(diretorio, entrada)
    if (statSync(caminho).isDirectory()) {
      achados.push(...arquivosDe(caminho))
      continue
    }
    if (/\.tsx?$/.test(entrada)) achados.push(caminho)
  }
  return achados
}

/** A feature a que um arquivo pertence: `src/features/<nome>/…`. */
function featureDe(caminho: string): string {
  return relative(FEATURES, caminho).split(sep)[0] ?? ''
}

describe('arquitetura do frontend', () => {
  const arquivos = arquivosDe(FEATURES)

  it('tem features para varrer (o teste não pode passar por não achar nada)', () => {
    expect(arquivos.length).toBeGreaterThan(10)
    expect(new Set(arquivos.map(featureDe)).size).toBeGreaterThan(3)
  })

  it('nenhuma feature importa de outra feature', () => {
    const violacoes: string[] = []
    for (const arquivo of arquivos) {
      const minha = featureDe(arquivo)
      const conteudo = readFileSync(arquivo, 'utf8')
      for (const achado of conteudo.matchAll(/from\s+['"]@\/features\/([^/'"]+)/g)) {
        const outra = achado[1]
        if (outra && outra !== minha) {
          violacoes.push(`${relative(RAIZ, arquivo)} → features/${outra}`)
        }
      }
      // O caminho relativo que sobe para fora da própria feature é o mesmo
      // problema escrito de outro jeito.
      for (const achado of conteudo.matchAll(/from\s+['"](\.\.\/){2,}([^'"]+)/g)) {
        violacoes.push(`${relative(RAIZ, arquivo)} → ${achado[0]}`)
      }
    }
    expect(violacoes).toEqual([])
  })

  it('nenhuma feature tem barrel file', () => {
    const barris = arquivos
      .filter((arquivo) => /(^|[\\/])index\.tsx?$/.test(arquivo))
      .map((arquivo) => relative(RAIZ, arquivo))
    expect(barris).toEqual([])
  })
})
