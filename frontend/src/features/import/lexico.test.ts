import { describe, expect, it } from 'vitest'
import type { ImportRow, ImportRowStatus } from '@/api/types'
import {
  BLOCO_DO_STATUS,
  DESCRICAO_DO_GRUPO,
  evidenciaDaLinha,
  ORDEM_DOS_STATUS,
  opcoesDeDecisao,
  PALAVRA_DO_STATUS,
  pontuacaoVisivel,
  proveniencia,
  rotuloDaAcao,
  tituloDoGrupo,
} from './lexico'

/** O léxico é onde o status vira palavra. Estes testes fixam as frases da
 *  tabela (g) de docs/DESIGN.md e garantem que **todo** status do contrato tem
 *  bloco, palavra e posição — um status novo no backend precisa aparecer nos
 *  três lugares, e o `tsc` já cobra dois deles; a ordem é o terceiro. */

const NOVE_STATUS: readonly ImportRowStatus[] = [
  'novo',
  'repetido_no_arquivo',
  'duplicado_exato',
  'duplicado_excluido',
  'possivel_duplicado',
  'pagamento_de_fatura',
  'transferencia_interna',
  'transferencia_ja_registrada',
  'rejeitado',
]

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
    description: 'Pix enviado NUBANK',
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

describe('léxico exaustivo', () => {
  it('todo status tem bloco, e os dois de transferência caem no bloco delas', () => {
    for (const status of NOVE_STATUS) {
      expect(BLOCO_DO_STATUS[status]).toBeDefined()
    }
    expect(BLOCO_DO_STATUS.transferencia_interna).toBe('transferencias')
    expect(BLOCO_DO_STATUS.transferencia_ja_registrada).toBe('transferencias')
  })

  it('todo status está na ordem dos grupos, uma vez só', () => {
    expect([...ORDEM_DOS_STATUS].sort()).toEqual([...NOVE_STATUS].sort())
    expect(new Set(ORDEM_DOS_STATUS).size).toBe(ORDEM_DOS_STATUS.length)
  })

  it('a palavra do grupo é a da tabela de copy', () => {
    expect(PALAVRA_DO_STATUS.transferencia_interna).toBe('Parece transferência')
    expect(PALAVRA_DO_STATUS.transferencia_ja_registrada).toBe('Já registrada como transferência')
    expect(tituloDoGrupo('transferencia_interna', 5)).toBe('Parece transferência · 5')
    expect(tituloDoGrupo('transferencia_ja_registrada', 2)).toBe(
      'Já registrada como transferência · 2',
    )
  })

  it('os dois grupos de transferência explicam a consequência', () => {
    expect(DESCRICAO_DO_GRUPO.transferencia_interna).toBe(
      'Não entra sem você confirmar. Se não for transferência, importe como despesa ou receita comum.',
    )
    expect(DESCRICAO_DO_GRUPO.transferencia_ja_registrada).toBe(
      'A outra conta já registrou este par. Vincular não cria lançamento: só marca esta linha como importada, para ela não voltar como nova.',
    )
  })
})

describe('pontuação e proveniência — texto, nunca cor', () => {
  it('100 nunca aparece: ausência de pontuação é correspondência exata', () => {
    expect(pontuacaoVisivel(100)).toBe('')
    expect(pontuacaoVisivel(88)).toBe('88%')
    expect(pontuacaoVisivel(null)).toBe('')
  })

  it('monta "88% · «supermercado»" e só «supermercado» com 100', () => {
    expect(proveniencia({ matchScore: 88, matchedKeyword: 'supermercado' })).toBe(
      '88% · «supermercado»',
    )
    expect(proveniencia({ matchScore: 100, matchedKeyword: 'supermercado' })).toBe('«supermercado»')
    expect(proveniencia({ matchScore: null, matchedKeyword: null })).toBe('')
  })

  it('bordas da pontuação: limiar, fração truncada, nulo com palavra, e a palavra como foi digitada', () => {
    // 80 é o limiar da spec: aparece, porque não é exata.
    expect(pontuacaoVisivel(80)).toBe('80%')
    // Nunca vírgula decimal numa pontuação: inteiro truncado, sem arredondar para cima.
    expect(pontuacaoVisivel(87.9)).toBe('87%')
    // Palavra sem pontuação (contrato tolerante): cita a palavra, sem inventar número.
    expect(proveniencia({ matchScore: null, matchedKeyword: 'padaria' })).toBe('«padaria»')
    // A forma exibível vem do servidor como foi cadastrada: caixa e acento ficam.
    expect(proveniencia({ matchScore: 93, matchedKeyword: 'Pão de Açúcar' })).toBe(
      '93% · «Pão de Açúcar»',
    )
  })
})

