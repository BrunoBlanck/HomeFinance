import { ApiError, NetworkError } from '@/api/client'
import type {
  Account,
  CategoryKind,
  CategoryTree,
  KeywordImportItem,
  KeywordImportNewCategory,
  KeywordImportPayload,
  KeywordImportRejectedKeyword,
  KeywordImportRejectReason,
  KeywordImportReport,
  KeywordImportSkipReason,
  KeywordImportTotals,
  NewCategoryOutcome,
} from '@/api/types'
import { contaPorId } from '@/lib/accounts'
import { NATUREZAS } from '@/lib/categories'
import { messageForError } from '@/lib/errors'
import { citarPalavra } from '@/lib/keywords'

/** A lógica da seção **Importar** de `/ia` (spec 0010 §4), sem JSX — para os
 *  testes afirmarem cada regra sem montar a tela.
 *
 *  Três coisas moram aqui, e as três são texto ou contagem, nunca dinheiro:
 *
 *  1. **O léxico**: os `reason`/`outcome` do contrato viram frase em pt-BR
 *     por `Record` EXAUSTIVO — motivo novo no contrato é erro de compilação,
 *     nunca `keyword_taken` cru na tela (o mesmo padrão de
 *     `MOTIVO_DA_REJEICAO` na importação de extratos).
 *  2. **O limiar de atenção** do impacto medido (§4.4 e §10.1): duas condições,
 *     absoluta E proporcional.
 *  3. **As contagens que recalculam ao desmarcar**: o rótulo do botão de
 *     confirmar e a frase dos totais. Subtrair contagem no cliente é permitido
 *     — a regra de "somar num lugar só" fala de centavos (§10.5), e aqui não
 *     há nenhum. */

// ------------------------------------------------------------ leitura

/** O que a tela diz quando o texto colado não pode nem virar pedido. A copy é
 *  da direção do `designer-ui` (docs/DESIGN.md, spec 0010). */
export const MSG_JSON_INVALIDO =
  'Isso não é um JSON válido. Cole o texto da resposta inteiro, do primeiro { ao último }.'

export type JsonColado =
  | { ok: true; payload: KeywordImportPayload }
  | { ok: false; mensagem: string }

/** O teto de corpo das duas rotas — o MESMO `aiimport.MaxPayloadBytes` do
 *  servidor (128 KiB). Espelhado aqui por um motivo medido pelo QA (achado B2,
 *  21/09/2026): direto na API um corpo maior é **413**, mas pelo proxy do Vite
 *  chega **502** (`ECONNRESET` — o servidor responde e fecha sem drenar o
 *  corpo), e um reverse proxy em produção tende a fazer o mesmo. Um 502 cairia
 *  em "Tentar de novo", que repete o erro para sempre. Barrar ANTES de
 *  qualquer requisição é o que resolve; o 413 continua mapeado como defesa em
 *  profundidade, para o dia em que o teto do servidor mudar. */
export const MAX_BYTES_DO_JSON = 128 * 1024

/** Tamanho em bytes **UTF-8**, que é o que o servidor conta — `length` de
 *  string conta unidades UTF-16 e mentiria em qualquer texto com acento. */
export function bytesUtf8(texto: string): number {
  return new TextEncoder().encode(texto).byteLength
}

export const MSG_GRANDE = 'O JSON passa de 128 KB. Reduza o período e peça de novo.'

/** `true` quando o corpo que SERIA enviado cabe no teto. Confere o envelope
 *  inteiro, e não só o texto colado: no confirm, `skipNewCategories` soma ao
 *  corpo (até 200 caminhos), e uma prévia que coube pode virar um confirm que
 *  não cabe. */
export function corpoCabe(corpo: unknown): boolean {
  return bytesUtf8(JSON.stringify(corpo)) <= MAX_BYTES_DO_JSON
}

/** Lê o texto colado. A tela só garante o que dá para garantir sem inventar
 *  regra: é JSON bem formado e é um objeto (não `null`, não lista, não número).
 *
 *  Tudo o mais — versão, campos desconhecidos, listas vazias, tetos — é do
 *  servidor, que é a autoridade e responde 400 por campo. Duplicar essas
 *  regras aqui seria uma segunda cópia que diverge no primeiro ajuste. O
 *  `as` na volta é a única fronteira de tipo desta feature, e é honesta: o
 *  contrato diz que o corpo é "o JSON da IA como veio", e é exatamente isso que
 *  é devolvido. */
