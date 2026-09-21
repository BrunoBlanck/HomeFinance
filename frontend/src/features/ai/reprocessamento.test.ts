import { describe, expect, it, vi } from 'vitest'
import { ApiError, EchoMismatchError, NetworkError } from '@/api/client'
import type { AutoCategorizeResult, TransferDetectResult } from '@/api/types'
import {
  type Chamadas,
  conferirTudo,
  consolidarPrevia,
  estadosDosPassos,
  executarPlano,
  fraseDaPrevia,
  fraseDoPasso,
  fraseDoQueFicou,
  fraseDoResultado,
  lerErro,
  MSG_MES_GRANDE,
  MSG_MUITAS_OPERACOES,
  nadaAReprocessar,
  PALAVRA_DO_ESTADO,
  type PreviaDoMes,
  planoDeExecucao,
  tipoDaFalha,
} from './reprocessamento'

const MESES = ['2026-07', '2026-08', '2026-09'] as const

function deteccao(
  month: string,
  parcial: Partial<TransferDetectResult> = {},
): TransferDetectResult {
  return { month, paired: 0, unpaired: 0, items: [], unpairedItems: [], ...parcial }
}

function categorizacao(
  month: string,
  parcial: Partial<AutoCategorizeResult> = {},
): AutoCategorizeResult {
  return { month, categorized: 0, unmatched: 0, items: [], unmatchedItems: [], ...parcial }
}

/** As prévias FIXAS dos três meses: 1 + 2 + 0 pares, 1 + 0 + 1 sem par,
 *  12 + 18 + 12 categorizados, 4 + 5 + 3 sem categoria. */
function previasFixas(): PreviaDoMes[] {
  return [
    {
      mes: '2026-07',
      transferencias: deteccao('2026-07', { paired: 1, unpaired: 1 }),
      categorizacao: categorizacao('2026-07', { categorized: 12, unmatched: 4 }),
    },
    {
      mes: '2026-08',
      transferencias: deteccao('2026-08', { paired: 2, unpaired: 0 }),
      categorizacao: categorizacao('2026-08', { categorized: 18, unmatched: 5 }),
    },
    {
      mes: '2026-09',
      transferencias: deteccao('2026-09', { paired: 0, unpaired: 1 }),
      categorizacao: categorizacao('2026-09', { categorized: 12, unmatched: 3 }),
    },
  ]
}

/** Chamadas espiãs que devolvem respostas fixas e registram a ORDEM em que
 *  foram feitas — é a ordem, e não só o conjunto, que os aceites 38 e 44
 *  afirmam. */
function chamadasEspias(
  respostas: {
    detectar?: (mes: string, dryRun: boolean) => TransferDetectResult | Error
    categorizar?: (mes: string, dryRun: boolean) => AutoCategorizeResult | Error
  } = {},
) {
  const ordem: string[] = []
  const chamadas: Chamadas = {
    detectar: vi.fn(async (mes, dryRun) => {
      ordem.push(`detect:${mes}:${dryRun ? 'previa' : 'real'}`)
      const resposta = respostas.detectar?.(mes, dryRun) ?? deteccao(mes, { paired: 1 })
      if (resposta instanceof Error) throw resposta
      return resposta
    }),
    categorizar: vi.fn(async (mes, dryRun) => {
      ordem.push(`categorize:${mes}:${dryRun ? 'previa' : 'real'}`)
      const resposta =
        respostas.categorizar?.(mes, dryRun) ?? categorizacao(mes, { categorized: 10 })
      if (resposta instanceof Error) throw resposta
      return resposta
    }),
  }
  return { ordem, chamadas }
}

// ------------------------------------------------------------ prévia

