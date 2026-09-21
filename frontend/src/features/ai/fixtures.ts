import type { KeywordImportItem, KeywordImportNewCategory, KeywordImportReport } from '@/api/types'

/** Relatório FIXO de prévia para os testes da seção Importar — só testes o
 *  importam. Ele cobre, de uma vez, tudo o que a tela sabe desenhar:
 *
 *  - três categorias a criar (uma com grupo novo), uma mesclada numa
 *    existente e uma recusada por nome;
 *  - duas entradas de conta: a do Inter com uma palavra genérica (`pagamento`,
 *    87 de 212 — chama atenção) e uma específica (`banco inter`, 6), listadas
 *    na ordem INVERSA do impacto para o teste provar a ordenação; a do Nubank
 *    só com palavras legítimas (4, 3 e 0 de 212);
 *  - duas categorias existentes recebendo palavras;
 *  - seis palavras puladas e quatro recusadas — `pix` nas DUAS contas
 *    (`ambiguous_in_payload`), uma `keyword_taken` com `ownerId` para a tela
 *    resolver o nome, e uma inválida.
 *
 *  Os totais são os do SERVIDOR, e batem com as listas: 3 criadas; 17 entram
 *  (2 + 3 + 0 das novas, 2 da mesclada, 3 + 2 das existentes, 2 + 3 das
 *  contas); 6 já estavam; 4 recusadas. */

export const CONTA_NUBANK = '018f0000-0000-7000-8000-00000000c001'
export const CONTA_INTER = '018f0000-0000-7000-8000-00000000c002'
export const CATEGORIA_MERCADO = '018f0000-0000-7000-8000-00000000a001'
export const CATEGORIA_FARMACIA = '018f0000-0000-7000-8000-00000000a002'
export const CATEGORIA_PADARIA_EXISTENTE = '018f0000-0000-7000-8000-00000000a003'
export const CATEGORIA_RESTAURANTE = '018f0000-0000-7000-8000-00000000a004'
export const GRUPO_ALIMENTACAO = '018f0000-0000-7000-8000-00000000a000'
export const GRUPO_SAUDE = '018f0000-0000-7000-8000-00000000a010'

export const PADARIA: KeywordImportNewCategory = {
  ref: 'alimentacao > padaria',
  group: 'Alimentação',
  name: 'Padaria',
  kind: 'expense',
  groupIsNew: false,
  outcome: 'created',
  categoryId: null,
  add: ['padaria', 'panificadora'],
  skipped: [],
  rejected: [],
}

export const FARMACIA_NOVA: KeywordImportNewCategory = {
  ref: 'saude > farmacia',
  group: 'Saúde',
  name: 'Farmácia',
  kind: 'expense',
  groupIsNew: true,
  outcome: 'created',
  categoryId: null,
  add: ['drogaria', 'farmacia', 'droga raia'],
  skipped: [],
  rejected: [],
}

export const ACADEMIA: KeywordImportNewCategory = {
  ref: 'saude > academia',
  group: 'Saúde',
  name: 'Academia',
  kind: 'expense',
  groupIsNew: true,
  outcome: 'created',
  categoryId: null,
  add: [],
  skipped: [],
  rejected: [],
}

export const MESCLADA: KeywordImportNewCategory = {
  ref: 'alimentacao > restaurante',
  group: 'Alimentação',
  name: 'Restaurante',
  kind: 'expense',
  groupIsNew: false,
  outcome: 'merged_into_existing',
  categoryId: CATEGORIA_RESTAURANTE,
  add: ['ifood', 'rappi'],
  skipped: [{ keyword: 'restaurante', reason: 'already_present' }],
  rejected: [],
}

export const RECUSADA_POR_NOME: KeywordImportNewCategory = {
  ref: 'lazer > ',
  group: 'Lazer',
  name: '',
  kind: null,
  groupIsNew: true,
  outcome: 'invalid_name',
  categoryId: null,
  add: [],
  skipped: [],
  rejected: [],
}

export const ENTRADA_PAGAMENTO: KeywordImportItem = {
  type: 'account',
  id: CONTA_INTER,
  name: 'Inter',
  added: ['banco inter', 'pagamento'],
  skipped: [],
  rejected: [{ keyword: 'pix', reason: 'ambiguous_in_payload' }],
  // O total é a UNIÃO (89 < 6 + 87): a mesma descrição casa com as duas.
  impact: {
    transferCandidates: 89,
    byKeyword: [
      { keyword: 'banco inter', transferCandidates: 6 },
      { keyword: 'pagamento', transferCandidates: 87 },
    ],
  },
}

