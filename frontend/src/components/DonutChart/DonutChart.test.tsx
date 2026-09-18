import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { arcosDaRosca, DonutChart } from './DonutChart'
import type { Fatia } from './dobrar'

function fatia(
  key: string,
  label: string,
  cents: number,
  shareBp: number,
  papel: Fatia['papel'],
): Fatia {
  return { key, label, cents, shareBp, papel }
}

const QUATRO: Fatia[] = [
  fatia('a', 'Alimentação', 210_000, 4100, '1'),
  fatia('b', 'Moradia', 120_000, 2340, '2'),
  fatia('c', 'Transporte', 80_000, 1560, '3'),
  fatia('d', 'Lazer', 102_345, 2000, '4'),
]

function svgDa(container: HTMLElement): SVGSVGElement {
  const svg = container.querySelector('figure svg')
  if (!svg) throw new Error('a figura não tem SVG')
  return svg as SVGSVGElement
}

function fatiasDo(container: HTMLElement): SVGPathElement[] {
  return Array.from(container.querySelectorAll('path[data-fill]'))
}

function montar(fatias: Fatia[], extras?: { loading?: boolean; totalCents?: number }) {
  return render(
    <DonutChart
      fatias={fatias}
      totalCents={extras?.totalCents ?? 512_345}
      rotuloDoCentro="setembro"
      legenda="Distribuição por categoria — os valores estão na tabela abaixo."
      {...(extras?.loading ? { loading: true } : {})}
    />,
  )
}

