import { describe, expect, it } from 'vitest'
import type { ImportRow } from '@/api/types'
import {
  categoriaEfetiva,
  contarRevisao,
  type Escolhas,
  montarDecisoes,
  nadaMarcado,
  nuanceDaConfirmacao,
  rotuloDoConfirmar,
  transferenciasIncompletas,
} from './decisoes'

/** Este arquivo cobre a função que decide **o que é gravado no banco**.
 *
 *  `montarDecisoes` é o último filtro antes de a importação virar dinheiro
 *  registrado: um erro aqui ou cria lançamento repetido, ou descarta em
 *  silêncio a linha que a pessoa mandou entrar, ou produz um 400 que derruba o
 *  lote inteiro (o confirm é tudo ou nada). */

const NUBANK = 'conta-nubank'
const ALIMENTACAO = 'cat-alimentacao'

function linha(over: Partial<ImportRow> = {}): ImportRow {
  return {
    id: 'row-1',
    seq: 1,
    lineNo: 2,
    status: 'novo',
    defaultAction: 'import',
    allowedActions: ['import', 'skip'],
    kind: 'expense',
    occurredOn: '2026-08-12',
    amountCents: 1100,
    description: 'PADARIA EXEMPLO LTDA',
    externalId: null,
    rejectReason: null,
    matchTransactionId: null,
    suggestedCategoryId: null,
    matchScore: null,
    matchedKeyword: null,
    suggestedCounterpartAccountId: null,
    matchOccurredOn: null,
    ...over,
  }
}

/** Uma linha nova COM categoria sugerida pelas palavras-chave. */
function sugerida(over: Partial<ImportRow> = {}): ImportRow {
  return linha({
    id: 'sug',
    suggestedCategoryId: ALIMENTACAO,
    matchScore: 88,
    matchedKeyword: 'padaria',
    ...over,
  })
}

/** Uma linha que parece transferência para a Nubank. */
function interna(over: Partial<ImportRow> = {}): ImportRow {
  return linha({
    id: 'int',
    status: 'transferencia_interna',
    defaultAction: 'skip',
    allowedActions: ['skip', 'transfer', 'import'],
    suggestedCounterpartAccountId: NUBANK,
    matchScore: 88,
    matchedKeyword: 'nubank',
    ...over,
  })
}

/** A outra perna já existe: o default é vincular. */
function jaRegistrada(over: Partial<ImportRow> = {}): ImportRow {
  return linha({
    id: 'reg',
    status: 'transferencia_ja_registrada',
    defaultAction: 'link',
    allowedActions: ['link', 'skip'],
    suggestedCounterpartAccountId: NUBANK,
    matchTransactionId: 'tx-perna',
    matchOccurredOn: '2026-08-10',
    ...over,
  })
}

const SEM_ESCOLHA: Escolhas = {}