describe('consolidarPrevia — aceite 37', () => {
  it('soma número a número as prévias individuais de cada mês', () => {
    const consolidada = consolidarPrevia(previasFixas())
    expect(consolidada.pares).toBe(1 + 2 + 0)
    expect(consolidada.semPar).toBe(1 + 0 + 1)
    expect(consolidada.categorizados).toBe(12 + 18 + 12)
    expect(consolidada.semCategoria).toBe(4 + 5 + 3)
    expect(consolidada.meses).toHaveLength(3)
  })

  it('a frase consolidada diz os três números somados', () => {
    expect(fraseDaPrevia(consolidarPrevia(previasFixas()))).toBe(
      '3 pares de transferência · 42 lançamentos categorizados · 12 seguem sem categoria.',
    )
  })

  it('singular em cada parte', () => {
    expect(
      fraseDaPrevia(
        consolidarPrevia([
          {
            mes: '2026-09',
            transferencias: deteccao('2026-09', { paired: 1 }),
            categorizacao: categorizacao('2026-09', { categorized: 1, unmatched: 1 }),
          },
        ]),
      ),
    ).toBe('1 par de transferência · 1 lançamento categorizado · 1 segue sem categoria.')
  })

  it('zero em tudo: nada a reprocessar, e a frase é honesta sobre os sem categoria', () => {
    const vazia = consolidarPrevia([
      {
        mes: '2026-09',
        transferencias: deteccao('2026-09'),
        categorizacao: categorizacao('2026-09'),
      },
    ])
    expect(nadaAReprocessar(vazia)).toBe(true)
    expect(fraseDaPrevia(vazia)).toBe(
      'Nada a reprocessar — não há par para reconhecer nem lançamento sem categoria.',
    )

    const comSemCategoria = consolidarPrevia([
      {
        mes: '2026-09',
        transferencias: deteccao('2026-09'),
        categorizacao: categorizacao('2026-09', { unmatched: 12 }),
      },
    ])
    expect(nadaAReprocessar(comSemCategoria)).toBe(true)
    expect(fraseDaPrevia(comSemCategoria)).toBe(
      'Nada a reprocessar — não há par para reconhecer, e nenhuma palavra-chave bate com os 12 lançamentos sem categoria.',
    )
  })

  it('só sem par não é "nada a reprocessar" quando há categoria a gravar', () => {
    const previa = consolidarPrevia([
      {
        mes: '2026-09',
        transferencias: deteccao('2026-09', { unpaired: 3 }),
        categorizacao: categorizacao('2026-09', { categorized: 2 }),
      },
    ])
    expect(nadaAReprocessar(previa)).toBe(false)
  })
})

describe('conferirTudo', () => {
  it('roda as duas prévias em todos os meses com dryRun: true — seis chamadas', async () => {
    const { ordem, chamadas } = chamadasEspias()
    const previa = await conferirTudo(MESES, chamadas)

    expect(ordem).toHaveLength(6)
    expect(ordem.filter((c) => c.endsWith(':previa'))).toHaveLength(6)
    for (const mes of MESES) {
      expect(ordem).toContain(`detect:${mes}:previa`)
      expect(ordem).toContain(`categorize:${mes}:previa`)
    }
    expect(previa.pares).toBe(3)
    expect(previa.categorizados).toBe(30)
  })

  it('uma falha derruba a prévia inteira — nunca um consolidado de cinco sextos', async () => {
    const { chamadas } = chamadasEspias({
      categorizar: (mes) => (mes === '2026-08' ? new ApiError(429, null) : categorizacao(mes)),
    })
    await expect(conferirTudo(MESES, chamadas)).rejects.toBeInstanceOf(ApiError)
  })
})

// ------------------------------------------------------- plano e execução

describe('planoDeExecucao — duas fases, cada uma mês a mês', () => {
  it('detecção em TODOS os meses antes de qualquer categorização', () => {
    expect(planoDeExecucao(MESES)).toEqual([
      { mes: '2026-07', etapa: 'transferencias' },
      { mes: '2026-08', etapa: 'transferencias' },
      { mes: '2026-09', etapa: 'transferencias' },
      { mes: '2026-07', etapa: 'categorizacao' },
      { mes: '2026-08', etapa: 'categorizacao' },
      { mes: '2026-09', etapa: 'categorizacao' },
    ])
  })

  it('um mês: dois passos', () => {
    expect(planoDeExecucao(['2026-09'])).toEqual([
      { mes: '2026-09', etapa: 'transferencias' },
      { mes: '2026-09', etapa: 'categorizacao' },
    ])
  })
})

