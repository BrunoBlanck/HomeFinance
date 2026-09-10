import { describe, expect, it } from 'vitest'
import { firstName, formatCentavos, formatCountdown, formatFullDate } from './format'

describe('format', () => {
  it('formata centavos como moeda brasileira', () => {
    //   é o espaço não separável que o Intl coloca depois de R$.
    expect(formatCentavos(129900)).toBe('R$ 1.299,00')
    expect(formatCentavos(0)).toBe('R$ 0,00')
    expect(formatCentavos(-4599)).toBe('-R$ 45,99')
  })

  it('formata a contagem regressiva em m:ss', () => {
    expect(formatCountdown(47)).toBe('0:47')
    expect(formatCountdown(60)).toBe('1:00')
    expect(formatCountdown(125)).toBe('2:05')
    expect(formatCountdown(-3)).toBe('0:00')
  })

  it('escreve a data por extenso com inicial maiúscula', () => {
    // 09/09/2026 cai numa quarta-feira — a spec usa "terça" só como exemplo de forma.
    expect(formatFullDate(new Date(2026, 8, 9))).toBe('Quarta-feira, 9 de setembro de 2026')
    expect(formatFullDate(new Date(2026, 8, 8))).toBe('Terça-feira, 8 de setembro de 2026')
  })

  it('extrai o primeiro nome', () => {
    expect(firstName('Bruno Blanck')).toBe('Bruno')
    expect(firstName('  Ana  Maria ')).toBe('Ana')
    expect(firstName('Bruno')).toBe('Bruno')
  })
})
