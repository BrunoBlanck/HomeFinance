import { useId } from 'react'
import { formatarParticipacao } from '@/lib/format'
import { formatarDinheiro } from '@/lib/money'
import { MoneyText } from '../MoneyText/MoneyText'
import { Skeleton } from '../Skeleton/Skeleton'
import { Swatch } from '../Swatch/Swatch'
import styles from './DonutChart.module.css'
import type { Fatia } from './dobrar'

export type { Fatia, GrupoDaRosca, PapelDaFatia } from './dobrar'
export { CHAVE_OUTRAS, dobrarParaRosca, MAX_FATIAS_NOMEADAS } from './dobrar'

type DonutChartProps = {
  /** ≤ 6, já dobradas, na ordem do anel (horário, a partir do topo). */
  fatias: readonly Fatia[]
  /** O total do mês, **do servidor**. O cliente não o recalcula somando fatias. */
  totalCents: number
  /** O que vai sob o número: "setembro". */
  rotuloDoCentro: string
  /** Texto do `<figcaption>`. */
  legenda: string
  loading?: boolean | undefined
}

// Unidades do viewBox. O anel tem 30 de espessura e o furo 172 de diâmetro —
// é o furo que dita o corpo do número no centro.
const CENTRO = 120
const RAIO_EXTERNO = 116
const RAIO_INTERNO = 86

/** O anel inteiro, para a fatia única e para o estado de carregamento.
 *  Dois subcaminhos com `fill-rule="evenodd"`: o de dentro fura o de fora.
 *  É `<path>`, e não `<circle>`, para que UMA regra de `fill` sirva a todos os
 *  casos — num `<circle>` a cor iria no `stroke`, e `forced-colors` passaria a
 *  ter duas regras para manter. */
const ANEL_INTEIRO =
  'M120 4A116 116 0 1 1 120 236A116 116 0 1 1 120 4Z' +
  'M120 34A86 86 0 1 0 120 206A86 86 0 1 0 120 34Z'

/** Rosca de "parte do todo" (docs/DESIGN.md, E6a; ADR-021: SVG próprio, sem
 *  biblioteca de gráfico).
 *
 *  Três decisões que este componente sustenta:
 *
 *  1. **O SVG é decorativo.** `aria-hidden` e `focusable="false"`: a fonte dos
 *     números é a tabela logo abaixo, com `caption`. O `<title>` nativo por
 *     fatia é cortesia para quem usa mouse — não há tooltip próprio, não há
 *     foco em fatia, e o caminho de teclado é a tabela.
 *  2. **Cor é tinta, não matiz.** As fatias saem da rampa `--chart-1..4`
 *     (ordinal, de `--ink` para `--surface`), da maior para a menor, mais
 *     `--chart-pending` para "Sem categoria" e hachura para "Outras". Ordem no
 *     anel, ordem na legenda e ordem de luminosidade são o mesmo eixo — o
 *     gráfico continua legível em `grayscale(1)`.
 *  3. **O cliente não calcula dinheiro.** O total vem do servidor, os
 *     percentuais vêm em pontos-base, e o ângulo é geometria (proporção sobre
 *     a soma das fatias visíveis), não contabilidade. */
