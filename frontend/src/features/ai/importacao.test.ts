import { describe, expect, it } from 'vitest'
import { ApiError, NetworkError } from '@/api/client'
import type {
  KeywordImportRejectReason,
  KeywordImportSkipReason,
  NewCategoryOutcome,
} from '@/api/types'
import {
  ACADEMIA,
  arvoreFixa,
  CATEGORIA_PADARIA_EXISTENTE,
  CONTA_NUBANK,
  ENTRADA_NUBANK,
  ENTRADA_PAGAMENTO,
  FARMACIA_NOVA,
  PADARIA,
  relatorioFixo,
} from './fixtures'
import {
  bytesUtf8,
  categoriasACriar,
  categoriasMescladas,
  chamaAtencao,
  contarAplicavel,
  corpoCabe,
  erroDaConferencia,
  fraseDoResultado,
  fraseDosGruposNovos,
  fraseDosTotais,
  gruposDeConta,
  janelaGrandeDemais,
  lerJsonColado,
  MAX_BYTES_DO_JSON,
  MOTIVO_DA_RECUSA,
  MOTIVO_DO_DESFECHO,
  MOTIVO_DO_PULO,
  MSG_GRANDE,
  MSG_JANELA_GRANDE,
  MSG_JSON_INVALIDO,
  motivoDaRecusa,
  nomeDoDono,
  oQueFicaDeFora,
  rotuloDaCaixa,
  rotuloDoConfirmar,
} from './importacao'

describe('lerJsonColado', () => {
  it('aceita um objeto JSON e devolve o payload como veio', () => {
    const lido = lerJsonColado('{"homefinanceKeywordImport": 1, "notes": "x"}')
    expect(lido.ok).toBe(true)
    if (lido.ok) expect(lido.payload).toEqual({ homefinanceKeywordImport: 1, notes: 'x' })
  })

  it.each([
    ['texto antes do JSON', 'Aqui está: {"a":1}'],
    ['JSON truncado', '{"homefinanceKeywordImport": 1, "newCategories": ['],
    ['vazio', ''],
    ['uma lista', '[1, 2]'],
    ['um número', '42'],
    ['null', 'null'],
  ])('recusa %s com a frase do campo, sem ecoar o texto', (_, texto) => {
    const lido = lerJsonColado(texto)
    expect(lido.ok).toBe(false)
    if (!lido.ok) {
      expect(lido.mensagem).toBe(MSG_JSON_INVALIDO)
      expect(lido.mensagem).not.toContain(texto.slice(0, 5) || '§')
    }
  })
})

/** Um JSON válido com EXATAMENTE `bytes` bytes UTF-8: o preenchimento vai em
 *  `notes`, que o servidor descarta. `recheio` é a unidade repetida — `x`
 *  (1 byte) ou `é` (2 bytes), para o teste do multibyte. */
function jsonComBytes(bytes: number, recheio = 'x'): string {
  const molde = '{"homefinanceKeywordImport":1,"notes":""}'
  const fixo = bytesUtf8(molde)
  const porUnidade = bytesUtf8(recheio)
  const unidades = Math.floor((bytes - fixo) / porUnidade)
  const sobra = bytes - fixo - unidades * porUnidade
  const texto = molde.replace('""', `"${recheio.repeat(unidades)}${'x'.repeat(sobra)}"`)
  if (bytesUtf8(texto) !== bytes) throw new Error(`molde errado: ${bytesUtf8(texto)} ≠ ${bytes}`)
  return texto
}