describe('montarDecisoes', () => {
  it('não manda nada quando todos os defaults são aceitos', () => {
    // O contrato pede APENAS as exceções: com 10.000 rowId o corpo estouraria
    // o limite de 1 MiB da requisição.
    const linhas = [linha({ id: 'a' }), linha({ id: 'b' }), linha({ id: 'c' })]
    expect(montarDecisoes(linhas, SEM_ESCOLHA)).toEqual([])
  })

  it('manda a exceção quando a ação difere do default', () => {
    const linhas = [linha({ id: 'a' }), linha({ id: 'b' })]
    const escolhas: Escolhas = { b: { acao: 'skip' } }

    expect(montarDecisoes(linhas, escolhas)).toEqual([{ rowId: 'b', action: 'skip' }])
  })

  it('manda a linha quando ela ganha categoria, mesmo mantendo o default', () => {
    // Categoria é informação nova: sem mandá-la, a linha entraria sem categoria
    // e a escolha da pessoa se perderia em silêncio.
    const linhas = [linha({ id: 'a' })]
    const escolhas: Escolhas = { a: { acao: 'import', categoriaId: 'cat-1' } }

    expect(montarDecisoes(linhas, escolhas)).toEqual([
      { rowId: 'a', action: 'import', categoryId: 'cat-1' },
    ])
  })

  it('não deixa categoria viajar junto com skip', () => {
    // O contrato aceita categoryId APENAS com action "import"; mandar os dois
    // juntos é 400, e 400 aqui derruba o lote todo.
    const linhas = [linha({ id: 'a' })]
    const escolhas: Escolhas = { a: { acao: 'skip', categoriaId: 'cat-1' } }

    expect(montarDecisoes(linhas, escolhas)).toEqual([{ rowId: 'a', action: 'skip' }])
  })

  it('manda a conta da outra perna junto com a transferência', () => {
    const linhas = [
      linha({
        id: 'fatura',
        status: 'pagamento_de_fatura',
        defaultAction: 'skip',
        allowedActions: ['skip', 'transfer', 'import'],
      }),
    ]
    const escolhas: Escolhas = { fatura: { acao: 'transfer', contraparteId: 'cartao-1' } }

    expect(montarDecisoes(linhas, escolhas)).toEqual([
      { rowId: 'fatura', action: 'transfer', counterpartAccountId: 'cartao-1' },
    ])
  })

  it('nunca cita uma linha do bloco "ficam de fora", nem com escolha na mão', () => {
    // duplicado_exato e rejeitado não têm ação permitida: citá-las no confirm é
    // 400. A tela não oferece controle para elas, mas o corpo é montado a
    // partir do estado — e estado pode ficar sujo.
    const linhas = [
      linha({ id: 'dup', status: 'duplicado_exato', defaultAction: 'skip', allowedActions: [] }),
      linha({
        id: 'ruim',
        status: 'rejeitado',
        defaultAction: 'skip',
        allowedActions: [],
        kind: null,
        occurredOn: null,
        amountCents: null,
        description: null,
        rejectReason: 'invalid_date',
      }),
    ]
    const escolhas: Escolhas = { dup: { acao: 'import' }, ruim: { acao: 'import' } }

    expect(montarDecisoes(linhas, escolhas)).toEqual([])
  })

  it('descarta ação que o servidor não ofereceu para aquela linha', () => {
    const linhas = [linha({ id: 'a', allowedActions: ['import', 'skip'] })]
    const escolhas: Escolhas = { a: { acao: 'transfer', contraparteId: 'x' } }

    expect(montarDecisoes(linhas, escolhas)).toEqual([])
  })

  it('usa o defaultAction do SERVIDOR, não uma tabela nossa', () => {
    // A mesma linha "possivel_duplicado", com default trocado pelo servidor,
    // produz exceções opostas. Uma cópia da tabela de status no cliente
    // divergiria daqui sem ninguém perceber.
    const barrada = linha({
      id: 'a',
      status: 'possivel_duplicado',
      defaultAction: 'skip',
      allowedActions: ['skip', 'import'],
    })
    expect(montarDecisoes([barrada], { a: { acao: 'import' } })).toEqual([
      { rowId: 'a', action: 'import' },
    ])
    expect(montarDecisoes([barrada], SEM_ESCOLHA)).toEqual([])

    const liberada = { ...barrada, defaultAction: 'import' as const }
    expect(montarDecisoes([liberada], SEM_ESCOLHA)).toEqual([])
    expect(montarDecisoes([liberada], { a: { acao: 'skip' } })).toEqual([
      { rowId: 'a', action: 'skip' },
    ])
  })

  describe('categoria tri-estado (spec 0005 §4.2.3)', () => {
    it('omite categoryId quando a pessoa aceitou a sugestão sem mexer', () => {
      // Ausente = o servidor usa `suggestedCategoryId`. Mandar o mesmo id de
      // volta seria uma exceção que não muda nada.
      expect(montarDecisoes([sugerida()], SEM_ESCOLHA)).toEqual([])
    })

    it('omite categoryId quando a pessoa escolheu a PRÓPRIA sugerida', () => {
      // Trocou e voltou: o select mostra a sugerida de novo, e o resultado é
      // idêntico ao de não ter mexido.
      const escolhas: Escolhas = { sug: { acao: 'import', categoriaId: ALIMENTACAO } }
      expect(montarDecisoes([sugerida()], escolhas)).toEqual([])
    })

    it('manda categoryId: null quando a pessoa LIMPOU a sugestão', () => {
      // É a única forma de entrar sem categoria apesar da sugestão: sem o
      // `null` explícito, o servidor gravaria a sugerida.
      const escolhas: Escolhas = { sug: { acao: 'import', categoriaId: null } }
      expect(montarDecisoes([sugerida()], escolhas)).toEqual([
        { rowId: 'sug', action: 'import', categoryId: null },
      ])
    })

    it('manda o valor quando a pessoa trocou a sugestão por outra categoria', () => {
      const escolhas: Escolhas = { sug: { acao: 'import', categoriaId: 'cat-padaria' } }
      expect(montarDecisoes([sugerida()], escolhas)).toEqual([
        { rowId: 'sug', action: 'import', categoryId: 'cat-padaria' },
      ])
    })

    it('não manda null numa linha SEM sugestão — não há o que limpar', () => {
      const escolhas: Escolhas = { a: { acao: 'import', categoriaId: null } }
      expect(montarDecisoes([linha({ id: 'a' })], escolhas)).toEqual([])
    })

    it('mantém o null ao limpar uma sugestão numa linha que também mudou de ação', () => {
      const barrada = sugerida({
        id: 'pd',
        status: 'possivel_duplicado',
        defaultAction: 'skip',
        allowedActions: ['skip', 'import'],
      })
      const escolhas: Escolhas = { pd: { acao: 'import', categoriaId: null } }
      expect(montarDecisoes([barrada], escolhas)).toEqual([
        { rowId: 'pd', action: 'import', categoryId: null },
      ])
    })
  })

  describe('transferência interna (spec 0005 §4.2.2)', () => {
    it('transfer com a contraparte sugerida vai SEM counterpartAccountId', () => {
      // O servidor usa `suggestedCounterpartAccountId`. Repeti-lo no corpo
      // seria a tela afirmando algo que ela só copiou.
      const escolhas: Escolhas = { int: { acao: 'transfer' } }
      expect(montarDecisoes([interna()], escolhas)).toEqual([{ rowId: 'int', action: 'transfer' }])
    })

    it('transfer para "outra conta" manda a conta escolhida', () => {
      const escolhas: Escolhas = {
        int: { acao: 'transfer', outraConta: true, contraparteId: 'conta-c6' },
      }
      expect(montarDecisoes([interna()], escolhas)).toEqual([
        { rowId: 'int', action: 'transfer', counterpartAccountId: 'conta-c6' },
      ])
    })

    it('import numa transferência interna leva a categoria escolhida', () => {
      const escolhas: Escolhas = { int: { acao: 'import', categoriaId: ALIMENTACAO } }
      expect(montarDecisoes([interna()], escolhas)).toEqual([
        { rowId: 'int', action: 'import', categoryId: ALIMENTACAO },
      ])
    })

    it('link é o default da já registrada: aceitar não manda nada', () => {
      expect(montarDecisoes([jaRegistrada()], SEM_ESCOLHA)).toEqual([])
    })

    it('recusar o vínculo vira a exceção skip', () => {
      expect(montarDecisoes([jaRegistrada()], { reg: { acao: 'skip' } })).toEqual([
        { rowId: 'reg', action: 'skip' },
      ])
    })

    it('link nunca leva categoria nem contraparte, mesmo com estado sujo', () => {
      // O contrato recusa os dois com `link`; 400 aqui derruba o lote.
      const escolhas: Escolhas = { reg: { acao: 'link', categoriaId: 'x', contraparteId: 'y' } }
      expect(montarDecisoes([jaRegistrada({ defaultAction: 'skip' })], escolhas)).toEqual([
        { rowId: 'reg', action: 'link' },
      ])
    })
  })
})

