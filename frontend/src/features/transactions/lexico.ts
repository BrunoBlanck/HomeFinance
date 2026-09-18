import type { Transaction } from '@/api/types'
import { dataCompleta } from '@/lib/civil'
import { formatarDinheiro } from '@/lib/money'

/** O léxico de `/lancamentos` — o que a tela e os componentes da feature dizem
 *  sobre uma linha, escrito uma vez.
 *
 *  Mora fora da tela porque o atalho de categorização (docs/DESIGN.md, E2c (h))
 *  nomeia o botão da célula, a legenda do editor e o `<select>` com o MESMO
 *  rótulo que o botão de excluir já usava — e um componente importando de uma
 *  tela seria a dependência ao contrário. */

/** Nome da linha para o leitor de tela: descrição, data e valor.
 *
 *  Nunca só "Excluir" ou "Sem categoria": numa tabela de 50 linhas, cinquenta
 *  botões com o mesmo nome deixam quem navega por lista de controles sem saber
 *  qual é qual — e aqui errar a linha apaga dinheiro. */
export function rotuloDaLinha(linha: Transaction): string {
  const descricao = linha.description.trim() || 'lançamento sem descrição'
  const valor = formatarDinheiro(Math.abs(linha.amountCents))
  return `${descricao}, ${dataCompleta(linha.occurredOn)}, ${valor}`
}