describe('lerJsonColado — o teto de 128 KiB, medido em bytes UTF-8 (achado B2 do QA)', () => {
  it('o teto é o do servidor: 131.072', () => {
    expect(MAX_BYTES_DO_JSON).toBe(131_072)
  })

  it('131.072 bytes passa; 131.073 barra, no campo, com a frase do 413', () => {
    expect(lerJsonColado(jsonComBytes(131_072)).ok).toBe(true)
    const barrado = lerJsonColado(jsonComBytes(131_073))
    expect(barrado.ok).toBe(false)
    if (!barrado.ok) expect(barrado.mensagem).toBe(MSG_GRANDE)
  })

  it('conta bytes, não `length`: um texto com acento perto do limite barra mesmo com length menor', () => {
    // ~70.000 «é» = 140.000 bytes, mas `length` ≈ 70.040 < 131.072.
    const texto = jsonComBytes(140_000, 'é')
    expect(texto.length).toBeLessThan(MAX_BYTES_DO_JSON)
    expect(bytesUtf8(texto)).toBeGreaterThan(MAX_BYTES_DO_JSON)
    const lido = lerJsonColado(texto)
    expect(lido.ok).toBe(false)
    if (!lido.ok) expect(lido.mensagem).toBe(MSG_GRANDE)
    // E o mesmo recheio no limite exato, ainda multibyte, passa.
    expect(lerJsonColado(jsonComBytes(131_072, 'é')).ok).toBe(true)
  })

  it('o envelope inteiro é o que se mede: os refs do confirm somam ao corpo', () => {
    // 131.030 bytes de payload cabem sozinhos, mas a moldura do envelope
    // (`{"payload":…,"fromMonth":"2026-07","toMonth":"2026-09"}`, 54 bytes)
    // leva o corpo a 131.084 — acima do teto.
    const payload = JSON.parse(jsonComBytes(131_030)) as unknown
    expect(lerJsonColado(jsonComBytes(131_030)).ok).toBe(true)
    expect(corpoCabe({ payload, fromMonth: '2026-07', toMonth: '2026-09' })).toBe(false)
    const menor = {
      payload: JSON.parse(jsonComBytes(130_900)) as unknown,
      fromMonth: '2026-07',
      toMonth: '2026-09',
    }
    expect(corpoCabe(menor)).toBe(true)
    // Com 200 refs, um confirm que não cabe onde a prévia coube.
    const refs = Array.from({ length: 200 }, (_, i) => `grupo ${i} > folha ${i}`)
    expect(corpoCabe({ ...menor, skipNewCategories: refs })).toBe(false)
  })
})

describe('erroDaConferencia — cada 400 tem a sua frase, pelo campo e nunca pelo texto', () => {
  it('versão ausente ou diferente de 1', () => {
    const erro = new ApiError(400, 'VALIDATION_FAILED', ['payload.homefinanceKeywordImport'], {
      'payload.homefinanceKeywordImport': 'Valor inválido para este campo.',
    })
    expect(erroDaConferencia(erro)).toEqual({
      onde: 'campo',
      mensagem:
        'Falta a linha "homefinanceKeywordImport": 1 no começo do JSON — a IA respondeu noutro formato.',
    })
    // O mesmo se o servidor nomear o campo sem o prefixo.
    expect(
      erroDaConferencia(new ApiError(400, 'VALIDATION_FAILED', ['homefinanceKeywordImport']))
        .mensagem,
    ).toContain('homefinanceKeywordImport')
  })

  it('as três listas vazias — `fields.payload` (achado A3)', () => {
    const erro = new ApiError(400, 'VALIDATION_FAILED', ['payload'], { payload: 'x' })
    expect(erroDaConferencia(erro)).toEqual({
      onde: 'campo',
      mensagem: 'O JSON não traz nenhuma palavra-chave nem categoria.',
    })
  })

  it('campo desconhecido (400 sem campo nomeado) cai na frase do formato', () => {
    const erro = new ApiError(400, 'VALIDATION_FAILED')
    expect(erroDaConferencia(erro)).toEqual({
      onde: 'campo',
      mensagem:
        'O JSON traz um campo que este app não aceita. Peça à IA para responder só com o formato do prompt.',
    })
  })

  it('413 e 429 têm frase própria, no campo', () => {
    expect(erroDaConferencia(new ApiError(413, 'PAYLOAD_TOO_LARGE'))).toEqual({
      onde: 'campo',
      mensagem: 'O JSON passa de 128 KB. Reduza o período e peça de novo.',
    })
    expect(erroDaConferencia(new ApiError(429, 'RATE_LIMITED'))).toEqual({
      onde: 'campo',
      mensagem: 'Muitas conferências seguidas. Tente de novo em um minuto.',
    })
  })

  it('422 em toMonth vai para a janela — a ação é o seletor do topo', () => {
    const erro = new ApiError(422, 'VALIDATION_FAILED', ['toMonth'], { toMonth: 'x' })
    expect(erroDaConferencia(erro)).toEqual({ onde: 'janela', mensagem: MSG_JANELA_GRANDE })
    expect(janelaGrandeDemais(erro)).toBe(true)
    // Um 422 que aponta OUTRO campo não é a janela.
    expect(janelaGrandeDemais(new ApiError(422, 'VALIDATION_FAILED', ['x'], { x: 'x' }))).toBe(
      false,
    )
  })

  it('502/503/504 vão para a seção com a frase do proxy, não o genérico', () => {
    for (const status of [502, 503, 504]) {
      expect(erroDaConferencia(new ApiError(status, null))).toEqual({
        onde: 'secao',
        mensagem:
          'O servidor recusou o envio antes de ler tudo — o JSON pode estar grande demais, ou a API está fora do ar.',
      })
    }
  })

  it('rede e 500 vão para a seção, com a frase única do app', () => {
    expect(erroDaConferencia(new NetworkError())).toEqual({
      onde: 'secao',
      mensagem: 'Sem conexão com o servidor. Verifique sua internet.',
    })
    expect(erroDaConferencia(new ApiError(500, 'INTERNAL_ERROR'))).toEqual({
      onde: 'secao',
      mensagem: 'Algo falhou do nosso lado. Tente de novo em instantes.',
    })
  })
})

