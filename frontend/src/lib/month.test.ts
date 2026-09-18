import { describe, expect, it } from 'vitest'
import {
  mesCorrente,
  mesCurto,
  mesDaURL,
  mesDe,
  mesPorExtenso,
  mesPorExtensoCapitalizado,
  mesValido,
  somarMeses,
} from './month'

describe('mês da aplicação', () => {
  it('reconhece só a forma YYYY-MM', () => {
    for (const bom of ['2026-01', '2026-09', '2026-12', '1970-01']) {
      expect(mesValido(bom), bom).toBe(true)
    }
    for (const ruim of [
      '',
      '2026',
      '2026-1',
      '2026-00',
      '2026-13',
      '26-09',
      '2026/09',
      '2026-09-12',
    ]) {
      expect(mesValido(ruim), ruim).toBe(false)
    }
  })

  // O teste que justifica o módulo existir. Às 21h de 30/09 em São Paulo já é
  // 1º de outubro em UTC: quem calculasse o mês no fuso errado abriria o app
  // em outubro e veria setembro "vazio".
  it('o mês é o da CASA, não o do servidor', () => {
    const viradaEmUTC = new Date('2026-10-01T00:30:00Z')

    expect(mesDe(viradaEmUTC, 'UTC')).toBe('2026-10')
    expect(mesDe(viradaEmUTC, 'America/Sao_Paulo')).toBe('2026-09')
  })

  it('funciona também na virada do ano', () => {
    const reveillon = new Date('2027-01-01T01:00:00Z')
    expect(mesDe(reveillon, 'UTC')).toBe('2027-01')
    expect(mesDe(reveillon, 'America/Sao_Paulo')).toBe('2026-12')
  })

  it('fuso desconhecido cai no padrão em vez de derrubar a tela', () => {
    const instante = new Date('2026-09-15T12:00:00Z')
    expect(mesDe(instante, 'Marte/Olympus_Mons')).toBe('2026-09')
    expect(mesDe(instante, '')).toBe('2026-09')
  })

  it('mesCorrente usa o instante informado', () => {
    expect(mesCorrente('America/Sao_Paulo', new Date('2026-03-10T15:00:00Z'))).toBe('2026-03')
  })

  it('soma e subtrai meses atravessando o ano', () => {
    expect(somarMeses('2026-09', 1)).toBe('2026-10')
    expect(somarMeses('2026-12', 1)).toBe('2027-01')
    expect(somarMeses('2026-01', -1)).toBe('2025-12')
    expect(somarMeses('2026-09', 12)).toBe('2027-09')
    expect(somarMeses('2026-09', -12)).toBe('2025-09')
    expect(somarMeses('2026-09', 0)).toBe('2026-09')
  })

  // O cálculo é sobre os números do mês, nunca sobre um Date: se passasse por
  // Date, "31 de janeiro + 1 mês" viraria 3 de março.
  it('somar meses nunca escorrega de mês', () => {
    let mes = '2026-01'
    const visitados: string[] = []
    for (let i = 0; i < 14; i++) {
      visitados.push(mes)
      mes = somarMeses(mes, 1)
    }
    expect(visitados).toEqual([
      '2026-01',
      '2026-02',
      '2026-03',
      '2026-04',
      '2026-05',
      '2026-06',
      '2026-07',
      '2026-08',
      '2026-09',
      '2026-10',
      '2026-11',
      '2026-12',
      '2027-01',
      '2027-02',
    ])
  })

  it('escreve o mês por extenso em pt-BR', () => {
    expect(mesPorExtenso('2026-09')).toBe('setembro de 2026')
    expect(mesPorExtenso('2026-03')).toBe('março de 2026')
    expect(mesPorExtensoCapitalizado('2026-01')).toBe('Janeiro de 2026')
  })

  it('abrevia o mês em minúsculas e sem ponto — só para o eixo do gráfico', () => {
    // Único lugar do app com mês abreviado (docs/DESIGN.md, E7 (c)): o eixo do
    // gráfico de 12 meses, que é decorativo e `aria-hidden`. Em qualquer texto
    // lido, o mês continua por extenso.
    expect(mesCurto('2026-01')).toBe('jan')
    expect(mesCurto('2026-09')).toBe('set')
    expect(mesCurto('2025-12')).toBe('dez')
    expect(mesCurto('2026-03')).toBe('mar')
    // Sem ponto, sem maiúscula e sem acento — três letras, doze vezes.
    for (let numero = 1; numero <= 12; numero += 1) {
      const curto = mesCurto(`2026-${String(numero).padStart(2, '0')}`)
      expect(curto).toMatch(/^[a-z]{3}$/)
    }
  })

  it('mês malformado não quebra a abreviação', () => {
    expect(mesCurto('banana')).toBe('jan')
  })

  // A URL é editável pela pessoa: `?mes=banana` não pode quebrar a tela.
  it('mês inválido na URL cai no corrente', () => {
    const agora = new Date('2026-09-15T12:00:00Z')
    const fuso = 'America/Sao_Paulo'

    expect(mesDaURL('2026-05', fuso, agora)).toBe('2026-05')
    expect(mesDaURL(undefined, fuso, agora)).toBe('2026-09')
    expect(mesDaURL('', fuso, agora)).toBe('2026-09')
    expect(mesDaURL('banana', fuso, agora)).toBe('2026-09')
    expect(mesDaURL('2026-13', fuso, agora)).toBe('2026-09')
    expect(mesDaURL('<script>', fuso, agora)).toBe('2026-09')
  })
})