describe('executarPlano — aceite 38', () => {
  it('chama as rotas na ordem transferências (todos os meses) → categorização (todos os meses), com dryRun: false', async () => {
    const { ordem, chamadas } = chamadasEspias()
    const concluidos: string[] = []
    const desfecho = await executarPlano(planoDeExecucao(MESES), chamadas, (c) =>
      concluidos.push(`${c.indice}:${c.passo.etapa}:${c.passo.mes}:${c.quantidade}`),
    )

    expect(ordem).toEqual([
      'detect:2026-07:real',
      'detect:2026-08:real',
      'detect:2026-09:real',
      'categorize:2026-07:real',
      'categorize:2026-08:real',
      'categorize:2026-09:real',
    ])
    expect(concluidos).toEqual([
      '0:transferencias:2026-07:1',
      '1:transferencias:2026-08:1',
      '2:transferencias:2026-09:1',
      '3:categorizacao:2026-07:10',
      '4:categorizacao:2026-08:10',
      '5:categorizacao:2026-09:10',
    ])
    expect(desfecho).toEqual({ ok: true, total: { pares: 3, categorizados: 30 } })
  })

  /** A sequência é ESTRITA: a chamada seguinte só sai quando a anterior
   *  respondeu. Uma execução em paralelo teria as seis em voo ao mesmo tempo
   *  — aqui, no instante em que a primeira responde, só ela foi feita. */
  it('é estritamente sequencial — a segunda chamada só sai depois da primeira responder', async () => {
    const { ordem, chamadas } = chamadasEspias()
    let liberar: (() => void) | null = null
    const original = chamadas.detectar
    chamadas.detectar = vi.fn(async (mes, dryRun) => {
      if (mes === '2026-07') {
        await new Promise<void>((resolve) => {
          liberar = resolve
        })
      }
      return original(mes, dryRun)
    })

    const execucao = executarPlano(planoDeExecucao(MESES), chamadas, () => {})
    await Promise.resolve()
    expect(ordem).toEqual([])
    expect(chamadas.detectar).toHaveBeenCalledTimes(1)
    expect(chamadas.categorizar).not.toHaveBeenCalled()
    ;(liberar as (() => void) | null)?.()
    await execucao
    expect(ordem).toHaveLength(6)
  })
})

describe('executarPlano — aceite 44: para no primeiro erro', () => {
  it('409 no segundo mês da fase 2: as cinco anteriores foram feitas e NENHUMA chamada sai depois', async () => {
    const conflito = new ApiError(409, 'CONFLICT')
    const { ordem, chamadas } = chamadasEspias({
      categorizar: (mes, dryRun) =>
        !dryRun && mes === '2026-08' ? conflito : categorizacao(mes, { categorized: 10 }),
    })
    const desfecho = await executarPlano(planoDeExecucao(MESES), chamadas, () => {})

    expect(ordem).toEqual([
      'detect:2026-07:real',
      'detect:2026-08:real',
      'detect:2026-09:real',
      'categorize:2026-07:real',
      'categorize:2026-08:real',
    ])
    expect(chamadas.categorizar).toHaveBeenCalledTimes(2)
    expect(desfecho).toEqual({
      ok: false,
      indiceDaFalha: 4,
      erro: conflito,
      total: { pares: 3, categorizados: 10 },
    })
  })

  it('falha na fase 1 impede a fase 2 inteira', async () => {
    const { ordem, chamadas } = chamadasEspias({
      detectar: (mes, dryRun) =>
        !dryRun && mes === '2026-08' ? new NetworkError() : deteccao(mes),
    })
    const desfecho = await executarPlano(planoDeExecucao(MESES), chamadas, () => {})
    expect(ordem).toEqual(['detect:2026-07:real', 'detect:2026-08:real'])
    expect(desfecho.ok).toBe(false)
    if (!desfecho.ok) expect(desfecho.indiceDaFalha).toBe(1)
  })
})