describe('léxico — os Records são exaustivos contra o enum gerado', () => {
  /** Se o contrato ganhar um motivo, o `Record` deixa de compilar; este teste
   *  afirma o outro lado — nenhuma frase é vazia nem é o código cru. */
  it('toda recusa tem frase em português, e nenhuma é o código', () => {
    const recusas: KeywordImportRejectReason[] = [
      'item_not_found',
      'item_archived',
      'name_mismatch',
      'group_has_children',
      'invalid_keyword',
      'keyword_taken',
      'ambiguous_in_payload',
      'limit_exceeded',
    ]
    for (const recusa of recusas) {
      expect(MOTIVO_DA_RECUSA[recusa]).not.toBe('')
      expect(MOTIVO_DA_RECUSA[recusa]).not.toBe(recusa)
      expect(MOTIVO_DA_RECUSA[recusa]).not.toMatch(/_/)
    }
    expect(Object.keys(MOTIVO_DA_RECUSA).sort()).toEqual([...recusas].sort())
  })

  it('todo desfecho de categoria nova tem frase', () => {
    const desfechos: NewCategoryOutcome[] = [
      'created',
      'merged_into_existing',
      'skipped_by_user',
      'invalid_name',
      'kind_required',
      'invalid_kind',
      'kind_mismatch',
      'name_taken_archived',
      'household_limit',
      'duplicate_in_payload',
    ]
    for (const desfecho of desfechos) {
      expect(MOTIVO_DO_DESFECHO[desfecho]).not.toBe('')
      expect(MOTIVO_DO_DESFECHO[desfecho]).not.toMatch(/_/)
    }
    expect(Object.keys(MOTIVO_DO_DESFECHO).sort()).toEqual([...desfechos].sort())
    const pulos: KeywordImportSkipReason[] = ['already_present']
    expect(Object.keys(MOTIVO_DO_PULO)).toEqual(pulos)
  })

  it('keyword_taken diz de quem é, resolvendo o ownerId na lista carregada', () => {
    const recusa = {
      keyword: 'padaria',
      reason: 'keyword_taken' as const,
      ownerId: CATEGORIA_PADARIA_EXISTENTE,
    }
    const dono = nomeDoDono(recusa, 'category', arvoreFixa(), [])
    expect(dono).toBe('Alimentação > Padaria')
    expect(motivoDaRecusa(recusa, 'category', dono)).toBe('já está em Alimentação > Padaria')

    // Sem a lista (cache vazio), a frase não cala nem inventa.
    expect(motivoDaRecusa(recusa, 'category', undefined)).toBe('já está em outra categoria')
    expect(motivoDaRecusa(recusa, 'account', undefined)).toBe('já está em outra conta')

    // Conta resolve na lista de contas, pelo id. Só `id` e `name` importam
    // aqui; o resto da `Account` não entra na frase.
    const contas = [{ id: CONTA_NUBANK, name: 'Nubank' }] as unknown as Parameters<
      typeof nomeDoDono
    >[3]
    expect(nomeDoDono({ ...recusa, ownerId: CONTA_NUBANK }, 'account', undefined, contas)).toBe(
      'Nubank',
    )
  })
})

describe('chamaAtencao — as duas condições, absoluta E proporcional', () => {
  it.each([
    [87, 212, true, 'genérica: 41% do período'],
    [10, 212, false, 'atinge o piso absoluto, mas é 4,7%'],
    [24, 500, false, 'nubank legítimo: 24 acertos são 4,8% de 500'],
    [24, 212, true, '24 de 212 são 11,3%: passa das duas'],
    [22, 212, true, 'fronteira: 22 × 10 = 220 ≥ 212'],
    [21, 212, false, 'fronteira: 21 × 10 = 210 < 212'],
    [9, 20, false, '45% do período, mas abaixo do piso de 10'],
    [10, 100, true, 'exatamente 10 e exatamente 10%'],
    [5, 0, false, 'período sem lançamento não chama atenção de nada'],
  ])('%i de %i → %s (%s)', (candidatos, universo, esperado) => {
    expect(chamaAtencao(candidatos, universo)).toBe(esperado)
  })
})