export function lerJsonColado(texto: string): JsonColado {
  // Tamanho ANTES de qualquer coisa — e antes do `JSON.parse`, que num texto
  // de megabytes seria trabalho à toa para um pedido que não vai sair.
  if (bytesUtf8(texto) > MAX_BYTES_DO_JSON) return { ok: false, mensagem: MSG_GRANDE }
  let valor: unknown
  try {
    valor = JSON.parse(texto)
  } catch {
    return { ok: false, mensagem: MSG_JSON_INVALIDO }
  }
  if (valor === null || typeof valor !== 'object' || Array.isArray(valor)) {
    return { ok: false, mensagem: MSG_JSON_INVALIDO }
  }
  return { ok: true, payload: valor as KeywordImportPayload }
}

// --------------------------------------------------------- erro do 400

/** Onde o erro da conferência mora: **no campo** (a pessoa corrige o que
 *  colou), **na janela** (o período é grande demais para a medição — a ação
 *  é trocar o seletor do topo, e "tentar de novo" daria o mesmo 422) ou na
 *  seção (rede, servidor — não há o que corrigir no texto). */
export type ErroDaConferencia = { onde: 'campo' | 'janela' | 'secao'; mensagem: string }

const MSG_VERSAO =
  'Falta a linha "homefinanceKeywordImport": 1 no começo do JSON — a IA respondeu noutro formato.'
const MSG_VAZIO = 'O JSON não traz nenhuma palavra-chave nem categoria.'
const MSG_CAMPO_DESCONHECIDO =
  'O JSON traz um campo que este app não aceita. Peça à IA para responder só com o formato do prompt.'
const MSG_RATE_LIMIT = 'Muitas conferências seguidas. Tente de novo em um minuto.'
/** 502/503/504: um proxy no meio recusou ou cortou. O caso medido (achado B2)
 *  é o corpo grande que o servidor recusa sem drenar — a pré-checagem já barra
 *  esse antes do pedido, então o que sobra aqui é raro; a frase diz as duas
 *  possibilidades em vez de fingir que sabe. */
const MSG_PROXY =
  'O servidor recusou o envio antes de ler tudo — o JSON pode estar grande demais, ou a API está fora do ar.'
/** O 422 em `fields.toMonth` das duas rotas: a janela tem descrições demais
 *  para a medição de impacto caber (ou para o denominador ser contado), tudo
 *  ou nada. Nada foi gravado, e repetir dá o mesmo 422 — a saída é o seletor
 *  do topo. */
export const MSG_JANELA_GRANDE =
  'A janela tem lançamentos demais para medir o impacto. Escolha um período menor no alto da página e confira de novo.'

/** Traduz a falha de `POST /ai/keyword-import/preview` para a frase e o lugar.
 *
 *  Os 400 são todos `VALIDATION_FAILED` (o `ErrorCode` é fechado, e código
 *  próprio só quando a AÇÃO da tela difere — achado A3 da emenda §10), então
 *  a distinção vem do NOME do campo em `fields`, que o servidor produz e que
 *  não é prosa: `…homefinanceKeywordImport` é a versão, `payload` são as três
 *  listas vazias. Qualquer outro 400 — campo desconhecido, lista acima do
 *  teto, tipo errado — cai na frase do formato, porque a ação da pessoa é a
 *  mesma nos três: pedir à IA o formato do prompt. O texto malformado nunca
 *  chega aqui: `lerJsonColado` o barra antes do pedido.
 *
 *  413 e 429 têm frase própria porque a ação difere (reduzir o período;
 *  esperar), e o 422 em `fields.toMonth` também (encurtar a janela — e sem
 *  "tentar de novo", que daria o mesmo 422). O resto — rede, 500, sessão —
 *  vai para a seção com a frase única do app. Nenhuma mensagem ecoa o conteúdo colado, e `notes` nunca
 *  passa por aqui: o servidor não o devolve. */