describe('evidenciaDaLinha', () => {
  it('parece transferência: conta pelo NOME, pontuação e palavra', () => {
    const interna = linha({
      status: 'transferencia_interna',
      suggestedCounterpartAccountId: 'conta-nubank',
      matchScore: 88,
      matchedKeyword: 'nubank',
    })
    expect(evidenciaDaLinha(interna, 'Nubank')).toBe(
      'Parece transferência para Nubank · 88% · «nubank»',
    )
    expect(evidenciaDaLinha({ ...interna, matchScore: 100 }, 'Nubank')).toBe(
      'Parece transferência para Nubank · «nubank»',
    )
  })

  it('sem o nome no cache fala em "outra conta", nunca no id', () => {
    const interna = linha({
      status: 'transferencia_interna',
      suggestedCounterpartAccountId: 'conta-nubank',
      matchScore: 88,
      matchedKeyword: 'nubank',
    })
    const frase = evidenciaDaLinha(interna)
    expect(frase).toBe('Parece transferência para outra conta · 88% · «nubank»')
    expect(frase).not.toContain('conta-nubank')
  })

  it('já registrada: com a data da perna, e sem ela', () => {
    const registrada = linha({
      status: 'transferencia_ja_registrada',
      suggestedCounterpartAccountId: 'conta-nubank',
      matchTransactionId: 'tx',
      matchOccurredOn: '2026-09-05',
    })
    expect(evidenciaDaLinha(registrada, 'Nubank')).toBe(
      'Já registrada em 05/09 como transferência com Nubank.',
    )
    expect(evidenciaDaLinha({ ...registrada, matchOccurredOn: null }, 'Nubank')).toBe(
      'Já registrada como transferência com Nubank.',
    )
  })

  it('pagamento de fatura com contraparte sugerida diz qual cartão parece', () => {
    const fatura = linha({
      status: 'pagamento_de_fatura',
      defaultAction: 'skip',
      allowedActions: ['skip', 'transfer', 'import'],
      suggestedCounterpartAccountId: 'cartao-nubank',
    })
    expect(evidenciaDaLinha(fatura, 'Cartão Nubank')).toBe(
      'Pagamento da fatura de um cartão · parece o Cartão Nubank',
    )
    // A palavra só é citada quando é da CONTA: neste status ela descreve a
    // categoria sempre que há uma sugerida (emenda §10.4).
    expect(
      evidenciaDaLinha({ ...fatura, matchedKeyword: 'nubank', matchScore: 90 }, 'Cartão Nubank'),
    ).toBe('Pagamento da fatura de um cartão · parece o Cartão Nubank · «nubank»')
    expect(
      evidenciaDaLinha(
        { ...fatura, suggestedCategoryId: 'cat', matchedKeyword: 'fatura', matchScore: 90 },
        'Cartão Nubank',
      ),
    ).toBe('Pagamento da fatura de um cartão · parece o Cartão Nubank')
    expect(evidenciaDaLinha({ ...fatura, suggestedCounterpartAccountId: null })).toBe(
      'Pagamento da fatura de um cartão.',
    )
  })
})

describe('rotuloDaAcao e opcoesDeDecisao', () => {
  const interna = linha({
    status: 'transferencia_interna',
    defaultAction: 'skip',
    allowedActions: ['skip', 'transfer', 'import'],
    suggestedCounterpartAccountId: 'conta-nubank',
    matchScore: 88,
    matchedKeyword: 'nubank',
  })

  it('a contraparte sugerida mora no TEXTO da opção, e "outra conta…" é a mesma ação', () => {
    expect(opcoesDeDecisao(interna, 'Nubank')).toEqual([
      { value: 'skip', label: 'Não importar' },
      { value: 'transfer', label: 'Registrar como transferência para Nubank' },
      { value: 'transfer:outra', label: 'Registrar como transferência para outra conta…' },
      { value: 'import', label: 'Importar como despesa comum (não é transferência)' },
    ])
  })

  it('import numa transferência interna concorda com o kind', () => {
    expect(rotuloDaAcao({ ...interna, kind: 'income' }, 'import')).toBe(
      'Importar como receita comum (não é transferência)',
    )
  })

  it('já registrada: vincular primeiro (default), depois não importar', () => {
    const registrada = linha({
      status: 'transferencia_ja_registrada',
      defaultAction: 'link',
      allowedActions: ['link', 'skip'],
      suggestedCounterpartAccountId: 'conta-nubank',
    })
    expect(opcoesDeDecisao(registrada, 'Nubank')).toEqual([
      { value: 'link', label: 'Vincular à transferência já registrada' },
      { value: 'skip', label: 'Não importar' },
    ])
  })

  it('fatura com cartão sugerido: "para Cartão Nubank" e "para outro cartão…"', () => {
    const fatura = linha({
      status: 'pagamento_de_fatura',
      defaultAction: 'skip',
      allowedActions: ['skip', 'transfer', 'import'],
      suggestedCounterpartAccountId: 'cartao-nubank',
    })
    expect(opcoesDeDecisao(fatura, 'Cartão Nubank').map((o) => o.label)).toEqual([
      'Não importar',
      'Registrar como transferência para Cartão Nubank',
      'Registrar como transferência para outro cartão…',
      'Importar como despesa mesmo assim',
    ])
  })

  it('fatura SEM sugestão continua como antes: uma opção com reticências', () => {
    const fatura = linha({
      status: 'pagamento_de_fatura',
      defaultAction: 'skip',
      allowedActions: ['skip', 'transfer', 'import'],
    })
    expect(opcoesDeDecisao(fatura, undefined).map((o) => o.label)).toEqual([
      'Não importar',
      'Registrar como transferência para…',
      'Importar como despesa mesmo assim',
    ])
  })

  it('com sugestão mas sem o nome no cache, a opção não vira "para…"', () => {
    // "para…" significa "falta escolher" — e não falta.
    expect(opcoesDeDecisao(interna, undefined)[1]?.label).toBe(
      'Registrar como transferência para a conta sugerida',
    )
  })
})