describe('categoriaEfetiva', () => {
  it('é a sugerida quando ninguém mexeu, e null quando não há sugestão', () => {
    expect(categoriaEfetiva(sugerida(), SEM_ESCOLHA)).toBe(ALIMENTACAO)
    expect(categoriaEfetiva(linha(), SEM_ESCOLHA)).toBeNull()
  })

  it('a escolha da pessoa vence a sugestão — inclusive "sem categoria"', () => {
    expect(categoriaEfetiva(sugerida(), { sug: { acao: 'import', categoriaId: 'outra' } })).toBe(
      'outra',
    )
    expect(categoriaEfetiva(sugerida(), { sug: { acao: 'import', categoriaId: null } })).toBeNull()
  })
})

describe('transferenciasIncompletas', () => {
  it('acusa a transferência sem conta de destino quando não há sugestão', () => {
    const linhas = [
      linha({ id: 'a', status: 'pagamento_de_fatura', allowedActions: ['skip', 'transfer'] }),
      linha({ id: 'b', status: 'pagamento_de_fatura', allowedActions: ['skip', 'transfer'] }),
    ]
    const escolhas: Escolhas = {
      a: { acao: 'transfer' },
      b: { acao: 'transfer', contraparteId: 'cartao-1' },
    }

    expect(transferenciasIncompletas(linhas, escolhas).map((l) => l.id)).toEqual(['a'])
  })

  it('não cobra conta quando a linha traz contraparte sugerida e a pessoa a aceitou', () => {
    expect(transferenciasIncompletas([interna()], { int: { acao: 'transfer' } })).toEqual([])
  })

  it('cobra conta quando a pessoa recusou a sugerida ("outra conta…") e não escolheu', () => {
    const escolhas: Escolhas = { int: { acao: 'transfer', outraConta: true } }
    expect(transferenciasIncompletas([interna()], escolhas).map((l) => l.id)).toEqual(['int'])

    const completa: Escolhas = { int: { acao: 'transfer', outraConta: true, contraparteId: 'c6' } }
    expect(transferenciasIncompletas([interna()], completa)).toEqual([])
  })
})