export function erroDaConferencia(error: unknown): ErroDaConferencia {
  if (error instanceof NetworkError) return { onde: 'secao', mensagem: messageForError(error) }
  if (!(error instanceof ApiError)) return { onde: 'secao', mensagem: messageForError(error) }

  if (error.status === 413) return { onde: 'campo', mensagem: MSG_GRANDE }
  if (error.status === 429) return { onde: 'campo', mensagem: MSG_RATE_LIMIT }
  if (error.status === 502 || error.status === 503 || error.status === 504) {
    return { onde: 'secao', mensagem: MSG_PROXY }
  }
  if (janelaGrandeDemais(error)) return { onde: 'janela', mensagem: MSG_JANELA_GRANDE }
  if (error.status === 400) {
    const campos = error.invalidFields
    if (campos.some((campo) => /(^|\.)homefinanceKeywordImport$/.test(campo))) {
      return { onde: 'campo', mensagem: MSG_VERSAO }
    }
    if (campos.some((campo) => /(^|\.)payload$/.test(campo))) {
      return { onde: 'campo', mensagem: MSG_VAZIO }
    }
    return { onde: 'campo', mensagem: MSG_CAMPO_DESCONHECIDO }
  }
  return { onde: 'secao', mensagem: messageForError(error) }
}

/** `true` quando o 422 aponta `toMonth`: a janela é grande demais para a
 *  medição (prévia) ou para o denominador (confirm). Nas duas, nada mudou e
 *  a ação é a mesma — encurtar a janela —, nunca "tentar de novo". */
export function janelaGrandeDemais(error: unknown): boolean {
  return (
    error instanceof ApiError && error.status === 422 && typeof error.fields.toMonth === 'string'
  )
}

// -------------------------------------------------------------- léxico

/** A natureza em minúscula, para o miolo da frase `Grupo novo · natureza:
 *  despesa`. `Record` exaustivo: natureza nova no contrato cobra a palavra. */
export const NATUREZA_NA_FRASE: Record<CategoryKind, string> = {
  expense: 'despesa',
  income: 'receita',
  investment: 'investimento',
  redemption: 'resgate',
}

/** O motivo de uma palavra **pulada** (§4.2, regra 7). Só existe um, e ele é
 *  o que torna reimportar o mesmo JSON inofensivo. */
export const MOTIVO_DO_PULO: Record<KeywordImportSkipReason, string> = {
  already_present: 'já estava lá',
}

/** O motivo de uma palavra **recusada** (§4.2). `keyword_taken` ganha o nome
 *  do dono em `motivoDaRecusa`; a frase daqui é o que sobra quando o dono não
 *  se resolve na lista carregada. */
export const MOTIVO_DA_RECUSA: Record<KeywordImportRejectReason, string> = {
  item_not_found: 'não existe nesta casa',
  item_archived: 'está arquivada — desarquive para receber palavras',
  name_mismatch: 'o nome que veio no JSON não é o deste item',
  group_has_children: 'é um grupo com subcategorias — a palavra vai numa subcategoria',
  invalid_keyword: 'fora do formato de palavra-chave',
  keyword_taken: 'já está em outro item',
  ambiguous_in_payload: 'aparece em dois itens no mesmo JSON',
  limit_exceeded: 'passaria de 20 palavras',
}

/** O que aconteceu com uma entrada de `newCategories` (§4.3), como frase. As
 *  três primeiras não são recusa e não aparecem na lista do que fica de fora
 *  — estão aqui porque o `Record` é exaustivo por construção. */
export const MOTIVO_DO_DESFECHO: Record<NewCategoryOutcome, string> = {
  created: 'entra',
  merged_into_existing: 'já existia — as palavras vão para ela',
  skipped_by_user: 'desmarcada por você',
  invalid_name: 'nome fora do formato (1 a 60 caracteres, sem >)',
  kind_required: 'grupo novo sem natureza',
  invalid_kind: 'natureza fora do conjunto (despesa, receita, investimento, resgate)',
  kind_mismatch: 'natureza diferente da do grupo',
  name_taken_archived: 'existe arquivada com este nome — desarquive em Categorias',
  household_limit: 'passaria do teto de 200 categorias',
  duplicate_in_payload: 'repetida no mesmo JSON — a primeira vale',
}

/** `keyword_taken` diz de quem é a palavra: `já está em Alimentação > Mercado`.
 *  O nome vem da lista de categorias/contas **já carregada**, resolvida pelo
 *  `ownerId` — nunca de texto do JSON, que é a IA falando. Sem nome (cache
 *  ainda vazio, item que a lista não trouxe), a frase fala em "outra
 *  categoria"/"outra conta" em vez de calar ou inventar. */
