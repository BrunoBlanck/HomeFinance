import type { TransactionKind } from '@/api/types'

/** A natureza de um lançamento e o que ela decide sobre o dinheiro.
 *
 *  Mora em `lib/` porque **duas features dependem da mesma regra**: a tabela de
 *  lançamentos e a revisão da importação escrevem o mesmo valor com o mesmo
 *  sinal, e import entre features é proibido (`AGENTS.md`). Duplicar um `switch`
 *  de cinco linhas parece barato até as duas cópias divergirem — e a divergência
 *  aqui exibe uma despesa como receita.
 *
 *  A regra do contrato, que este módulo concentra: **`amountCents` é sempre
 *  não-negativo, e o sinal vem do `kind`**. */

/** O valor COM SINAL, em centavos.
 *
 *  `switch` exaustivo sem `default`: um `kind` novo no backend vira erro de
 *  compilação aqui, em vez de cair num `else` e mostrar o valor invertido. */
export function valorComSinal(kind: TransactionKind, centavos: number): number {
  switch (kind) {
    case 'income':
    case 'transfer_in':
      return centavos
    case 'expense':
    case 'transfer_out':
      return -centavos
  }
}

/** `true` nas duas pernas de uma transferência.
 *
 *  Transferência não é receita nem despesa: não entra em subtotal, não entra em
 *  resumo e não ganha `--income`/`--expense` (docs/DESIGN.md, princípio 3).
 *  Dinheiro que troca de bolso dentro da casa não mudou o patrimônio da casa. */
export function ehTransferencia(kind: TransactionKind): boolean {
  return kind === 'transfer_out' || kind === 'transfer_in'
}