describe('contarRevisao', () => {
  const linhas = [
    linha({ id: 'nova-1' }),
    linha({ id: 'nova-2' }),
    linha({ id: 'repetida', status: 'repetido_no_arquivo' }),
    linha({
      id: 'excluida',
      status: 'duplicado_excluido',
      defaultAction: 'skip',
      allowedActions: ['skip', 'import'],
    }),
    linha({
      id: 'fatura',
      status: 'pagamento_de_fatura',
      defaultAction: 'skip',
      allowedActions: ['skip', 'transfer', 'import'],
    }),
    linha({ id: 'dup', status: 'duplicado_exato', defaultAction: 'skip', allowedActions: [] }),
  ]

  it('conta os defaults do servidor quando ninguém mexeu', () => {
    expect(contarRevisao(linhas, SEM_ESCOLHA)).toEqual({
      vaoEntrar: 3,
      ignorados: 2,
      restaurados: 0,
      transferencias: 0,
      vinculadas: 0,
      bloqueadas: 1,
    })
  })

  it('separa restauração e transferência de uma importação comum', () => {
    const escolhas: Escolhas = {
      excluida: { acao: 'import' },
      fatura: { acao: 'transfer', contraparteId: 'cartao-1' },
      'nova-2': { acao: 'skip' },
    }

    expect(contarRevisao(linhas, escolhas)).toEqual({
      vaoEntrar: 4,
      ignorados: 1,
      restaurados: 1,
      transferencias: 1,
      vinculadas: 0,
      bloqueadas: 1,
    })
  })

  it('não conta linha bloqueada como ignorada pela pessoa', () => {
    // São coisas diferentes no resultado: "você ignorou" é escolha, "bloqueada"
    // é impedimento. Misturar as duas faria a tela culpar a pessoa por algo que
    // ela não decidiu.
    const contagem = contarRevisao(linhas, SEM_ESCOLHA)
    expect(contagem.bloqueadas).toBe(1)
    expect(contagem.ignorados).toBe(2)
  })

  it('link vira "vinculadas": não entra em vaoEntrar nem em ignorados', () => {
    // Vincular não cria lançamento — contá-lo como "importado" prometeria um
    // lançamento que não vai existir; como "ignorado", esconderia o que a
    // pessoa aceitou.
    const contagem = contarRevisao([jaRegistrada(), interna()], SEM_ESCOLHA)
    expect(contagem).toEqual({
      vaoEntrar: 0,
      ignorados: 1,
      restaurados: 0,
      transferencias: 0,
      vinculadas: 1,
      bloqueadas: 0,
    })
    expect(nadaMarcado(contagem)).toBe(false)
  })

  it('aceitar a transferência interna conta como par a criar', () => {
    const contagem = contarRevisao([interna()], { int: { acao: 'transfer' } })
    expect(contagem.vaoEntrar).toBe(1)
    expect(contagem.transferencias).toBe(1)
  })
})