export function motivoDaRecusa(
  recusa: KeywordImportRejectedKeyword,
  tipo: 'category' | 'account',
  nomeDoDono: string | undefined,
): string {
  if (recusa.reason !== 'keyword_taken') return MOTIVO_DA_RECUSA[recusa.reason]
  if (nomeDoDono) return `já está em ${nomeDoDono}`
  return tipo === 'account' ? 'já está em outra conta' : 'já está em outra categoria'
}

/** `Grupo > Folha` de uma categoria pelo id, em qualquer natureza — ou só
 *  `Grupo` quando o id é de um grupo. `undefined` quando a árvore não a tem. */
export function caminhoDaCategoria(
  arvore: CategoryTree | undefined,
  id: string | undefined,
): string | undefined {
  if (!arvore || !id) return undefined
  for (const natureza of NATUREZAS) {
    for (const grupo of arvore[natureza] ?? []) {
      if (grupo.id === id) return grupo.name
      const filha = grupo.children.find((categoria) => categoria.id === id)
      if (filha) return `${grupo.name} > ${filha.name}`
    }
  }
  return undefined
}

/** O nome do dono de uma palavra recusada por `keyword_taken`, do tipo do
 *  item em que ela ia entrar: categoria procura na árvore, conta na lista. */
export function nomeDoDono(
  recusa: KeywordImportRejectedKeyword,
  tipo: 'category' | 'account',
  arvore: CategoryTree | undefined,
  contas: readonly Account[],
): string | undefined {
  if (!recusa.ownerId) return undefined
  return tipo === 'account'
    ? contaPorId(contas, recusa.ownerId)?.name
    : caminhoDaCategoria(arvore, recusa.ownerId)
}

// ------------------------------------------------------------ atenção

/** Piso absoluto do limiar de atenção: abaixo de 10 lançamentos, a palavra
 *  não converte o bastante para ser "genérica", seja qual for a proporção. */
export const ATENCAO_MINIMO = 10

/** O limiar de atenção do impacto medido (spec 0010 §4.4 e §10.1): a
 *  palavra chama atenção quando os candidatos são **ao menos 10** E **ao
 *  menos 10%** dos lançamentos do período.
 *
 *  As duas condições, e não uma: em absoluto, `nubank` com 24 acertos
 *  legítimos num período de 500 lançamentos gritaria igual a `pagamento` com
 *  87; em proporção pura, 3 de 20 gritaria num mês quase vazio. A comparação
 *  é em inteiros (`candidatos × 10 >= universo`) para não depender de ponto
 *  flutuante na fronteira. Período sem lançamento não chama atenção de nada:
 *  não há o que converter. */
export function chamaAtencao(candidatos: number, periodTransactions: number): boolean {
  if (periodTransactions <= 0) return false
  return candidatos >= ATENCAO_MINIMO && candidatos * 10 >= periodTransactions
}

/** Uma palavra de conta que ENTRA, com o impacto medido dela — uma linha do
 *  bloco B. */
export type PalavraDeConta = {
  chave: string
  palavra: string
  candidatos: number
  atencao: boolean
}

/** Um grupo do bloco B: a conta e as palavras dela, impacto decrescente. */
export type GrupoDeConta = {
  chave: string
  /** `Nubank · 3` — o nome do servidor e quantas palavras entram. */
  rotulo: string
  palavras: PalavraDeConta[]
}

/** O bloco B a partir do relatório: um grupo por entrada de conta que tem
 *  palavra a gravar, na ordem do JSON; dentro do grupo, **uma linha por
 *  palavra** (`impact.byKeyword`, que o servidor devolve na ordem de `added`)
 *  em **impacto decrescente** — a palavra genérica é a primeira que o olho
 *  encontra, e é ela, não a conta, que chama atenção. Ordenação estável:
 *  empate mantém a ordem do JSON.
 *
 *  O total do item (`impact.transferCandidates`, a união) **não** aparece: com
 *  uma palavra ele repete o número da linha, com várias é um segundo número
 *  que não aponta o problema — e a ação da pessoa é sempre sobre UMA palavra
 *  (apagá-la do JSON). Entrada sem `impact` (o confirm não o traz) não
 *  produz grupo; o bloco B só existe na prévia.
 *
 *  A chave é o ÍNDICE do item, nunca `id`: o contrato devolve `""` quando o
 *  id do JSON não tinha forma de uuid, e duas entradas assim colidiriam. */
