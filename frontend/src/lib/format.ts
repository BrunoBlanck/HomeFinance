const currency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })
const fullDate = new Intl.DateTimeFormat('pt-BR', { dateStyle: 'full' })

/** Dinheiro trafega em centavos (inteiro) e só vira decimal aqui, na borda. */
export function formatCentavos(cents: number): string {
  return currency.format(cents / 100)
}

/** Casas decimais do percentual: duas na tabela (as linhas fecham em
 *  `100,00%`), uma na legenda e no `<title>` da rosca (o relance). */
export type CasasDeParticipacao = 1 | 2

// Um formatador por número de casas, construído uma vez: numa tabela de 40
// linhas o `Intl.NumberFormat` seria instanciado a cada célula.
const participacao: Record<CasasDeParticipacao, Intl.NumberFormat> = {
  1: new Intl.NumberFormat('pt-BR', {
    style: 'percent',
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }),
  2: new Intl.NumberFormat('pt-BR', {
    style: 'percent',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }),
}

/** Participação em pontos-base (`0..10000`, ADR-027c) → `41,23%` / `41,2%`.
 *
 *  **Formatação, não cálculo.** O percentual é apurado no servidor pelo método
 *  do maior resto, e é por isso que as linhas da tabela somam exatamente
 *  `100,00%` sem nota de arredondamento. A única divisão aqui é a que o
 *  `Intl` exige para escrever o número — o cliente nunca calcula participação
 *  a partir de centavos. */
export function formatarParticipacao(bp: number, casas: CasasDeParticipacao): string {
  return participacao[casas].format(bp / 10000)
}

/** "Terça-feira, 9 de setembro de 2026" — o pt-BR devolve minúscula no dia da semana. */
export function formatFullDate(date: Date): string {
  const text = fullDate.format(date)
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/** Contagem regressiva em `m:ss`. */
export function formatCountdown(totalSeconds: number): string {
  const safe = Math.max(0, Math.floor(totalSeconds))
  const minutes = Math.floor(safe / 60)
  const seconds = safe % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}

export function firstName(fullName: string): string {
  const [first] = fullName.trim().split(/\s+/)
  return first ?? fullName
}
