import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const SRC = join(process.cwd(), 'src')

function cssModules(dir: string): string[] {
  const found: string[] = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) found.push(...cssModules(full))
    else if (entry.name.endsWith('.module.css')) found.push(full)
  }
  return found
}

/** Regressão de um bug que só aparece no navegador: se um *.module.css for
 *  avaliado antes da declaração de ordem, o navegador cria a camada
 *  `components` primeiro, `reset` e `base` passam a vencê-la, e o
 *  `font: inherit` do reset apaga a tipografia de todos os campos. */
describe('ordem das camadas CSS', () => {
  it('a folha global é o primeiro import de main.tsx', () => {
    const source = readFileSync(join(SRC, 'main.tsx'), 'utf8')
    const firstImport = source.split('\n').find((line) => line.startsWith('import '))
    expect(firstImport).toBe("import '@/styles/index.css'")
  })

  it('index.css declara a ordem antes de qualquer @import', () => {
    const css = readFileSync(join(SRC, 'styles', 'index.css'), 'utf8')
    const statement = css.indexOf('@layer reset, tokens, base, components, utilities;')
    expect(statement).toBeGreaterThanOrEqual(0)
    expect(statement).toBeLessThan(css.indexOf('@import'))
  })

  it('todo CSS Module vive na camada components', () => {
    const files = cssModules(SRC)
    expect(files.length).toBeGreaterThan(10)
    for (const file of files) {
      expect(readFileSync(file, 'utf8').trimStart().startsWith('@layer components {')).toBe(true)
    }
  })
})