export function gruposDeConta(relatorio: KeywordImportReport): GrupoDeConta[] {
  const universo = relatorio.totals.periodTransactions
  const grupos: GrupoDeConta[] = []
  relatorio.items.forEach((item, indice) => {
    if (item.type !== 'account' || item.added.length === 0 || !item.impact) return
    const palavras = item.impact.byKeyword
      .map((medida, posicao) => ({
        chave: `${indice}-${posicao}-${medida.keyword}`,
        palavra: medida.keyword,
        candidatos: medida.transferCandidates,
        atencao: chamaAtencao(medida.transferCandidates, universo),
      }))
      .sort((a, b) => b.candidatos - a.candidatos)
    if (palavras.length === 0) return
    grupos.push({
      chave: `conta-${indice}`,
      rotulo: `${item.name ?? '—'} · ${palavras.length}`,
      palavras,
    })
  })
  return grupos
}

/** As categorias que já existiam e recebem palavra — o bloco C. Com o índice
 *  no relatório, que é a chave estável (o `id` pode vir `""`). */
export function entradasDeCategoria(
  relatorio: KeywordImportReport,
): Array<{ indice: number; item: KeywordImportItem }> {
  return relatorio.items
    .map((item, indice) => ({ indice, item }))
    .filter(({ item }) => item.type === 'category' && item.added.length > 0)
}

/** As categorias que **nascem** — o bloco A. Só `created`: `merged_into_existing`
 *  não é criação (vai para o bloco C com a nota), e os demais desfechos são
 *  recusa (vão para o que fica de fora). */
export function categoriasACriar(relatorio: KeywordImportReport): KeywordImportNewCategory[] {
  return relatorio.newCategories.filter((entrada) => entrada.outcome === 'created')
}

/** As entradas de `newCategories` que já existiam ativas: as palavras vão para
 *  a categoria existente. Aparecem no bloco C, nunca no A. */
export function categoriasMescladas(relatorio: KeywordImportReport): KeywordImportNewCategory[] {
  return relatorio.newCategories.filter(
    (entrada) => entrada.outcome === 'merged_into_existing' && entrada.add.length > 0,
  )
}

/** A partir de quantas categorias novas o apoio do bloco A avisa que a IA
 *  costuma criar demais (§4.3: "a tela destaca a contagem quando o lote
 *  propõe muita estrutura nova"). */
export const MUITAS_CATEGORIAS = 5

// ------------------------------------------------------- o que fica de fora

/** Uma linha do `<details>` "Ver o que não entra": a palavra (ou o caminho da
 *  categoria nova), o item a que ela ia, e o motivo já em pt-BR. */
export type LinhaDeFora = {
  chave: string
  /** `«palavra»` ou, para uma categoria nova recusada, `Grupo > Folha`. */
  palavra: string
  /** O nome do item, vindo do SERVIDOR — `null` quando ele não foi resolvido
   *  na casa (`item_not_found`), e aí a tela mostra um travessão. */
  item: string | null
  motivo: string
}

export type ForaDaPrevia = {
  jaEstavam: LinhaDeFora[]
  recusadas: LinhaDeFora[]
}

/** Separa tudo o que **não entra** em dois grupos: o que já estava lá (pulado,
 *  inofensivo) e o que foi recusado (com motivo). Uma categoria nova recusada
 *  vira uma linha própria com o caminho; as palavras recusadas de qualquer
 *  entrada viram uma linha cada. `skipped_by_user` não aparece: na prévia ele
 *  não existe, e no relatório final ele tem uma linha própria na `<dl>`. */
