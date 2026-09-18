/** A dobra do anel (docs/DESIGN.md, E6a (a) e (c)).
 *
 *  A rosca tem no máximo **4 fatias nomeadas** — não é gosto, é medida: entre
 *  `--ink` e o limite de 2:1 sobre `--surface` cabem quatro passos da rampa
 *  com ΔE ≥ 15 entre vizinhas nos dois temas; cinco reprovam. Da 5ª categoria
 *  em diante tudo vira uma fatia só, `Outras (N categorias)`, hachurada e
 *  sempre a última. `Sem categoria` é fatia própria, em cor de status, e
 *  **nunca** dobra em Outras — é dinheiro esperando decisão, não um resto.
 *
 *  A tabela lista **todas** as categorias; a dobra é só do anel. Por isso a
 *  função devolve também o papel de **cada** grupo: é com ele que a linha da
 *  tabela mostra a mesma amostra da fatia — e as dobradas mostram a hachura,
 *  que é o mapa "estes, juntos, são a fatia hachurada".
 *
 *  A única aritmética é a soma **inteira** de `cents` e `shareBp` das dobradas.
 *  Nunca se divide centavo aqui: o percentual vem pronto do servidor (ADR-027c). */

import type { PapelDaAmostra } from '../Swatch/Swatch'

/** O papel de uma fatia na rampa. É o MESMO vocabulário da amostra de 12 px
 *  (`components/Swatch`), definido uma vez só: a rosca, a legenda e a linha da
 *  tabela pintam pelo mesmo mapa. */
export type PapelDaFatia = PapelDaAmostra

export type Fatia = {
  key: string
  label: string
  cents: number
  /** Pontos-base (`0..10000`), do servidor. */
  shareBp: number
  papel: PapelDaFatia
}

export type GrupoDaRosca = {
  key: string
  label: string
  cents: number
  shareBp: number
  /** O balde "Sem categoria". */
  pendente?: boolean | undefined
}

export const MAX_FATIAS_NOMEADAS = 4

/** A chave da fatia dobrada — não colide com um UUID nem com a pendente. */
export const CHAVE_OUTRAS = 'outras'

export function rotuloDeOutras(quantas: number): string {
  return quantas === 1 ? 'Outra (1 categoria)' : `Outras (${quantas} categorias)`
}

/** Recebe os grupos **na ordem do servidor** (valor desc) e devolve as fatias
 *  na ordem do anel — horário, a partir do topo — mais o papel de cada grupo
 *  de entrada.
 *
 *  - `shareBp = 0` some do anel, não conta no N de Outras e não recebe papel;
 *  - a pendente fica onde o servidor a pôs e não consome passo da rampa;
 *  - as quatro primeiras nomeadas recebem `'1'`…`'4'`;
 *  - o resto vira **uma** fatia `'outras'`, colocada por último. */
export function dobrarParaRosca(grupos: readonly GrupoDaRosca[]): {
  fatias: Fatia[]
  papelPorChave: ReadonlyMap<string, PapelDaFatia>
} {
  const fatias: Fatia[] = []
  const papelPorChave = new Map<string, PapelDaFatia>()
  const dobradas: GrupoDaRosca[] = []
  let nomeadas = 0

  for (const grupo of grupos) {
    if (grupo.shareBp <= 0) continue

    if (grupo.pendente) {
      papelPorChave.set(grupo.key, 'pendente')
      fatias.push({ ...semPendente(grupo), papel: 'pendente' })
      continue
    }

    if (nomeadas < MAX_FATIAS_NOMEADAS) {
      nomeadas += 1
      const papel = String(nomeadas) as '1' | '2' | '3' | '4'
      papelPorChave.set(grupo.key, papel)
      fatias.push({ ...semPendente(grupo), papel })
      continue
    }

    papelPorChave.set(grupo.key, 'outras')
    dobradas.push(grupo)
  }

  if (dobradas.length > 0) {
    let cents = 0
    let shareBp = 0
    for (const grupo of dobradas) {
      cents += grupo.cents
      shareBp += grupo.shareBp
    }
    fatias.push({
      key: CHAVE_OUTRAS,
      label: rotuloDeOutras(dobradas.length),
      cents,
      shareBp,
      papel: 'outras',
    })
  }

  return { fatias, papelPorChave }
}

function semPendente({ key, label, cents, shareBp }: GrupoDaRosca) {
  return { key, label, cents, shareBp }
}