// ------------------------------------------------------ estados por palavra

describe('estadosDosPassos', () => {
  it('em execução: feitos, um em andamento e o resto na fila', () => {
    expect(estadosDosPassos(6, 2, null)).toEqual([
      'feito',
      'feito',
      'em_andamento',
      'na_fila',
      'na_fila',
      'na_fila',
    ])
  })

  it('parado no índice 4: os feitos ficam, o que falhou leva a palavra da falha, o resto não chegou a rodar', () => {
    expect(estadosDosPassos(6, 4, { indice: 4, tipo: 'nao_aplicado' })).toEqual([
      'feito',
      'feito',
      'feito',
      'feito',
      'nao_aplicado',
      'nao_chegou_a_rodar',
    ])
    expect(estadosDosPassos(2, 0, { indice: 0, tipo: 'sem_resposta' })).toEqual([
      'sem_resposta',
      'nao_chegou_a_rodar',
    ])
  })

  it('toda palavra tem texto — nunca o código cru na tela', () => {
    for (const palavra of Object.values(PALAVRA_DO_ESTADO)) {
      expect(palavra).not.toMatch(/_/)
    }
    expect(PALAVRA_DO_ESTADO.nao_chegou_a_rodar).toBe('não chegou a rodar')
  })

  it('o tipo da falha: resposta de erro é "não aplicado"; sem resposta é dúvida', () => {
    expect(tipoDaFalha(new ApiError(409, 'CONFLICT'))).toBe('nao_aplicado')
    expect(tipoDaFalha(new ApiError(500, null))).toBe('nao_aplicado')
    expect(tipoDaFalha(new NetworkError())).toBe('sem_resposta')
    expect(tipoDaFalha(new EchoMismatchError())).toBe('sem_resposta')
  })
})

// ----------------------------------------------------------------- copy

describe('fraseDoQueFicou', () => {
  it('a frase da direção: 409 na categorização de agosto', () => {
    expect(fraseDoQueFicou(MESES, { indice: 4, tipo: 'nao_aplicado' })).toBe(
      'As transferências de julho, agosto e setembro foram aplicadas e continuam aplicadas. A categorização de julho também. A de agosto não foi, e a de setembro não chegou a rodar. Confira de novo antes de seguir.',
    )
  })

  it('falha na primeira categorização: nada da fase 2 "também"', () => {
    expect(fraseDoQueFicou(MESES, { indice: 3, tipo: 'nao_aplicado' })).toBe(
      'As transferências de julho, agosto e setembro foram aplicadas e continuam aplicadas. A categorização de julho não foi aplicada, e as de agosto e setembro não chegaram a rodar. Confira de novo antes de seguir.',
    )
  })

  it('falha na última categorização: ninguém ficou por rodar', () => {
    expect(fraseDoQueFicou(MESES, { indice: 5, tipo: 'nao_aplicado' })).toBe(
      'As transferências de julho, agosto e setembro foram aplicadas e continuam aplicadas. A categorização de julho e agosto também. A de setembro não foi. Confira de novo antes de seguir.',
    )
  })

  it('falha no meio da fase 1: a categorização inteira não chegou a rodar', () => {
    expect(fraseDoQueFicou(MESES, { indice: 1, tipo: 'nao_aplicado' })).toBe(
      'As transferências de julho foram aplicadas e continuam aplicadas. As de agosto não foram aplicadas, e as de setembro não chegaram a rodar. A categorização não chegou a rodar. Confira de novo antes de seguir.',
    )
    expect(fraseDoQueFicou(MESES, { indice: 0, tipo: 'nao_aplicado' })).toBe(
      'As transferências de julho não foram aplicadas, e as de agosto e setembro não chegaram a rodar. A categorização não chegou a rodar. Confira de novo antes de seguir.',
    )
  })

  it('janela de um mês', () => {
    expect(fraseDoQueFicou(['2026-09'], { indice: 1, tipo: 'nao_aplicado' })).toBe(
      'As transferências de setembro foram aplicadas e continuam aplicadas. A categorização de setembro não foi aplicada. Confira de novo antes de seguir.',
    )
    expect(fraseDoQueFicou(['2026-09'], { indice: 0, tipo: 'nao_aplicado' })).toBe(
      'As transferências de setembro não foram aplicadas. A categorização não chegou a rodar. Confira de novo antes de seguir.',
    )
  })

  /** Rede caída no meio: o app NÃO afirma "não aplicado" — pode ter sido. */
  it('sem resposta: diz a dúvida, não chuta', () => {
    expect(fraseDoQueFicou(MESES, { indice: 4, tipo: 'sem_resposta' })).toBe(
      'As transferências de julho, agosto e setembro foram aplicadas e continuam aplicadas. A categorização de julho também. A de agosto ficou sem resposta, e a de setembro não chegou a rodar. Confira de novo antes de seguir.',
    )
    expect(fraseDoQueFicou(MESES, { indice: 2, tipo: 'sem_resposta' })).toBe(
      'As transferências de julho e agosto foram aplicadas e continuam aplicadas. As de setembro ficaram sem resposta. A categorização não chegou a rodar. Confira de novo antes de seguir.',
    )
  })
})