export function oQueFicaDeFora(
  relatorio: KeywordImportReport,
  resolverDono: (
    recusa: KeywordImportRejectedKeyword,
    tipo: 'category' | 'account',
  ) => string | undefined,
): ForaDaPrevia {
  const jaEstavam: LinhaDeFora[] = []
  const recusadas: LinhaDeFora[] = []

  relatorio.items.forEach((item, indice) => {
    for (const pulada of item.skipped) {
      jaEstavam.push({
        chave: `item-${indice}-pulada-${pulada.keyword}`,
        palavra: citarPalavra(pulada.keyword),
        item: item.name,
        motivo: MOTIVO_DO_PULO[pulada.reason],
      })
    }
    for (const recusa of item.rejected) {
      recusadas.push({
        chave: `item-${indice}-recusada-${recusa.keyword}`,
        palavra: citarPalavra(recusa.keyword),
        item: item.name,
        motivo: motivoDaRecusa(recusa, item.type, resolverDono(recusa, item.type)),
      })
    }
  })

  relatorio.newCategories.forEach((entrada, indice) => {
    const caminho = `${entrada.group} > ${entrada.name}`
    const recusada =
      entrada.outcome !== 'created' &&
      entrada.outcome !== 'merged_into_existing' &&
      entrada.outcome !== 'skipped_by_user'
    if (recusada) {
      recusadas.push({
        chave: `nova-${indice}`,
        palavra: caminho,
        item: 'categoria nova',
        motivo: MOTIVO_DO_DESFECHO[entrada.outcome],
      })
    }
    for (const pulada of entrada.skipped) {
      jaEstavam.push({
        chave: `nova-${indice}-pulada-${pulada.keyword}`,
        palavra: citarPalavra(pulada.keyword),
        item: caminho,
        motivo: MOTIVO_DO_PULO[pulada.reason],
      })
    }
    for (const recusa of entrada.rejected) {
      recusadas.push({
        chave: `nova-${indice}-recusada-${recusa.keyword}`,
        palavra: citarPalavra(recusa.keyword),
        item: caminho,
        motivo: motivoDaRecusa(recusa, 'category', resolverDono(recusa, 'category')),
      })
    }
  })

  return { jaEstavam, recusadas }
}

// ---------------------------------------------------- contagens da seleção

/** O que vai ser aplicado depois do que a pessoa desmarcou. */
export type Aplicavel = {
  categorias: number
  palavras: number
  /** Grupos que nascem junto — contados uma vez, mesmo com duas folhas novas
   *  no mesmo grupo. */
  gruposNovos: number
}

/** O grupo de um `ref` (`alimentacao > padaria` → `alimentacao`): é a forma
 *  normalizada pelo servidor, então duas folhas de "Saúde" e "saude" contam
 *  o mesmo grupo. */
function grupoDoRef(ref: string): string {
  const separador = ref.indexOf(' > ')
  return separador === -1 ? ref : ref.slice(0, separador)
}

/** Recalcula as contagens a partir dos `totals` do servidor e do que foi
 *  desmarcado. Desmarcar uma categoria de 2 palavras faz `palavras` cair 2 —
 *  é a prova, no rótulo do botão, de que desmarcar leva as palavras junto
 *  (aceite 48). Nunca negativo: se o servidor contou diferente, o piso é
 *  zero e o botão diz "Nada para aplicar". */
export function contarAplicavel(
  relatorio: KeywordImportReport,
  desmarcadas: ReadonlySet<string>,
): Aplicavel {
  const criadas = categoriasACriar(relatorio)
  let categorias = relatorio.totals.categoriesCreated
  let palavras = relatorio.totals.added
  const grupos = new Set<string>()

  for (const entrada of criadas) {
    if (desmarcadas.has(entrada.ref)) {
      categorias -= 1
      palavras -= entrada.add.length
      continue
    }
    if (entrada.groupIsNew) grupos.add(grupoDoRef(entrada.ref))
  }

  return {
    categorias: Math.max(0, categorias),
    palavras: Math.max(0, palavras),
    gruposNovos: grupos.size,
  }
}

function plural(quantidade: number, um: string, varios: string): string {
  return `${quantidade} ${quantidade === 1 ? um : varios}`
}

/** `3 categorias novas · 18 palavras entram · 6 já estavam lá · 3 recusadas.`
 *
 *  `skipped` e `rejected` são os números do servidor como vieram; categorias e
 *  palavras já descontam o que a pessoa desmarcou — a frase é `role="status"`
 *  e ATUALIZA ao desmarcar (trade-off assumido: número parado seria mentira).
 *  Segmento zerado não aparece, exceto o das palavras, que é o assunto da
 *  frase: "nenhuma palavra entra" é informação. */
