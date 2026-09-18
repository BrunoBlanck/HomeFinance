import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alturaDaBarra, BarChart, type ColunaDoGrafico } from './BarChart'

const SERIES = [
  { papel: '1', nome: 'Aportes' },
  { papel: '3', nome: 'Resgates' },
] as const

function coluna(
  key: string,
  rotulo: string,
  aportes: number,
  resgates: number,
  viradaDeAno = false,
): ColunaDoGrafico {
  const base: ColunaDoGrafico = {
    key,
    rotulo,
    valores: [
      { papel: '1', cents: aportes },
      { papel: '3', cents: resgates },
    ],
    titulos: [`${key} · Aportes`, `${key} · Resgates`],
  }
  if (viradaDeAno) base.viradaDeAno = true
  return base
}

/** Doze colunas, com janeiro na posição 3 — a virada de ano cai antes dela. */
function dozeColunas(): ColunaDoGrafico[] {
  const meses = ['out', 'nov', 'dez', 'jan', 'fev', 'mar', 'abr', 'mai', 'jun', 'jul', 'ago', 'set']
  return meses.map((mes, indice) =>
    coluna(`m${indice}`, mes, indice === 11 ? 200_000 : 0, 0, mes === 'jan'),
  )
}

/** O SVG da plotagem, e não o do `Swatch` da legenda — que também é um
 *  `<rect data-fill>`. A busca é pelo viewBox, que é contrato do componente. */
function plotagem(container: HTMLElement): Element {
  // `getAttribute`, e não um seletor `svg[viewBox=...]`: num documento HTML o
  // seletor de atributo é normalizado para minúsculas, e `viewBox` do SVG é
  // camelCase — o seletor nunca casaria.
  const svg = Array.from(container.querySelectorAll('svg')).find(
    (candidato) => candidato.getAttribute('viewBox') === '0 0 480 240',
  )
  if (!svg) throw new Error('plotagem não encontrada')
  return svg
}

function barras(container: HTMLElement) {
  return Array.from(plotagem(container).querySelectorAll('rect[data-fill]'))
}