describe('os blocos a partir do relatório fixo', () => {
  const relatorio = relatorioFixo()

  it('bloco A: só `created`, na ordem do JSON', () => {
    expect(categoriasACriar(relatorio).map((e) => e.ref)).toEqual([
      'alimentacao > padaria',
      'saude > farmacia',
      'saude > academia',
    ])
  })

  it('bloco C recebe as mescladas com palavra; a recusada por nome não vai a lugar nenhum', () => {
    expect(categoriasMescladas(relatorio).map((e) => e.ref)).toEqual(['alimentacao > restaurante'])
  })

  it('bloco B: um grupo por conta (ordem do JSON), uma linha por palavra em impacto decrescente', () => {
    const grupos = gruposDeConta(relatorio)
    expect(grupos.map((g) => g.rotulo)).toEqual(['Inter · 2', 'Nubank · 3'])
    // O servidor mandou `banco inter` antes de `pagamento`; a tela inverte,
    // porque quem manda é o impacto — e só a genérica chama atenção.
    expect(grupos[0]?.palavras.map((p) => [p.palavra, p.candidatos, p.atencao])).toEqual([
      ['pagamento', 87, true],
      ['banco inter', 6, false],
    ])
    expect(grupos[1]?.palavras.map((p) => [p.palavra, p.candidatos, p.atencao])).toEqual([
      ['nu pagamentos', 4, false],
      ['nubank', 3, false],
      ['nu invest', 0, false],
    ])
    // A ordem dos GRUPOS é a do JSON, não a do impacto.
    expect(
      gruposDeConta(relatorioFixo({ items: [ENTRADA_NUBANK, ENTRADA_PAGAMENTO] })).map(
        (g) => g.rotulo,
      ),
    ).toEqual(['Nubank · 3', 'Inter · 2'])
    // Chave pelo índice: dois itens com `id` vazio não colidem.
    const semId = gruposDeConta(
      relatorioFixo({
        items: [
          { ...ENTRADA_NUBANK, id: '' },
          { ...ENTRADA_NUBANK, id: '' },
        ],
      }),
    )
    expect(new Set(semId.map((g) => g.chave)).size).toBe(2)
    expect(new Set(semId.flatMap((g) => g.palavras.map((p) => p.chave))).size).toBe(6)
  })

  it('bloco B não existe sem `impact` (o confirm não o traz)', () => {
    const { impact: _semImpacto, ...semImpacto } = ENTRADA_PAGAMENTO
    expect(gruposDeConta(relatorioFixo({ items: [semImpacto] }))).toEqual([])
  })

  it('o que fica de fora: puladas e recusadas, cada uma com item e motivo', () => {
    const fora = oQueFicaDeFora(relatorio, (recusa, tipo) =>
      nomeDoDono(recusa, tipo, arvoreFixa(), []),
    )
    expect(fora.jaEstavam.map((l) => [l.palavra, l.item, l.motivo])).toEqual([
      ['«mercado»', 'Alimentação > Mercado', 'já estava lá'],
      ['«supermercado»', 'Alimentação > Mercado', 'já estava lá'],
      ['«remedio»', 'Saúde > Remédios', 'já estava lá'],
      ['«farmacia»', 'Saúde > Remédios', 'já estava lá'],
      ['«nu»', 'Nubank', 'já estava lá'],
      ['«restaurante»', 'Alimentação > Restaurante', 'já estava lá'],
    ])
    expect(fora.recusadas.map((l) => [l.palavra, l.item, l.motivo])).toEqual([
      ['«padaria»', 'Alimentação > Mercado', 'já está em Alimentação > Padaria'],
      ['«x»', 'Saúde > Remédios', 'fora do formato de palavra-chave'],
      ['«pix»', 'Inter', 'aparece em dois itens no mesmo JSON'],
      ['«pix»', 'Nubank', 'aparece em dois itens no mesmo JSON'],
      ['Lazer > ', 'categoria nova', 'nome fora do formato (1 a 60 caracteres, sem >)'],
    ])
    // Nenhum motivo é o código cru.
    for (const linha of [...fora.jaEstavam, ...fora.recusadas]) {
      expect(linha.motivo).not.toMatch(/_/)
    }
  })

  it('o nome acessível da caixa traz o caminho inteiro e a contagem', () => {
    expect(rotuloDaCaixa(PADARIA)).toBe('Criar Alimentação > Padaria com 2 palavras-chave')
    expect(rotuloDaCaixa(FARMACIA_NOVA)).toBe('Criar Saúde > Farmácia com 3 palavras-chave')
    expect(rotuloDaCaixa(ACADEMIA)).toBe('Criar Saúde > Academia sem palavra-chave')
  })
})