export function fraseDosTotais(totals: KeywordImportTotals, aplicavel: Aplicavel): string {
  const partes: string[] = []
  if (aplicavel.categorias > 0) {
    partes.push(plural(aplicavel.categorias, 'categoria nova', 'categorias novas'))
  }
  partes.push(
    aplicavel.palavras === 0
      ? 'nenhuma palavra entra'
      : plural(aplicavel.palavras, 'palavra entra', 'palavras entram'),
  )
  if (totals.skipped > 0)
    partes.push(`${totals.skipped} já ${totals.skipped === 1 ? 'estava' : 'estavam'} lá`)
  if (totals.rejected > 0) partes.push(plural(totals.rejected, 'recusada', 'recusadas'))
  return `${partes.join(' · ')}.`
}

/** O rótulo do botão de confirmar diz a SAÍDA, e recalcula ao desmarcar:
 *  `Criar 3 categorias e gravar 18 palavras` · `Gravar 18 palavras` ·
 *  `Criar 2 categorias` · `Nada para aplicar`. */
export function rotuloDoConfirmar(aplicavel: Aplicavel): string {
  const { categorias, palavras } = aplicavel
  if (categorias === 0 && palavras === 0) return 'Nada para aplicar'
  const criar = categorias > 0 ? `Criar ${plural(categorias, 'categoria', 'categorias')}` : ''
  const gravar = palavras > 0 ? `${plural(palavras, 'palavra', 'palavras')}` : ''
  if (criar && gravar) return `${criar} e gravar ${gravar}`
  if (criar) return criar
  return `Gravar ${gravar}`
}

/** `Inclui 2 grupos novos.` — ou vazio, quando nenhum grupo nasce. */
export function fraseDosGruposNovos(aplicavel: Aplicavel): string {
  if (aplicavel.gruposNovos === 0) return ''
  return `Inclui ${plural(aplicavel.gruposNovos, 'grupo novo', 'grupos novos')}.`
}

/** `true` quando a prévia não tem nada para aplicar mesmo antes de a pessoa
 *  desmarcar qualquer coisa (aceite 51). */
export function previaVazia(totals: KeywordImportTotals): boolean {
  return totals.categoriesCreated === 0 && totals.added === 0
}

// ------------------------------------------------------- relatório final

/** `2 categorias criadas e 16 palavras gravadas.` — com os números do
 *  **confirm**, nunca os da prévia: entre uma e outra o estado pode ter mudado
 *  e o servidor revalidou tudo. `Nada foi gravado.` quando nada entrou. */
export function fraseDoResultado(totals: KeywordImportTotals): string {
  const criadas =
    totals.categoriesCreated > 0
      ? plural(totals.categoriesCreated, 'categoria criada', 'categorias criadas')
      : ''
  const gravadas =
    totals.added > 0 ? plural(totals.added, 'palavra gravada', 'palavras gravadas') : ''
  if (criadas && gravadas) return `${criadas} e ${gravadas}.`
  if (criadas) return `${criadas}.`
  if (gravadas) return `${gravadas}.`
  return 'Nada foi gravado.'
}

/** Quantas categorias a pessoa desmarcou, contadas pelo servidor no confirm
 *  (`skipped_by_user`), e não pelo tamanho do conjunto local. */
export function desmarcadasPeloServidor(relatorio: KeywordImportReport): number {
  return relatorio.newCategories.filter((entrada) => entrada.outcome === 'skipped_by_user').length
}

/** O nome acessível da caixa de uma categoria a criar: o caminho inteiro e a
 *  contagem — `Criar Alimentação > Padaria com 2 palavras-chave`. Nunca só
 *  "Criar": sete caixas com o mesmo nome não dizem nada a quem navega por
 *  lista de controles. */
export function rotuloDaCaixa(entrada: KeywordImportNewCategory): string {
  const quantas = entrada.add.length
  const palavras =
    quantas === 0
      ? 'sem palavra-chave'
      : `com ${plural(quantas, 'palavra-chave', 'palavras-chave')}`
  return `Criar ${entrada.group} > ${entrada.name} ${palavras}`
}

/** As palavras citadas, uma atrás da outra: `«padaria» · «panificadora»`. É
 *  texto citado, não ficha — ficha é a linguagem de editar, e aqui nada se
 *  edita. */
export function citarPalavras(palavras: readonly string[]): string {
  return palavras.map(citarPalavra).join(' · ')
}
