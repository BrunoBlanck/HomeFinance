import { describe, expect, it } from 'vitest'
import {
  centavosDeDigitos,
  dentroDaFaixa,
  digitosParaTexto,
  formatarDinheiro,
  formatarValor,
  MAX_CENTAVOS,
  tomDoValor,
} from './money'

describe('dinheiro no cliente', () => {
  it('formata em pt-BR com duas casas', () => {
    expect(formatarValor(0)).toBe('0,00')
    expect(formatarValor(1)).toBe('0,01')
    expect(formatarValor(123_456)).toBe('1.234,56')
    expect(formatarValor(-8000)).toBe('-80,00')
  })

  // Numa coluna onde receita e despesa convivem, a direção do dinheiro não pode
  // depender de --income/--expense: em escala de cinza as duas cores viram o
  // mesmo tom. O sinal explícito é o portador que sobrevive (docs/DESIGN.md).
  it('acrescenta o "+" no positivo quando o sinal é pedido', () => {
    expect(formatarValor(160_000, { sinal: 'sempre' })).toBe('+1.600,00')
    expect(formatarValor(1, { sinal: 'sempre' })).toBe('+0,01')
  })

  it('o negativo continua com o sinal dele, pedido ou não', () => {
    expect(formatarValor(-1100, { sinal: 'sempre' })).toBe('-11,00')
    expect(formatarValor(-1100, { sinal: 'automatico' })).toBe('-11,00')
    expect(formatarValor(-1100)).toBe('-11,00')
  })

  it('sem pedir o sinal, nada muda — o padrão não mexe em nenhuma tela que já existe', () => {
    expect(formatarValor(160_000, {})).toBe('1.600,00')
    expect(formatarValor(160_000, { sinal: 'automatico' })).toBe('1.600,00')
  })

  it('o sinal pedido também vale com o símbolo da moeda', () => {
    const comSinal = formatarDinheiro(160_000, { sinal: 'sempre' }).replace(/\s/g, ' ')
    expect(comSinal).toBe('+R$ 1.600,00')
    const semPedir = formatarDinheiro(123_456, { sinal: 'automatico' }).replace(/\s/g, ' ')
    expect(semPedir).toBe('R$ 1.234,56')
  })

  it('formata com símbolo quando o contexto pede', () => {
    // O espaço do pt-BR entre "R$" e o número é NBSP; normalizamos para
    // comparar sem depender do byte exato do ICU.
    expect(formatarDinheiro(123_456).replace(/ /g, ' ')).toBe('R$ 1.234,56')
  })

  // A entrada é "centavos primeiro": digitar 1, 2, 3, 4 vira R$ 12,34. Não há
  // vírgula para acertar, e é como caixa de supermercado e teclado de celular
  // funcionam.
  it('lê os dígitos da esquerda para a direita, em centavos', () => {
    expect(centavosDeDigitos('')).toBe(0)
    expect(centavosDeDigitos('1')).toBe(1)
    expect(centavosDeDigitos('12')).toBe(12)
    expect(centavosDeDigitos('123')).toBe(123)
    expect(centavosDeDigitos('1234')).toBe(1234)
  })

  it('descarta tudo o que não é dígito, então colar valor formatado funciona', () => {
    expect(centavosDeDigitos('R$ 1.234,56')).toBe(123_456)
    expect(centavosDeDigitos('1.234,56')).toBe(123_456)
    expect(centavosDeDigitos('  12,34  ')).toBe(1234)
    expect(centavosDeDigitos('12abc34')).toBe(1234)
    expect(centavosDeDigitos('R$')).toBe(0)
    expect(centavosDeDigitos('-50,00')).toBe(5000)
  })

  it('devolve sempre inteiro — nunca ponto flutuante', () => {
    for (const entrada of ['1', '10', '999', '1234567', '0,1', '0,01']) {
      const valor = centavosDeDigitos(entrada)
      expect(Number.isInteger(valor), `${entrada} -> ${valor}`).toBe(true)
    }
  })

  it('respeita o teto de sanidade, inclusive num colar gigante', () => {
    expect(centavosDeDigitos('9'.repeat(11))).toBe(MAX_CENTAVOS)
    expect(centavosDeDigitos('9'.repeat(50))).toBe(MAX_CENTAVOS)
    // Sem o corte antes do parse, 50 noves virariam Infinity e o teto passaria
    // batido.
    expect(Number.isFinite(centavosDeDigitos('9'.repeat(400)))).toBe(true)
  })

  it('zeros à esquerda não inflam o valor', () => {
    expect(centavosDeDigitos('0001234')).toBe(1234)
    expect(centavosDeDigitos('000')).toBe(0)
  })

  it('o texto do campo acompanha os dígitos, sempre com duas casas', () => {
    expect(digitosParaTexto(0)).toBe('0,00')
    expect(digitosParaTexto(5)).toBe('0,05')
    expect(digitosParaTexto(1234)).toBe('12,34')
    // O campo mostra o módulo; o sinal é decidido fora dele.
    expect(digitosParaTexto(-1234)).toBe('12,34')
  })

  it('a faixa aceita é a mesma do backend, e é simétrica', () => {
    expect(dentroDaFaixa(0)).toBe(true)
    expect(dentroDaFaixa(MAX_CENTAVOS)).toBe(true)
    expect(dentroDaFaixa(-MAX_CENTAVOS)).toBe(true)
    expect(dentroDaFaixa(MAX_CENTAVOS + 1)).toBe(false)
    expect(dentroDaFaixa(-MAX_CENTAVOS - 1)).toBe(false)
    expect(dentroDaFaixa(12.5)).toBe(false)
  })

  // Zero é neutro de propósito: pintar R$ 0,00 de verde ou de vermelho sugere
  // um movimento que não houve.
  it('o tom do valor separa positivo, negativo e zero', () => {
    expect(tomDoValor(1)).toBe('positivo')
    expect(tomDoValor(-1)).toBe('negativo')
    expect(tomDoValor(0)).toBe('zero')
  })
})