describe('BarChart', () => {
  describe('alturaDaBarra', () => {
    it('zero não desenha barra nenhuma', () => {
      // A regra da E6a para participação nula: um traço de altura mínima ali
      // afirmaria que houve movimento.
      expect(alturaDaBarra(0, 240_000)).toBe(0)
    })

    it('o máximo ocupa a altura de plotagem inteira', () => {
      expect(alturaDaBarra(240_000, 240_000)).toBe(232)
    })

    it('valor mínimo desenha 3 unidades — o fio que não finge altura', () => {
      // 1 centavo sobre R$ 2.400 daria 0,0001 unidade; o piso de 3 é o que diz
      // "houve algo" sem inventar uma barra visível.
      expect(alturaDaBarra(1, 240_000)).toBe(3)
      expect(alturaDaBarra(500, 240_000)).toBe(3)
    })

    it('a proporção é sobre 232, arredondada', () => {
      expect(alturaDaBarra(120_000, 240_000)).toBe(116)
      expect(alturaDaBarra(60_000, 240_000)).toBe(58)
    })

    it('máximo zerado não divide por zero', () => {
      expect(alturaDaBarra(0, 0)).toBe(0)
      expect(Number.isFinite(alturaDaBarra(100, 0))).toBe(true)
    })
  })

  it('desenha duas barras por coluna, na geometria do viewBox', () => {
    const { container } = render(
      <BarChart
        colunas={[coluna('2026-09', 'set', 200_000, 85_000)]}
        series={SERIES}
        maximoCents={200_000}
        legenda="legenda"
      />,
    )

    const desenhadas = barras(container)
    expect(desenhadas).toHaveLength(2)

    // Barra k da coluna i: x = 40·i + 5,5 + k·16, largura 13.
    expect(desenhadas[0]?.getAttribute('x')).toBe('5.5')
    expect(desenhadas[0]?.getAttribute('width')).toBe('13')
    expect(desenhadas[1]?.getAttribute('x')).toBe('21.5')

    // Aporte é sempre o primeiro e leva --chart-1; resgate é o segundo e leva
    // --chart-3. Nunca 1 e 2: duas séries que se comparam barra contra barra
    // precisam de dois passos de separação.
    expect(desenhadas[0]?.getAttribute('data-fill')).toBe('1')
    expect(desenhadas[1]?.getAttribute('data-fill')).toBe('3')

    // Base em y = 236, topo útil em y = 4.
    expect(desenhadas[0]?.getAttribute('height')).toBe('232')
    expect(desenhadas[0]?.getAttribute('y')).toBe('4')
    expect(desenhadas[1]?.getAttribute('height')).toBe('99')
    expect(desenhadas[1]?.getAttribute('y')).toBe('137')
  })

  it('mês zerado não desenha barra, e a coluna vizinha continua no lugar', () => {
    const { container } = render(
      <BarChart
        colunas={[coluna('a', 'ago', 0, 0), coluna('b', 'set', 200_000, 0)]}
        series={SERIES}
        maximoCents={200_000}
        legenda="legenda"
      />,
    )

    const desenhadas = barras(container)
    expect(desenhadas).toHaveLength(1)
    // A barra que sobrou é a de aportes de setembro: x = 40·1 + 5,5.
    expect(desenhadas[0]?.getAttribute('x')).toBe('45.5')
  })

  it('cada barra leva o <title> da sua posição', () => {
    const { container } = render(
      <BarChart
        colunas={[coluna('2026-09', 'set', 200_000, 85_000)]}
        series={SERIES}
        maximoCents={200_000}
        legenda="legenda"
      />,
    )

    const titulos = Array.from(plotagem(container).querySelectorAll('rect[data-fill] > title')).map(
      (titulo) => titulo.textContent,
    )
    expect(titulos).toEqual(['2026-09 · Aportes', '2026-09 · Resgates'])
  })

  it('marca a virada de ano antes de janeiro, e nunca na primeira coluna', () => {
    const { container } = render(
      <BarChart colunas={dozeColunas()} series={SERIES} maximoCents={200_000} legenda="legenda" />,
    )

    // Uma linha vertical só (a base é horizontal e tem y1 = y2).
    const verticais = Array.from(plotagem(container).querySelectorAll('line')).filter(
      (linha) => linha.getAttribute('x1') === linha.getAttribute('x2'),
    )
    expect(verticais).toHaveLength(1)
    // Janeiro é a 4ª coluna (índice 3): x = 40 · 3.
    expect(verticais[0]?.getAttribute('x1')).toBe('120')
    expect(verticais[0]?.getAttribute('y1')).toBe('4')
    expect(verticais[0]?.getAttribute('y2')).toBe('236')
  })

  it('janeiro como primeira coluna não ganha marca de virada', () => {
    const { container } = render(
      <BarChart
        colunas={[coluna('jan', 'jan', 100_000, 0, true), coluna('fev', 'fev', 0, 0)]}
        series={SERIES}
        maximoCents={100_000}
        legenda="legenda"
      />,
    )

    const verticais = Array.from(plotagem(container).querySelectorAll('line')).filter(
      (linha) => linha.getAttribute('x1') === linha.getAttribute('x2'),
    )
    expect(verticais).toHaveLength(0)
  })

  it('o SVG é ilustração: aria-hidden e sem foco', () => {
    // O caminho de teclado é a tabela ao lado (ADR-021), não o desenho.
    const { container } = render(
      <BarChart
        colunas={[coluna('a', 'set', 100_000, 0)]}
        series={SERIES}
        maximoCents={100_000}
        legenda="legenda"
      />,
    )

    const svg = plotagem(container)
    expect(svg.getAttribute('aria-hidden')).toBe('true')
    expect(svg.getAttribute('focusable')).toBe('false')
    // Sem eixo Y, sem malha e sem número escrito no desenho.
    expect(plotagem(container).querySelectorAll('text')).toHaveLength(0)
  })

  it('o eixo é HTML e aria-hidden; a legenda não é', () => {
    const { container } = render(
      <BarChart
        colunas={dozeColunas()}
        series={SERIES}
        maximoCents={200_000}
        legenda="Aportes e resgates dos últimos 12 meses — a barra mais alta é R$ 2.000,00."
      />,
    )

    const [eixo, legenda] = Array.from(container.querySelectorAll('ol'))

    // O eixo é HTML (uma <ol>, não <text> do SVG): texto dentro do viewBox
    // encolheria junto com ele e ignoraria o zoom de fonte. E é aria-hidden:
    // doze nomes de mês sem valor nenhum são ruído no áudio, e o mês por
    // extenso está em cada linha da tabela ao lado.
    expect(eixo?.getAttribute('aria-hidden')).toBe('true')
    expect(Array.from(eixo?.querySelectorAll('li') ?? []).map((item) => item.textContent)).toEqual([
      'out',
      'nov',
      'dez',
      'jan',
      'fev',
      'mar',
      'abr',
      'mai',
      'jun',
      'jul',
      'ago',
      'set',
    ])

    // A legenda NÃO é escondida: ela nomeia as duas séries em duas palavras e é
    // o que liga a figura à tabela para quem enxerga.
    expect(legenda?.getAttribute('aria-hidden')).toBeNull()
    expect(screen.getByText('Aportes')).toBeInTheDocument()
    expect(screen.getByText('Resgates')).toBeInTheDocument()

    // A escala é dita em PALAVRAS, porque não há eixo Y nem número sobre barra.
    expect(
      screen.getByText(/a barra mais alta é R\$ 2\.000,00/, { selector: 'figcaption' }),
    ).toBeInTheDocument()
  })

  it('a legenda traz SEMPRE as duas séries, mesmo com uma sem nenhuma barra', () => {
    // Sem a legenda completa, a tinta que sobrou vira ambígua.
    const { container } = render(
      <BarChart
        colunas={[coluna('a', 'set', 100_000, 0)]}
        series={SERIES}
        maximoCents={100_000}
        legenda="legenda"
      />,
    )

    expect(barras(container)).toHaveLength(1)
    expect(screen.getByText('Aportes')).toBeInTheDocument()
    expect(screen.getByText('Resgates')).toBeInTheDocument()
  })

  it('carregando: 24 barras de platô, sem título e sem tinta de série', () => {
    const { container } = render(
      <BarChart
        colunas={dozeColunas()}
        series={SERIES}
        maximoCents={0}
        legenda="legenda"
        loading
      />,
    )

    // Nenhuma barra de dado...
    expect(barras(container)).toHaveLength(0)
    // ...e 24 de platô, todas na mesma altura (40 % de 232).
    const platos = Array.from(plotagem(container).querySelectorAll('rect'))
    expect(platos).toHaveLength(24)
    expect(new Set(platos.map((plato) => plato.getAttribute('height')))).toEqual(new Set(['93']))
    expect(plotagem(container).querySelectorAll('title')).toHaveLength(0)

    // A figura anuncia-se ocupada, mas quem fala é a DataTable ao lado: aqui
    // não há `role="status"`.
    const figura = container.querySelector('figure')
    expect(figura?.getAttribute('aria-busy')).toBe('true')
    expect(container.querySelector('[role="status"]')).toBeNull()

    // O eixo e a base são REAIS mesmo carregando: derivam do mês da URL.
    const base = Array.from(plotagem(container).querySelectorAll('line')).find(
      (linha) => linha.getAttribute('y1') === '236' && linha.getAttribute('y2') === '236',
    )
    expect(base?.getAttribute('x2')).toBe('480')
  })
})