describe('rotuloDoConfirmar', () => {
  const base = {
    vaoEntrar: 0,
    ignorados: 0,
    restaurados: 0,
    transferencias: 0,
    vinculadas: 0,
    bloqueadas: 0,
  }

  it('diz o que vai acontecer', () => {
    expect(rotuloDoConfirmar({ ...base, vaoEntrar: 42, ignorados: 6 })).toBe(
      'Importar 42 lançamentos · 6 ignorados',
    )
  })

  it('concorda no singular', () => {
    expect(rotuloDoConfirmar({ ...base, vaoEntrar: 1, ignorados: 1 })).toBe(
      'Importar 1 lançamento · 1 ignorado',
    )
  })

  it('omite os ignorados quando não há nenhum', () => {
    expect(rotuloDoConfirmar({ ...base, vaoEntrar: 59 })).toBe('Importar 59 lançamentos')
  })

  it('muda de rótulo com nada marcado, em vez de o botão apagar', () => {
    expect(rotuloDoConfirmar({ ...base, ignorados: 3 })).toBe('Nada marcado para importar')
  })

  it('cita os vínculos entre os importados e os ignorados', () => {
    expect(rotuloDoConfirmar({ ...base, vaoEntrar: 42, vinculadas: 2, ignorados: 6 })).toBe(
      'Importar 42 lançamentos · 2 vinculados · 6 ignorados',
    )
    expect(rotuloDoConfirmar({ ...base, vaoEntrar: 1, vinculadas: 1 })).toBe(
      'Importar 1 lançamento · 1 vinculado',
    )
  })

  it('só vínculos é "Vincular", não "Importar 0"', () => {
    expect(rotuloDoConfirmar({ ...base, vinculadas: 2, ignorados: 1 })).toBe(
      'Vincular 2 lançamentos',
    )
    expect(rotuloDoConfirmar({ ...base, vinculadas: 1 })).toBe('Vincular 1 lançamento')
  })
})

describe('nuanceDaConfirmacao', () => {
  const base = {
    vaoEntrar: 10,
    ignorados: 0,
    restaurados: 0,
    transferencias: 0,
    vinculadas: 0,
    bloqueadas: 0,
  }

  it('cala quando não há nuance', () => {
    expect(nuanceDaConfirmacao(base)).toBe('')
  })

  it('avisa restauração e transferência juntas', () => {
    expect(nuanceDaConfirmacao({ ...base, restaurados: 1, transferencias: 1 })).toBe(
      'Inclui 1 lançamento restaurado e 1 transferência.',
    )
  })

  it('avisa só o que existe', () => {
    expect(nuanceDaConfirmacao({ ...base, transferencias: 2 })).toBe('Inclui 2 transferências.')
    expect(nuanceDaConfirmacao({ ...base, restaurados: 3 })).toBe(
      'Inclui 3 lançamentos restaurados.',
    )
  })

  it('avisa os vínculos a transferências já registradas', () => {
    expect(nuanceDaConfirmacao({ ...base, transferencias: 5, vinculadas: 2 })).toBe(
      'Inclui 5 transferências e 2 vínculos a transferências já registradas.',
    )
    expect(nuanceDaConfirmacao({ ...base, vinculadas: 1 })).toBe(
      'Inclui 1 vínculo a transferência já registrada.',
    )
    expect(nuanceDaConfirmacao({ ...base, restaurados: 1, transferencias: 1, vinculadas: 1 })).toBe(
      'Inclui 1 lançamento restaurado, 1 transferência e 1 vínculo a transferência já registrada.',
    )
  })
})
