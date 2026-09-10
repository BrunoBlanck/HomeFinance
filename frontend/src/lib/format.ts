const currency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })
const fullDate = new Intl.DateTimeFormat('pt-BR', { dateStyle: 'full' })

/** Dinheiro trafega em centavos (inteiro) e só vira decimal aqui, na borda. */
export function formatCentavos(cents: number): string {
  return currency.format(cents / 100)
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