describe('contagens que recalculam ao desmarcar (aceite 48)', () => {
  const relatorio = relatorioFixo()

  it('com tudo marcado, os números são os do servidor', () => {
    const aplicavel = contarAplicavel(relatorio, new Set())
    expect(aplicavel).toEqual({ categorias: 3, palavras: 17, gruposNovos: 1 })
    expect(rotuloDoConfirmar(aplicavel)).toBe('Criar 3 categorias e gravar 17 palavras')
    expect(fraseDosTotais(relatorio.totals, aplicavel)).toBe(
      '3 categorias novas · 17 palavras entram · 6 já estavam lá · 4 recusadas.',
    )
    // Farmácia e Academia nascem no MESMO grupo novo: ele conta uma vez.
    expect(fraseDosGruposNovos(aplicavel)).toBe('Inclui 1 grupo novo.')
  })

  it('desmarcar uma categoria de 2 palavras faz o número cair 2', () => {
    const aplicavel = contarAplicavel(relatorio, new Set(['alimentacao > padaria']))
    expect(aplicavel).toEqual({ categorias: 2, palavras: 15, gruposNovos: 1 })
    expect(rotuloDoConfirmar(aplicavel)).toBe('Criar 2 categorias e gravar 15 palavras')
  })

  it('desmarcar as duas do grupo novo apaga o grupo da nuance', () => {
    const aplicavel = contarAplicavel(relatorio, new Set(['saude > farmacia', 'saude > academia']))
    expect(aplicavel).toEqual({ categorias: 1, palavras: 14, gruposNovos: 0 })
    expect(fraseDosGruposNovos(aplicavel)).toBe('')
  })

  it('desmarcar todas deixa só as palavras dos itens existentes', () => {
    const aplicavel = contarAplicavel(
      relatorio,
      new Set(['alimentacao > padaria', 'saude > farmacia', 'saude > academia']),
    )
    expect(aplicavel).toEqual({ categorias: 0, palavras: 12, gruposNovos: 0 })
    expect(rotuloDoConfirmar(aplicavel)).toBe('Gravar 12 palavras')
  })

  it('sem nada para aplicar, o rótulo diz isso', () => {
    expect(rotuloDoConfirmar({ categorias: 0, palavras: 0, gruposNovos: 0 })).toBe(
      'Nada para aplicar',
    )
    expect(rotuloDoConfirmar({ categorias: 2, palavras: 0, gruposNovos: 0 })).toBe(
      'Criar 2 categorias',
    )
    expect(rotuloDoConfirmar({ categorias: 1, palavras: 1, gruposNovos: 0 })).toBe(
      'Criar 1 categoria e gravar 1 palavra',
    )
  })

  it('a frase dos totais omite segmentos zerados, menos o das palavras', () => {
    expect(
      fraseDosTotais(
        { categoriesCreated: 0, added: 0, skipped: 0, rejected: 1, periodTransactions: 0 },
        { categorias: 0, palavras: 0, gruposNovos: 0 },
      ),
    ).toBe('nenhuma palavra entra · 1 recusada.')
    expect(
      fraseDosTotais(
        { categoriesCreated: 1, added: 1, skipped: 1, rejected: 0, periodTransactions: 0 },
        { categorias: 1, palavras: 1, gruposNovos: 0 },
      ),
    ).toBe('1 categoria nova · 1 palavra entra · 1 já estava lá.')
  })

  it('um ref que não é de categoria criada não desconta nada', () => {
    const aplicavel = contarAplicavel(relatorio, new Set(['alimentacao > restaurante']))
    expect(aplicavel.palavras).toBe(17)
    expect(aplicavel.categorias).toBe(3)
  })
})

describe('fraseDoResultado — os números do confirm', () => {
  it.each([
    [2, 16, '2 categorias criadas e 16 palavras gravadas.'],
    [1, 1, '1 categoria criada e 1 palavra gravada.'],
    [0, 16, '16 palavras gravadas.'],
    [2, 0, '2 categorias criadas.'],
    [0, 0, 'Nada foi gravado.'],
  ])('%i categorias, %i palavras → %s', (categorias, palavras, esperado) => {
    expect(
      fraseDoResultado({
        categoriesCreated: categorias,
        added: palavras,
        skipped: 0,
        rejected: 0,
        periodTransactions: 0,
      }),
    ).toBe(esperado)
  })
})