describe('frases do progresso e do resultado', () => {
  it('uma frase curta por chamada concluída, com o número da resposta', () => {
    expect(fraseDoPasso({ mes: '2026-07', etapa: 'transferencias' }, 1)).toBe('Julho: 1 par.')
    expect(fraseDoPasso({ mes: '2026-08', etapa: 'transferencias' }, 0)).toBe('Agosto: 0 pares.')
    expect(fraseDoPasso({ mes: '2026-07', etapa: 'categorizacao' }, 12)).toBe(
      'Julho: 12 categorizados.',
    )
    expect(fraseDoPasso({ mes: '2026-09', etapa: 'categorizacao' }, 1)).toBe(
      'Setembro: 1 categorizado.',
    )
  })

  it('o resultado, com os números da execução; zero em tudo é o caso idempotente', () => {
    expect(fraseDoResultado({ pares: 3, categorizados: 40 })).toBe(
      'Reprocessado — 3 pares de transferência e 40 lançamentos categorizados.',
    )
    expect(fraseDoResultado({ pares: 1, categorizados: 0 })).toBe(
      'Reprocessado — 1 par de transferência.',
    )
    expect(fraseDoResultado({ pares: 0, categorizados: 1 })).toBe(
      'Reprocessado — 1 lançamento categorizado.',
    )
    expect(fraseDoResultado({ pares: 0, categorizados: 0 })).toBe(
      'Nada mudou — não havia par para reconhecer nem lançamento sem categoria.',
    )
  })
})

describe('lerErro', () => {
  it('409 é conflito; 429 e 422 em month são limite; o resto é a frase única', () => {
    expect(lerErro(new ApiError(409, 'CONFLICT')).tipo).toBe('conflito')
    expect(lerErro(new ApiError(429, null))).toEqual({
      tipo: 'limite',
      mensagem: MSG_MUITAS_OPERACOES,
    })
    expect(
      lerErro(new ApiError(422, 'VALIDATION_FAILED', ['month'], { month: 'too_many' })),
    ).toEqual({ tipo: 'limite', mensagem: MSG_MES_GRANDE })
    expect(lerErro(new NetworkError())).toEqual({
      tipo: 'outro',
      mensagem: 'Sem conexão com o servidor. Verifique sua internet.',
    })
    expect(lerErro(new ApiError(500, null)).tipo).toBe('outro')
  })
})