export const ENTRADA_NUBANK: KeywordImportItem = {
  type: 'account',
  id: CONTA_NUBANK,
  name: 'Nubank',
  added: ['nu pagamentos', 'nubank', 'nu invest'],
  skipped: [{ keyword: 'nu', reason: 'already_present' }],
  rejected: [{ keyword: 'pix', reason: 'ambiguous_in_payload' }],
  impact: {
    transferCandidates: 5,
    byKeyword: [
      { keyword: 'nu pagamentos', transferCandidates: 4 },
      { keyword: 'nubank', transferCandidates: 3 },
      { keyword: 'nu invest', transferCandidates: 0 },
    ],
  },
}

export const ENTRADA_MERCADO: KeywordImportItem = {
  type: 'category',
  id: CATEGORIA_MERCADO,
  name: 'Alimentação > Mercado',
  added: ['zaffari', 'mercado do seu jose', 'carrefour'],
  skipped: [
    { keyword: 'mercado', reason: 'already_present' },
    { keyword: 'supermercado', reason: 'already_present' },
  ],
  rejected: [{ keyword: 'padaria', reason: 'keyword_taken', ownerId: CATEGORIA_PADARIA_EXISTENTE }],
}

export const ENTRADA_FARMACIA: KeywordImportItem = {
  type: 'category',
  id: CATEGORIA_FARMACIA,
  name: 'Saúde > Remédios',
  added: ['panvel', 'pague menos'],
  skipped: [
    { keyword: 'remedio', reason: 'already_present' },
    { keyword: 'farmacia', reason: 'already_present' },
  ],
  rejected: [{ keyword: 'x', reason: 'invalid_keyword' }],
}

export function relatorioFixo(parcial: Partial<KeywordImportReport> = {}): KeywordImportReport {
  return {
    totals: {
      categoriesCreated: 3,
      added: 17,
      skipped: 6,
      rejected: 4,
      periodTransactions: 212,
    },
    newCategories: [PADARIA, FARMACIA_NOVA, ACADEMIA, MESCLADA, RECUSADA_POR_NOME],
    items: [ENTRADA_MERCADO, ENTRADA_FARMACIA, ENTRADA_PAGAMENTO, ENTRADA_NUBANK],
    ...parcial,
  }
}

/** A árvore de categorias que resolve o `ownerId` da recusa por
 *  `keyword_taken`: `Alimentação > Padaria` já existe. Só o que o teste
 *  precisa — as quatro naturezas, uma povoada. */
export function arvoreFixa() {
  const base = {
    kind: 'expense' as const,
    keywords: [],
    archivedAt: null,
    createdAt: '2026-09-01T00:00:00Z',
    updatedAt: '2026-09-01T00:00:00Z',
    children: [],
  }
  return {
    expense: [
      {
        ...base,
        id: GRUPO_ALIMENTACAO,
        name: 'Alimentação',
        parentId: null,
        children: [
          { ...base, id: CATEGORIA_MERCADO, name: 'Mercado', parentId: GRUPO_ALIMENTACAO },
          {
            ...base,
            id: CATEGORIA_PADARIA_EXISTENTE,
            name: 'Padaria',
            parentId: GRUPO_ALIMENTACAO,
          },
        ],
      },
      {
        ...base,
        id: GRUPO_SAUDE,
        name: 'Saúde',
        parentId: null,
        children: [{ ...base, id: CATEGORIA_FARMACIA, name: 'Remédios', parentId: GRUPO_SAUDE }],
      },
    ],
    income: [],
    investment: [],
    redemption: [],
  }
}

/** O JSON que a IA respondeu, como TEXTO — é o que os testes colam. O
 *  conteúdo não precisa bater com o relatório fixo (quem responde o relatório
 *  é o mock do servidor); precisa ser um objeto JSON válido. */
export const JSON_DA_IA = JSON.stringify(
  {
    homefinanceKeywordImport: 1,
    newCategories: [{ group: 'Alimentação', name: 'Padaria', add: ['padaria', 'panificadora'] }],
    accountKeywords: [{ accountId: CONTA_INTER, accountName: 'Inter', add: ['pagamento'] }],
    notes: 'explicação que a IA sempre quer dar',
  },
  null,
  2,
)
