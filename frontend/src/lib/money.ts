import { formatCentavos } from './format'

/** Dinheiro no cliente.
 *
 *  Regra que este módulo existe para sustentar (ADR-003): dinheiro é **inteiro
 *  em centavos** em toda camada — estado do React, corpo da requisição e
 *  formulário. Nenhuma função daqui devolve `number` fracionário, e a divisão
 *  por 100 acontece **uma única vez**, na formatação.
 *
 *  Por que isso importa num app doméstico: `0.1 + 0.2 !== 0.3` em ponto
 *  flutuante. Somar cem lançamentos em reais produz centavos fantasmas, e o
 *  usuário vê um total que não bate com a conta dele. */

/** Teto de sanidade, o mesmo do backend (§4.5 do PLANOS.md): R$ 999.999.999,99. */
export const MAX_CENTAVOS = 99_999_999_999

/** Como o sinal aparece no texto formatado.
 *
 *  `'automatico'` é o comportamento de sempre: só o negativo mostra sinal.
 *  `'sempre'` acrescenta o `+` no positivo.
 *
 *  Para que serve o `'sempre'`: numa coluna em que receita e despesa convivem
 *  — a tabela de lançamentos e a revisão de importação —, a direção do dinheiro
 *  não pode depender de `--income`/`--expense`. Cor é reforço, nunca o portador
 *  único da informação (docs/DESIGN.md): com `+` e `−` explícitos, a coluna
 *  continua legível em escala de cinza, no daltonismo e impressa.
 *
 *  **Zero não ganha sinal, nem no `'sempre'`** (`signDisplay: 'exceptZero'`,
 *  não `'always'`). O `+` existe para dizer a direção do dinheiro, e zero não
 *  tem direção: um dia em que entrada e saída se anulam sairia como `+0,00`,
 *  que afirma "entrou" sobre um saldo que não entrou nem saiu. */
export type SinalDeValor = 'automatico' | 'sempre'

export type OpcoesDeFormato = {
  sinal?: SinalDeValor | undefined
}

const DUAS_CASAS = { minimumFractionDigits: 2, maximumFractionDigits: 2 } as const

// Formatadores no topo do módulo: construir um `Intl.NumberFormat` custa caro e
// numa tabela de 200 linhas isso acontece uma vez por célula.
const valorSemSinal = new Intl.NumberFormat('pt-BR', DUAS_CASAS)
const valorComSinal = new Intl.NumberFormat('pt-BR', { ...DUAS_CASAS, signDisplay: 'exceptZero' })
const dinheiroComSinal = new Intl.NumberFormat('pt-BR', {
  style: 'currency',
  currency: 'BRL',
  signDisplay: 'exceptZero',
})

/** Formata para exibição: "R$ 1.234,56". Valor negativo sai com o sinal. */
export function formatarDinheiro(centavos: number, opcoes?: OpcoesDeFormato): string {
  if (opcoes?.sinal === 'sempre') return dinheiroComSinal.format(centavos / 100)
  return formatCentavos(centavos)
}

/** Formata sem o símbolo da moeda — para célula de tabela onde o cabeçalho já
 *  diz que a coluna é dinheiro, e repetir "R$" em cada linha é ruído. */
export function formatarValor(centavos: number, opcoes?: OpcoesDeFormato): string {
  const formatador = opcoes?.sinal === 'sempre' ? valorComSinal : valorSemSinal
  return formatador.format(centavos / 100)
}

/** Converte o que o usuário digitou em centavos, lendo **só os dígitos**.
 *
 *  A entrada é "centavos primeiro", que é como teclado numérico de celular e
 *  caixa de supermercado funcionam: digitar `1`, `2`, `3`, `4` vira
 *  R$ 12,34. Não há vírgula para acertar, não há como errar a casa decimal, e
 *  colar "R$ 1.234,56" funciona porque tudo o que não é dígito é descartado.
 *
 *  O sinal é decidido em outro lugar (o `kind` do lançamento, ou o campo de
 *  saldo de abertura); esta função devolve sempre o módulo. */
export function centavosDeDigitos(texto: string): number {
  const digitos = texto.replace(/\D/g, '')
  if (digitos === '') return 0
  // `slice` antes do parse: um colar gigante viraria Infinity em Number(), e
  // aí o teto nunca seria alcançado — ele seria pulado.
  const limitado = digitos.replace(/^0+(?=\d)/, '').slice(0, 15)
  const valor = Number(limitado)
  return Number.isFinite(valor) ? Math.min(valor, MAX_CENTAVOS) : MAX_CENTAVOS
}

/** Texto a mostrar no campo enquanto se digita: sempre com duas casas. */
export function digitosParaTexto(centavos: number): string {
  return formatarValor(Math.abs(centavos))
}

/** `true` quando o valor está dentro da faixa que o backend aceita. */
export function dentroDaFaixa(centavos: number): boolean {
  return Number.isInteger(centavos) && Math.abs(centavos) <= MAX_CENTAVOS
}

/** Tom semântico de um valor, para a UI escolher a cor.
 *
 *  `zero` é neutro de propósito: pintar R$ 0,00 de verde ou vermelho sugere um
 *  movimento que não houve. */
export type TomDeValor = 'positivo' | 'negativo' | 'zero'

export function tomDoValor(centavos: number): TomDeValor {
  if (centavos > 0) return 'positivo'
  if (centavos < 0) return 'negativo'
  return 'zero'
}
