import { describe, expect, it } from 'vitest'
import {
  firstName,
  formatarParticipacao,
  formatCentavos,
  formatCountdown,
  formatFullDate,
} from './format'

describe('format', () => {
  it('formata pontos-base como percentual, com as casas pedidas', () => {
    // Duas casas: a tabela. `4123` bp são 41,23 % exatos — nenhum arredondamento.
    expect(formatarParticipacao(4123, 2)).toBe('41,23%')
    expect(formatarParticipacao(10000, 2)).toBe('100,00%')
    expect(formatarParticipacao(0, 2)).toBe('0,00%')
    expect(formatarParticipacao(1, 2)).toBe('0,01%')
    // Uma casa: legenda e <title> da rosca.
    expect(formatarParticipacao(4123, 1)).toBe('41,2%')
    expect(formatarParticipacao(590, 1)).toBe('5,9%')
    expect(formatarParticipacao(10000, 1)).toBe('100,0%')
  })

  // Bordas do percentual (QA E6a): os dois extremos que a rosca produz de
  // verdade. `9999` é o mês em que uma única categoria ficou com tudo menos um
  // ponto-base — com duas casas ele NÃO pode virar `100,00%`, senão a tabela
  // some com a diferença; e com UMA casa ele arredonda para `100,0%`, que é o
  // preço combinado da legenda (docs/DESIGN.md E6a (a) 6) e por isso está aqui
  // por escrito, e não por acidente.
  it('99,99% não vira 100% na tabela, e 0,01% não vira zero', () => {
    expect(formatarParticipacao(9999, 2)).toBe('99,99%')
    expect(formatarParticipacao(9999, 1)).toBe('100,0%')
    expect(formatarParticipacao(1, 1)).toBe('0,0%')
    expect(formatarParticipacao(1, 2)).toBe('0,01%')
    // E a soma das duas fecha a tabela em 100,00%.
    expect(formatarParticipacao(9999 + 1, 2)).toBe('100,00%')
  })

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
