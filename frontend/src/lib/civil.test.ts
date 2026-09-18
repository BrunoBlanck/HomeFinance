import { afterAll, describe, expect, it } from 'vitest'
import {
  civilValida,
  dataCompleta,
  dataCurta,
  diaPorExtenso,
  mesDaDataCivil,
  partesCivis,
} from './civil'

const TZ_ORIGINAL = process.env.TZ

afterAll(() => {
  process.env.TZ = TZ_ORIGINAL
})

/** O fuso do PROCESSO muda entre os blocos de propósito.
 *
 *  Todo o valor deste arquivo está aqui: as funções de `civil.ts` pinam
 *  `timeZone: 'UTC'`, e é isso que precisa ser provado — não que elas formatam
 *  bonito, mas que formatam IGUAL com o processo em UTC e com o processo em
 *  São Paulo. Um teste rodando num fuso só passaria com a implementação errada
 *  metade das vezes, dependendo de onde a suíte fosse executada. */
describe.each(['UTC', 'America/Sao_Paulo', 'Pacific/Kiritimati'])(
  'fuso do processo: %s',
  (fuso) => {
    process.env.TZ = fuso

    it('escreve o dia do cabeçalho sem deslocar a data', () => {
      process.env.TZ = fuso
      expect(diaPorExtenso('2026-08-31')).toBe('segunda-feira, 31 de agosto')
      expect(diaPorExtenso('2026-01-01')).toBe('quinta-feira, 1 de janeiro')
    })

    it('escreve a data curta e a completa sem deslocar o dia', () => {
      process.env.TZ = fuso
      expect(dataCurta('2026-08-31')).toBe('31/08')
      expect(dataCompleta('2026-08-31')).toBe('31/08/2026')
      // Virada de ano nos dois sentidos: é onde o deslocamento de fuso troca
      // também o ANO, não só o dia.
      expect(dataCompleta('2026-12-31')).toBe('31/12/2026')
      expect(dataCompleta('2026-01-01')).toBe('01/01/2026')
    })

    it('tira o mês da data civil sem passar por instante', () => {
      process.env.TZ = fuso
      expect(mesDaDataCivil('2026-08-31')).toBe('2026-08')
      expect(mesDaDataCivil('2026-01-01')).toBe('2026-01')
      expect(mesDaDataCivil('2026-12-31')).toBe('2026-12')
    })
  },
)

/** A armadilha, escrita como teste.
 *
 *  Se alguém "simplificar" `civil.ts` para `new Date(civil)`, este caso continua
 *  passando (ele não usa o módulo) e os de cima quebram — e a mensagem de falha
 *  aponta para cá, que explica o porquê. */
describe('a armadilha do fuso', () => {
  it('o construtor de Date com string devolve o dia anterior no fuso da casa', () => {
    const ingenuo = new Intl.DateTimeFormat('pt-BR', {
      day: '2-digit',
      month: '2-digit',
      timeZone: 'America/Sao_Paulo',
    }).format(new Date('2026-08-31'))

    expect(ingenuo).toBe('30/08')
    expect(dataCurta('2026-08-31')).toBe('31/08')
  })
})

describe('validação', () => {
  it('recusa data que casa com o formato mas não existe no calendário', () => {
    expect(partesCivis('2026-02-31')).toBeNull()
    expect(partesCivis('2026-13-01')).toBeNull()
    expect(partesCivis('2026-00-10')).toBeNull()
    expect(civilValida('2026-02-31')).toBe(false)
    expect(mesDaDataCivil('2026-02-31')).toBeNull()
  })

  it('aceita 29 de fevereiro em ano bissexto e recusa fora dele', () => {
    expect(civilValida('2028-02-29')).toBe(true)
    expect(civilValida('2026-02-29')).toBe(false)
  })

  it('recusa texto que não tem a forma de data civil', () => {
    expect(partesCivis('')).toBeNull()
    expect(partesCivis('31/08/2026')).toBeNull()
    expect(partesCivis('2026-8-1')).toBeNull()
    expect(partesCivis('<script>')).toBeNull()
  })

  it('devolve o texto original quando não dá para formatar', () => {
    // A tabela não pode explodir por causa de uma data estranha: o pior caso
    // é mostrar o que veio, não derrubar a tela inteira.
    expect(dataCurta('2026-02-31')).toBe('2026-02-31')
    expect(diaPorExtenso('')).toBe('')
  })
})