export function DonutChart({
  fatias,
  totalCents,
  rotuloDoCentro,
  legenda,
  loading = false,
}: DonutChartProps) {
  const id = useId().replace(/[^a-zA-Z0-9-]/g, '')
  const hachuraId = `hf-rosca-${id}`
  const arcos = loading ? [] : arcosDaRosca(fatias)

  return (
    <figure className={styles.figura} aria-busy={loading || undefined}>
      <div className={styles.rosca}>
        {/* Decorativo (ADR-021): a fonte dos números é a tabela com `caption`
            logo abaixo. `<title>` por fatia é cortesia para quem usa mouse. */}
        <svg viewBox="0 0 240 240" aria-hidden="true" focusable="false" className={styles.svg}>
          <defs>
            <pattern
              id={hachuraId}
              width="6"
              height="6"
              patternUnits="userSpaceOnUse"
              patternTransform="rotate(45)"
            >
              <line
                x1="0"
                y1="0"
                x2="0"
                y2="6"
                className={styles.hachura}
                vectorEffect="non-scaling-stroke"
              />
            </pattern>
          </defs>

          {arcos.length === 0 ? (
            <path className={styles.anelVazio} fillRule="evenodd" d={ANEL_INTEIRO} />
          ) : (
            arcos.map(({ fatia, d }) => (
              <path
                key={fatia.key}
                className={styles.fatia}
                data-fill={fatia.papel}
                fillRule="evenodd"
                vectorEffect="non-scaling-stroke"
                d={d}
                {...(fatia.papel === 'outras' ? { fill: `url(#${hachuraId})` } : {})}
              >
                <title>{tituloDaFatia(fatia)}</title>
              </path>
            ))
          )}
        </svg>

        <p className={styles.centro}>
          {loading ? (
            <Skeleton width="6rem" height="1.375rem" />
          ) : (
            <>
              <span className="sr-only">Total de </span>
              <MoneyText cents={totalCents} emphasis="hero" />
            </>
          )}
          <span className={styles.centroRotulo}>
            <span className="sr-only">em </span>
            {rotuloDoCentro}
          </span>
        </p>
      </div>

      {/* Sempre presente: com uma fatia só ela é uma linha, e é ela que diz o
          nome da categoria única. A ordem é a do anel.
          `role="list"` porque o CSS tira o marcador, e sem o papel explícito o
          VoiceOver deixa de anunciar a lista (a regra está em reset.css). */}
      {/* biome-ignore lint/a11y/noRedundantRoles: o papel e reposto de proposito, ver reset.css */}
      <ol className={styles.legenda} role="list">
        {loading
          ? [0, 1, 2].map((indice) => (
              <li key={indice}>
                <Skeleton width="10rem" height="1rem" />
              </li>
            ))
          : fatias.map((fatia) => (
              <li key={fatia.key}>
                <Swatch papel={fatia.papel} />
                <span className={styles.nome}>{fatia.label}</span>
                <span className={styles.parte}>{formatarParticipacao(fatia.shareBp, 1)}</span>
              </li>
            ))}
      </ol>

      <figcaption className={styles.figcaption}>{legenda}</figcaption>
    </figure>
  )
}

/** "Alimentação · R$ 2.100,00 · 41,2%" — o `<title>` nativo da fatia. O nome
 *  entra como filho de JSX, nunca como HTML. */
function tituloDaFatia(fatia: Fatia): string {
  return `${fatia.label} · ${formatarDinheiro(fatia.cents)} · ${formatarParticipacao(fatia.shareBp, 1)}`
}

/** Os caminhos das fatias, no sentido horário a partir do topo.
 *
 *  A proporção é de `shareBp` sobre a soma das fatias **visíveis** — geometria,
 *  não dinheiro: se a pessoa vê cinco fatias, elas precisam fechar o círculo,
 *  mesmo que o servidor tenha mandado uma sexta com zero. */
export function arcosDaRosca(fatias: readonly Fatia[]): { fatia: Fatia; d: string }[] {
  // Fatia sem participação não é desenhada — nem como traço de largura zero,
  // que apareceria como um risco no anel.
  const visiveis = fatias.filter((fatia) => fatia.shareBp > 0)
  const soma = visiveis.reduce((total, fatia) => total + fatia.shareBp, 0)
  if (soma <= 0) return []
  if (visiveis.length === 1) {
    const unica = visiveis[0]
    return unica ? [{ fatia: unica, d: ANEL_INTEIRO }] : []
  }

  const saida: { fatia: Fatia; d: string }[] = []
  let anguloInicial = 0
  for (const fatia of visiveis) {
    const anguloFinal = anguloInicial + (fatia.shareBp / soma) * 2 * Math.PI
    saida.push({ fatia, d: caminhoDaFatia(anguloInicial, anguloFinal) })
    anguloInicial = anguloFinal
  }
  return saida
}

function caminhoDaFatia(a0: number, a1: number): string {
  const grande = a1 - a0 > Math.PI ? 1 : 0
  const externoInicio = ponto(a0, RAIO_EXTERNO)
  const externoFim = ponto(a1, RAIO_EXTERNO)
  const internoFim = ponto(a1, RAIO_INTERNO)
  const internoInicio = ponto(a0, RAIO_INTERNO)
  return (
    `M${externoInicio} ` +
    `A${RAIO_EXTERNO} ${RAIO_EXTERNO} 0 ${grande} 1 ${externoFim} ` +
    `L${internoFim} ` +
    `A${RAIO_INTERNO} ${RAIO_INTERNO} 0 ${grande} 0 ${internoInicio} Z`
  )
}

/** `P(θ, ρ) = (120 + ρ·sin θ, 120 − ρ·cos θ)` — θ = 0 no topo, crescendo no
 *  sentido horário. */
function ponto(angulo: number, raio: number): string {
  const x = CENTRO + raio * Math.sin(angulo)
  const y = CENTRO - raio * Math.cos(angulo)
  return `${arredondar(x)} ${arredondar(y)}`
}

function arredondar(valor: number): string {
  // Três casas: abaixo da precisão de subpixel em qualquer tamanho que o anel
  // assume, e sem `-0` no caminho.
  return String(Math.round(valor * 1000) / 1000 + 0)
}
