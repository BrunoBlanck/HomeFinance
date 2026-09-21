import type { ReactNode } from 'react'
import type { TransferBalance, TransferPair } from '@/api/types'
import { MoneyText } from '@/components/MoneyText/MoneyText'
import { Skeleton } from '@/components/Skeleton/Skeleton'
import { formatarDinheiro } from '@/lib/money'
import { nomeDoMes } from '@/lib/month'
import styles from './TransferPairPanel.module.css'

export type ContaDoPar = {
  id: string
  nome: string
}

type Props = {
  /** A conta do filtro (`?conta`). É ELA que orienta o painel: a primeira
   *  linha é sempre "X → Y", e o líquido é positivo quando X mandou mais. */
  conta: ContaDoPar
  /** A outra conta (`?contraparte`). */
  contraparte: ContaDoPar
  /** O par como o servidor o devolve (A = menor id). `undefined` enquanto
   *  carrega. */
  par: TransferPair | undefined
  saldos: readonly TransferBalance[]
  /** `AAAA-MM`. */
  mes: string
  carregando: boolean
}

/** Painel do par de contas (spec 0005 §4.4.3; docs/DESIGN.md E2c (f)).
 *
 *  Duas `<dl>` lado a lado, não quatro cartões com número gigante: conferir o
 *  que foi e o que voltou entre duas contas é uma leitura vertical de números
 *  alinhados à direita, e é isso que uma lista de definição entrega.
 *
 *  O servidor manda o par orientado por `A` = menor id, porque assim o par é o
 *  mesmo independente do sentido. A pessoa, porém, escolheu uma conta no filtro
 *  — e é dela o ponto de vista. O mapeamento de `aToBCents`/`bToACents`/
 *  `netCents` para "X → Y"/"Y → X"/líquido é por **id**, nunca por posição, e
 *  `orientarPar` é testado para o líquido mostrado ser exatamente `±netCents`.
 *
 *  Tudo neutro: transferência não é receita nem despesa. Só o saldo no fim do
 *  mês usa `tone="semantic"` — saldo é posição, como em `/contas`. */
export function TransferPairPanel({ conta, contraparte, par, saldos, mes, carregando }: Props) {
  const orientado = par ? orientarPar(par, conta.id) : null
  const nome = nomeDoMes(mes)
  const saldoDaConta = saldos.find((saldo) => saldo.accountId === conta.id)
  const saldoDaContraparte = saldos.find((saldo) => saldo.accountId === contraparte.id)

  return (
    <div className={styles.painel} aria-busy={carregando || undefined}>
      <div className={styles.grade}>
        <div className={styles.coluna}>
          <p className={styles.tituloDaLista}>Entre as duas contas</p>
          <dl className={styles.lista}>
            <Linha rotulo={`${conta.nome} → ${contraparte.nome}`}>
              {orientado ? <MoneyText cents={orientado.xParaY} /> : <Esqueleto />}
            </Linha>
            <Linha rotulo={`${contraparte.nome} → ${conta.nome}`}>
              {orientado ? <MoneyText cents={orientado.yParaX} /> : <Esqueleto />}
            </Linha>
            <Linha rotulo="Líquido">
              {orientado ? (
                <MoneyText cents={orientado.liquido} sign="always" emphasis="total" />
              ) : (
                <Esqueleto />
              )}
            </Linha>
          </dl>
        </div>

        <div className={styles.coluna}>
          <p className={styles.tituloDaLista}>Saldo no fim de {nome}</p>
          <dl className={styles.lista}>
            <Linha rotulo={conta.nome}>
              {carregando || !saldoDaConta ? (
                <Esqueleto />
              ) : (
                <MoneyText cents={saldoDaConta.balanceAtMonthEndCents} tone="semantic" />
              )}
            </Linha>
            <Linha rotulo={contraparte.nome}>
              {carregando || !saldoDaContraparte ? (
                <Esqueleto />
              ) : (
                <MoneyText cents={saldoDaContraparte.balanceAtMonthEndCents} tone="semantic" />
              )}
            </Linha>
          </dl>
        </div>
      </div>

      {/* A frase só existe com o dado: direção inventada enquanto carrega seria
          pior do que silêncio. */}
      {orientado ? (
        <p className={styles.frase}>
          {fraseDeDirecao(conta.nome, contraparte.nome, orientado.liquido, nome)}
        </p>
      ) : null}
    </div>
  )
}

function Linha({ rotulo, children }: { rotulo: string; children: ReactNode }) {
  return (
    <div className={styles.linha}>
      <dt>{rotulo}</dt>
      <dd>{children}</dd>
    </div>
  )
}

function Esqueleto() {
  return <Skeleton width="4.5rem" height="1rem" />
}

// --------------------------------------------------------------- cálculo

export type ParOrientado = {
  /** Da conta do filtro para a outra. */
  xParaY: number
  /** Da outra para a conta do filtro. */
  yParaX: number
  /** `xParaY − yParaX`: positivo quando a conta do filtro mandou mais. */
  liquido: number
}

/** Reorienta o par do servidor (A = menor id) pelo ponto de vista da conta do
 *  filtro. `null` quando o par não envolve a conta — não deveria acontecer com
 *  o filtro aplicado, mas um par alheio orientado por acaso mostraria o
 *  dinheiro na direção errada, e direção errada é o pior erro desta tela. */
export function orientarPar(par: TransferPair, contaId: string): ParOrientado | null {
  if (par.accountAId === contaId) {
    return { xParaY: par.aToBCents, yParaX: par.bToACents, liquido: par.netCents }
  }
  if (par.accountBId === contaId) {
    // `0 - n` e não `-n`: com `netCents === 0` a negação daria `-0`, que o
    // formatador escreve como "-0,00".
    return { xParaY: par.bToACents, yParaX: par.aToBCents, liquido: 0 - par.netCents }
  }
  return null
}

/** "Nubank enviou R$ 1.700,00 a mais para C6 em setembro." — sempre na voz de
 *  quem mandou mais. Líquido zero: "As duas contas se equilibraram em setembro:
 *  o que foi, voltou." A direção do dinheiro é dita em PALAVRAS, e o sinal do
 *  líquido é só reforço. */
export function fraseDeDirecao(
  conta: string,
  contraparte: string,
  liquido: number,
  nomeDoMesAtual: string,
): ReactNode {
  if (liquido === 0) {
    return `As duas contas se equilibraram em ${nomeDoMesAtual}: o que foi, voltou.`
  }
  const [quemMandou, quemRecebeu] = liquido > 0 ? [conta, contraparte] : [contraparte, conta]
  return (
    <>
      <strong>{quemMandou}</strong> enviou <strong>{formatarDinheiro(Math.abs(liquido))}</strong> a
      mais para <strong>{quemRecebeu}</strong> em {nomeDoMesAtual}.
    </>
  )
}