describe('DonutChart', () => {
  it('o SVG é decorativo: a fonte dos números é a tabela', () => {
    const { container } = montar(QUATRO)
    const svg = svgDa(container)
    expect(svg).toHaveAttribute('aria-hidden', 'true')
    expect(svg).toHaveAttribute('focusable', 'false')
    expect(svg).toHaveAttribute('viewBox', '0 0 240 240')
    // Sem tooltip próprio e sem foco em fatia: o caminho de teclado é a tabela.
    expect(container.querySelector('[role="tooltip"]')).toBeNull()
    expect(container.querySelector('[tabindex]')).toBeNull()
  })

  it('cada fatia é um <path> com <title> nativo e gap por stroke — nunca um <circle>', () => {
    const { container } = montar(QUATRO)
    const caminhos = fatiasDo(container)
    expect(caminhos).toHaveLength(4)
    expect(container.querySelector('circle')).toBeNull()

    expect(caminhos.map((p) => p.getAttribute('data-fill'))).toEqual(['1', '2', '3', '4'])
    for (const caminho of caminhos) {
      expect(caminho).toHaveAttribute('fill-rule', 'evenodd')
      expect(caminho).toHaveAttribute('vector-effect', 'non-scaling-stroke')
      expect(caminho.getAttribute('d')).toMatch(/^M[\d.\s-]+A116 116 /)
    }
    // "Nome · R$ valor · percentual com UMA casa".
    expect(caminhos[0]?.querySelector('title')?.textContent?.replace(/ /g, ' ')).toBe(
      'Alimentação · R$ 2.100,00 · 41,0%',
    )
  })

  it('uma fatia só vira o anel inteiro, com o furo por fill-rule', () => {
    const { container } = montar([fatia('sem', 'Sem categoria', 30_000, 10_000, 'pendente')])
    const caminhos = fatiasDo(container)
    expect(caminhos).toHaveLength(1)
    expect(caminhos[0]).toHaveAttribute('data-fill', 'pendente')
    expect(caminhos[0]).toHaveAttribute('fill-rule', 'evenodd')
    // Dois subcaminhos: o círculo de fora e o furo.
    expect(caminhos[0]?.getAttribute('d')).toBe(
      'M120 4A116 116 0 1 1 120 236A116 116 0 1 1 120 4Z' +
        'M120 34A86 86 0 1 0 120 206A86 86 0 1 0 120 34Z',
    )
    expect(container.querySelector('circle')).toBeNull()
  })

  it('"Outras" é a última, hachurada, com id de padrão próprio', () => {
    const { container } = montar([
      ...QUATRO,
      fatia('outras', 'Outras (3 categorias)', 63_000, 1230, 'outras'),
    ])
    const hachurada = fatiasDo(container).at(-1)
    expect(hachurada).toHaveAttribute('data-fill', 'outras')

    const fill = hachurada?.getAttribute('fill') ?? ''
    expect(fill).toMatch(/^url\(#hf-rosca-[\w-]+\)$/)
    const padraoId = fill.slice(5, -1)
    expect(container.querySelector(`pattern[id="${padraoId}"]`)).toBeTruthy()
    // A hachura é feita de linhas, não de cor: sobrevive ao grayscale.
    expect(container.querySelector(`pattern[id="${padraoId}"] line`)).toBeTruthy()
    expect(hachurada?.querySelector('title')?.textContent).toContain('Outras (3 categorias)')
  })

  it('o centro traz o total do servidor e lê "Total de … em setembro"', () => {
    const { container } = montar(QUATRO, { totalCents: 512_345 })
    expect(container.querySelector('[data-emphasis="hero"]')).toBeTruthy()
    // Visível sem "R$"; o leitor de tela recebe a frase inteira.
    expect(screen.getByText('5.123,45')).toBeInTheDocument()
    expect(screen.getByText('Total de')).toBeInTheDocument()
    expect(screen.getByText('R$ 5.123,45')).toBeInTheDocument()
    expect(screen.getByText('setembro')).toBeInTheDocument()
  })

  it('a legenda é <ol> na ordem do anel, com amostra, nome e uma casa', () => {
    const { container } = montar(QUATRO)
    expect(container.querySelector('ol')).toBeTruthy()
    const itens = screen.getAllByRole('listitem')
    expect(itens.map((li) => li.textContent)).toEqual([
      'Alimentação41,0%',
      'Moradia23,4%',
      'Transporte15,6%',
      'Lazer20,0%',
    ])
    // A amostra é decorativa: o nome está escrito ao lado.
    for (const li of itens) {
      const amostra = li.querySelector('svg')
      expect(amostra).toHaveAttribute('aria-hidden', 'true')
      expect(amostra?.querySelector('rect')).toHaveAttribute('data-fill')
    }
  })

  it('nada é escrito DENTRO das fatias: o SVG não tem <text>', () => {
    const { container } = montar(QUATRO)
    expect(svgDa(container).querySelector('text')).toBeNull()
  })

  it('a legenda escrita acompanha a figura', () => {
    const { container } = montar(QUATRO)
    expect(container.querySelector('figcaption')?.textContent).toBe(
      'Distribuição por categoria — os valores estão na tabela abaixo.',
    )
  })

  it('carregando: anel sem fatia, esqueletos, aria-busy e NENHUMA live region própria', () => {
    const { container } = montar(QUATRO, { loading: true })
    expect(container.querySelector('figure')).toHaveAttribute('aria-busy', 'true')
    expect(fatiasDo(container)).toHaveLength(0)
    // O anel continua desenhado, na cor-base do Skeleton.
    expect(svgDa(container).querySelector('path')).toBeTruthy()
    // Quem anuncia o carregamento é a DataTable logo abaixo — uma live region só.
    expect(container.querySelector('[role="status"]')).toBeNull()
    expect(screen.queryByText('5.123,45')).not.toBeInTheDocument()
    // O rótulo do mês fica: só o número espera.
    expect(screen.getByText('setembro')).toBeInTheDocument()
  })

  it('o nome da categoria entra como TEXTO, nunca como marcação', () => {
    const { container } = montar([fatia('x', '<img src=x onerror=alert(1)>', 100, 10_000, '1')])
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('title')?.textContent).toContain('<img src=x onerror=alert(1)>')
  })
})

describe('arcosDaRosca', () => {
  it('fecha o círculo: a primeira começa no topo e a última volta a ele', () => {
    const arcos = arcosDaRosca(QUATRO)
    expect(arcos).toHaveLength(4)
    expect(arcos[0]?.d.startsWith('M120 4 ')).toBe(true)
    // 4100 bp = 147,6°: arco pequeno, sentido horário.
    expect(arcos[0]?.d).toContain('A116 116 0 0 1')
    expect(arcos.at(-1)?.d).toContain('120 4')
  })

  it('fatia maior que meia-volta usa o large-arc-flag', () => {
    const arcos = arcosDaRosca([
      fatia('a', 'Grande', 100, 7000, '1'),
      fatia('b', 'Pequena', 30, 3000, '2'),
    ])
    expect(arcos[0]?.d).toContain('A116 116 0 1 1')
    expect(arcos[1]?.d).toContain('A116 116 0 0 1')
  })

  it('fatia com participação zero não é desenhada, e a que sobra vira o anel', () => {
    const arcos = arcosDaRosca([
      fatia('a', 'Única', 100, 10_000, '1'),
      fatia('z', 'Zerada', 0, 0, '2'),
    ])
    expect(arcos).toHaveLength(1)
    expect(arcos[0]?.fatia.key).toBe('a')
    expect(arcosDaRosca([])).toEqual([])
    expect(arcosDaRosca([fatia('z', 'Zerada', 0, 0, '1')])).toEqual([])
  })
})
