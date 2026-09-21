import { ApiError } from '@/api/client'
import type {
  AutoCategorizeResult,
  TransferDetectResult,
  TransferDetectUnpairedReason,
} from '@/api/types'
import { isConflict, isLoteGrandeDemais, messageForError } from '@/lib/errors'
import { nomeDoMes } from '@/lib/month'
import { capitalizar, enumerarMeses } from './janela'

/** A lógica da seção **Reprocessar** de `/ia` (spec 0010 §5), sem JSX — para
 *  os testes afirmarem cada regra sem montar a tela.
 *
 *  Quatro coisas moram aqui, e nenhuma é dinheiro:
 *
 *  1. **A prévia consolidada**: as seis respostas de `dryRun` somadas. Somar
 *     no cliente é legítimo aqui — são contagens de linhas afetadas, cada uma
 *     completa e autoritativa na sua resposta, e a regra de "somar num lugar
 *     só" fala de centavos (ADR-036(e), spec 0010 §10.5).
 *  2. **O plano de execução em DUAS FASES**: a detecção de transferências em
 *     todos os meses, e só depois a categorização em todos os meses. Ver
 *     `planoDeExecucao` — a ordem é a única decisão desta fatia que refina a
 *     spec, e o motivo está lá.
 *  3. **A execução sequencial**, `await` a `await`, que **para no primeiro
 *     erro** e nunca faz uma chamada depois dele (§5.2, item 4).
 *  4. **O léxico**: os estados por palavra, as frases do progresso e a frase
 *     que diz exatamente o que ficou feito e o que não quando a execução para. */

// ------------------------------------------------------------ prévia

/** As duas prévias de um mês, como o servidor as devolveu. */
export type PreviaDoMes = {
  mes: string
  transferencias: TransferDetectResult
  categorizacao: AutoCategorizeResult
}

/** As prévias de todos os meses e os quatro totais somados. */
export type PreviaConsolidada = {
  meses: readonly PreviaDoMes[]
  /** Pares que virariam transferência. */
  pares: number
  /** Candidatas sem a outra perna gravada — ficam como estão. */
  semPar: number
  /** Lançamentos que receberiam categoria. */
  categorizados: number
  /** Lançamentos sem categoria que continuariam sem. */
  semCategoria: number
}

/** Soma as prévias, número a número (aceite 37). É uma soma de inteiros e
 *  nada mais: nenhuma prévia é reinterpretada, nenhuma lista é cruzada. */
export function consolidarPrevia(meses: readonly PreviaDoMes[]): PreviaConsolidada {
  let pares = 0
  let semPar = 0
  let categorizados = 0
  let semCategoria = 0
  for (const previa of meses) {
    pares += previa.transferencias.paired
    semPar += previa.transferencias.unpaired
    categorizados += previa.categorizacao.categorized
    semCategoria += previa.categorizacao.unmatched
  }
  return { meses, pares, semPar, categorizados, semCategoria }
}

/** `true` quando não há o que executar: nenhum par e nenhuma categoria a
 *  gravar. Os sem par e os sem categoria não contam — eles são justamente o
 *  que o reprocessamento **não** muda. */
export function nadaAReprocessar(previa: Pick<PreviaConsolidada, 'pares' | 'categorizados'>) {
  return previa.pares === 0 && previa.categorizados === 0
}

/** `3 pares de transferência · 42 lançamentos categorizados · 12 seguem sem
 *  categoria.` — a frase consolidada da prévia, uma linha, `tabular-nums`.
 *
 *  Zero em tudo vira a frase de "nada a reprocessar", e ela é honesta sobre
 *  os que continuam sem categoria: dizer "nem lançamento sem categoria"
 *  quando há 12 seria mentira numérica. */
export function fraseDaPrevia(previa: PreviaConsolidada): string {
  if (nadaAReprocessar(previa)) {
    if (previa.semCategoria === 0) {
      return 'Nada a reprocessar — não há par para reconhecer nem lançamento sem categoria.'
    }
    return `Nada a reprocessar — não há par para reconhecer, e nenhuma palavra-chave bate com ${plural(
      previa.semCategoria,
      'o lançamento sem categoria',
      'os {n} lançamentos sem categoria',
    )}.`
  }
  const partes = [
    plural(previa.pares, '1 par de transferência', '{n} pares de transferência'),
    plural(previa.categorizados, '1 lançamento categorizado', '{n} lançamentos categorizados'),
    plural(previa.semCategoria, '1 segue sem categoria', '{n} seguem sem categoria'),
  ]
  return `${partes.join(' · ')}.`
}

// ------------------------------------------------------- plano e execução

export type Etapa = 'transferencias' | 'categorizacao'

/** Um passo do plano: uma chamada, um mês, uma etapa. */
export type Passo = { mes: string; etapa: Etapa }

/** O plano de execução — **duas fases, cada uma mês a mês.**
 *
 *  A spec 0010 §5.1 fixa a ordem *transferências → categorização*, porque
 *  `POST /transfers/detect` zera o `categoryId` das duas pernas ao converter
 *  um par (ADR-028): categorizar antes seria escrever no que a etapa seguinte
 *  esvazia. A §5.2 diz "mês a mês, cada mês na ordem" — que, lido ao pé da
 *  letra, seria detectar julho, categorizar julho, detectar agosto…
 *
 *  Só que a detecção procura o espelho a **±3 dias** (`occurredOn` da outra
 *  perna), e três dias cruzam a fronteira do mês. Categorizada julho, a
 *  detecção de agosto pode formar um par com uma perna em 31/07 — e zerar a
 *  categoria de julho **depois** de ela ter sido reportada como gravada. O
 *  resultado final estaria certo; o relatório de julho, não.
 *
 *  Fase 1 = detecção em **todos** os meses; fase 2 = categorização em todos. É
 *  a leitura fiel ao princípio da §5.1 — nenhuma categorização acontece antes
 *  de **toda** conversão — e elimina a borda. Registrado em docs/DESIGN.md,
 *  spec 0010 (h). */
export function planoDeExecucao(meses: readonly string[]): Passo[] {
  return [
    ...meses.map((mes): Passo => ({ mes, etapa: 'transferencias' })),
    ...meses.map((mes): Passo => ({ mes, etapa: 'categorizacao' })),
  ]
}

/** As duas chamadas, injetadas: a tela passa as funções de `api/`, os testes
 *  passam espiões que registram a ORDEM. */
export type Chamadas = {
  detectar: (mes: string, dryRun: boolean) => Promise<TransferDetectResult>
  categorizar: (mes: string, dryRun: boolean) => Promise<AutoCategorizeResult>
}

/** A prévia das duas etapas em todos os meses — até seis chamadas, **em
 *  paralelo**: `dryRun` não escreve, então nem a ordem entre fases nem a ordem
 *  entre meses importa aqui. Qualquer falha derruba a prévia inteira: uma
 *  prévia de cinco sextos seria um consolidado que mente. */
export async function conferirTudo(
  meses: readonly string[],
  chamadas: Chamadas,
): Promise<PreviaConsolidada> {
  const previas = await Promise.all(
    meses.map(async (mes): Promise<PreviaDoMes> => {
      const [transferencias, categorizacao] = await Promise.all([
        chamadas.detectar(mes, true),
        chamadas.categorizar(mes, true),
      ])
      return { mes, transferencias, categorizacao }
    }),
  )
  return consolidarPrevia(previas)
}

/** Os números **das respostas de execução** — nunca os da prévia. */
export type TotalDaExecucao = { pares: number; categorizados: number }

export type PassoConcluido = { indice: number; passo: Passo; quantidade: number }

export type DesfechoDaExecucao =
  | { ok: true; total: TotalDaExecucao }
  | { ok: false; indiceDaFalha: number; erro: unknown; total: TotalDaExecucao }

/** Executa o plano, **estritamente em sequência** e com `dryRun: false`.
 *
 *  Cada chamada é a rota existente, com a própria transação e o próprio
 *  registro de auditoria. A primeira falha — 409, 429, rede, o que for — para
 *  tudo: nenhuma chamada sai depois dela (aceite 44). O que já foi aplicado
 *  fica aplicado (cada rota é uma transação), e `indiceDaFalha` diz até onde
 *  se chegou; a tela transforma isso na frase do que ficou feito. */
export async function executarPlano(
  plano: readonly Passo[],
  chamadas: Chamadas,
  aoConcluir: (concluido: PassoConcluido) => void,
): Promise<DesfechoDaExecucao> {
  const total: TotalDaExecucao = { pares: 0, categorizados: 0 }
  for (const [indice, passo] of plano.entries()) {
    let quantidade: number
    try {
      if (passo.etapa === 'transferencias') {
        quantidade = (await chamadas.detectar(passo.mes, false)).paired
        total.pares += quantidade
      } else {
        quantidade = (await chamadas.categorizar(passo.mes, false)).categorized
        total.categorizados += quantidade
      }
    } catch (erro) {
      return { ok: false, indiceDaFalha: indice, erro, total }
    }
    aoConcluir({ indice, passo, quantidade })
  }
  return { ok: true, total }
}

// ------------------------------------------------------ estados por palavra

/** O estado de um passo, dito por PALAVRA na célula — nunca por cor. */
export type EstadoDoPasso =
  | 'na_fila'
  | 'em_andamento'
  | 'feito'
  | 'nao_aplicado'
  | 'sem_resposta'
  | 'nao_chegou_a_rodar'

/** `Record` exaustivo: um estado novo aqui é erro de compilação na tela. */
export const PALAVRA_DO_ESTADO: Record<EstadoDoPasso, string> = {
  na_fila: 'na fila',
  em_andamento: 'em andamento…',
  feito: 'feito',
  nao_aplicado: 'não aplicado',
  sem_resposta: 'sem resposta',
  nao_chegou_a_rodar: 'não chegou a rodar',
}

/** O que se sabe sobre o passo que falhou.
 *
 *  `nao_aplicado` quando o servidor **respondeu** com erro (409, 429, 422,
 *  500): as duas rotas são uma transação, e uma resposta de erro é uma
 *  transação desfeita — nada foi gravado. `sem_resposta` quando não houve
 *  resposta (rede caiu no meio) ou ela não é interpretável: o pedido pode ter
 *  chegado e sido aplicado, e afirmar "não aplicado" seria chutar. A prévia
 *  nova resolve a dúvida — o que foi aplicado volta com 0. */
export type TipoDaFalha = 'nao_aplicado' | 'sem_resposta'

export type Falha = { indice: number; tipo: TipoDaFalha }

export function tipoDaFalha(erro: unknown): TipoDaFalha {
  return erro instanceof ApiError ? 'nao_aplicado' : 'sem_resposta'
}

/** Os estados dos `total` passos do plano, dado quantos foram `feitos` e, se
 *  a execução parou, onde e como.
 *
 *  Sem falha: os feitos, um `em_andamento` e o resto `na_fila`. Com falha: os
 *  feitos, o que falhou com a palavra da falha e o resto `nao_chegou_a_rodar`
 *  — "na fila" seria promessa de algo que não vai acontecer. */
export function estadosDosPassos(
  total: number,
  feitos: number,
  falha: Falha | null,
): EstadoDoPasso[] {
  return Array.from({ length: total }, (_, indice): EstadoDoPasso => {
    if (indice < feitos) return 'feito'
    if (falha === null) return indice === feitos ? 'em_andamento' : 'na_fila'
    return indice === falha.indice ? falha.tipo : 'nao_chegou_a_rodar'
  })
}

// ----------------------------------------------------------------- copy

/** `Julho: 1 par.` · `Julho: 12 categorizados.` — a frase curta de cada
 *  chamada concluída, para o `<output>` do progresso. O número é o que a
 *  resposta de execução devolveu. */
export function fraseDoPasso(passo: Passo, quantidade: number): string {
  const mes = capitalizar(nomeDoMes(passo.mes))
  if (passo.etapa === 'transferencias') {
    return `${mes}: ${plural(quantidade, '1 par', '{n} pares')}.`
  }
  return `${mes}: ${plural(quantidade, '1 categorizado', '{n} categorizados')}.`
}

/** A frase final, com os números das respostas de execução. Zero em tudo é o
 *  caso idempotente (§5.6): rodar de novo sem novidade não é falha. */
export function fraseDoResultado(total: TotalDaExecucao): string {
  if (total.pares === 0 && total.categorizados === 0) {
    return 'Nada mudou — não havia par para reconhecer nem lançamento sem categoria.'
  }
  const partes: string[] = []
  if (total.pares > 0) {
    partes.push(plural(total.pares, '1 par de transferência', '{n} pares de transferência'))
  }
  if (total.categorizados > 0) {
    partes.push(
      plural(total.categorizados, '1 lançamento categorizado', '{n} lançamentos categorizados'),
    )
  }
  return `Reprocessado — ${partes.join(' e ')}.`
}

/** O que ficou feito e o que não, quando a execução parou — dito com todas as
 *  letras, mês a mês, etapa a etapa. É o corpo do `Alert` (§5.2, item 4).
 *
 *  Exemplo, 409 na categorização de agosto numa janela de três meses:
 *  `As transferências de julho, agosto e setembro foram aplicadas e continuam
 *  aplicadas. A categorização de julho também. A de agosto não foi, e a de
 *  setembro não chegou a rodar. Confira de novo antes de seguir.` */
export function fraseDoQueFicou(meses: readonly string[], falha: Falha): string {
  const n = meses.length
  const faseDaFalha: 1 | 2 = falha.indice < n ? 1 : 2
  const posicao = falha.indice % n
  const frases: string[] = []

  if (faseDaFalha === 1) {
    const feitos = meses.slice(0, posicao)
    const falhou = meses[posicao] ?? ''
    const naoRodaram = meses.slice(posicao + 1)
    const verbo = falha.tipo === 'sem_resposta' ? 'ficaram sem resposta' : 'não foram aplicadas'
    if (feitos.length > 0) {
      frases.push(
        `As transferências de ${enumerarMeses(feitos)} foram aplicadas e continuam aplicadas.`,
      )
      frases.push(`As de ${nomeDoMes(falhou)} ${verbo}${caudaDosQueNaoRodaram(naoRodaram, 'as')}`)
    } else {
      frases.push(
        `As transferências de ${nomeDoMes(falhou)} ${verbo}${caudaDosQueNaoRodaram(naoRodaram, 'as')}`,
      )
    }
    frases.push('A categorização não chegou a rodar.')
  } else {
    frases.push(
      `As transferências de ${enumerarMeses(meses)} foram aplicadas e continuam aplicadas.`,
    )
    const feitos = meses.slice(0, posicao)
    const falhou = meses[posicao] ?? ''
    const naoRodaram = meses.slice(posicao + 1)
    const verbo = falha.tipo === 'sem_resposta' ? 'ficou sem resposta' : 'não foi aplicada'
    if (feitos.length > 0) {
      frases.push(`A categorização de ${enumerarMeses(feitos)} também.`)
      const curto = falha.tipo === 'sem_resposta' ? 'ficou sem resposta' : 'não foi'
      frases.push(`A de ${nomeDoMes(falhou)} ${curto}${caudaDosQueNaoRodaram(naoRodaram, 'a')}`)
    } else {
      frases.push(
        `A categorização de ${nomeDoMes(falhou)} ${verbo}${caudaDosQueNaoRodaram(naoRodaram, 'a')}`,
      )
    }
  }
  frases.push('Confira de novo antes de seguir.')
  return frases.join(' ')
}

/** `, e a de setembro não chegou a rodar.` · `, e as de agosto e setembro não
 *  chegaram a rodar.` · `.` quando não sobrou ninguém. `artigo` é o do
 *  substantivo elidido: "as (transferências)" ou "a (categorização)". */
function caudaDosQueNaoRodaram(meses: readonly string[], artigo: 'a' | 'as'): string {
  if (meses.length === 0) return '.'
  const plural = artigo === 'as' || meses.length > 1
  return `, e ${plural ? 'as' : 'a'} de ${enumerarMeses(meses)} ${
    plural ? 'não chegaram' : 'não chegou'
  } a rodar.`
}

/** A frase própria do 429, com a ação certa: esperar. Repetir na hora daria
 *  outro 429 — o balde repõe um token por minuto. */
export const MSG_MUITAS_OPERACOES = 'Muitas operações seguidas. Espere um minuto e confira de novo.'

/** O 422 em `fields.month`: o mês tem mais candidatas do que a rota aceita
 *  (tudo ou nada, nunca execução parcial). Repetir daria o mesmo 422; a saída
 *  é a janela do topo. */
export const MSG_MES_GRANDE =
  'Um dos meses tem lançamentos demais para reprocessar de uma vez. Escolha um período menor no alto da página e confira de novo.'

/** Como a tela lê uma falha, da prévia ou da execução. `conflito` é o 409
 *  (o estado mudou entre a prévia e a gravação); `limite` é o 429 e o 422
 *  de mês grande, cuja ação NÃO é "tentar de novo"; `outro` é o resto, com a
 *  frase única do app. */
export type ErroLido = {
  tipo: 'conflito' | 'limite' | 'outro'
  mensagem: string
}

export function lerErro(erro: unknown): ErroLido {
  if (isConflict(erro)) return { tipo: 'conflito', mensagem: messageForError(erro) }
  if (erro instanceof ApiError && erro.status === 429) {
    return { tipo: 'limite', mensagem: MSG_MUITAS_OPERACOES }
  }
  if (isLoteGrandeDemais(erro)) return { tipo: 'limite', mensagem: MSG_MES_GRANDE }
  // Rede, 500, sessão, eco divergente: a frase única do app, e a ação é
  // conferir de novo — que é o que resolve também a dúvida do `sem_resposta`.
  return { tipo: 'outro', mensagem: messageForError(erro) }
}

/** A PALAVRA de cada motivo de candidata sem par — a mesma do diálogo de
 *  `/transferencias`. `Record` exaustivo: um motivo novo no contrato vira erro
 *  de compilação aqui, não "no_mirror" cru na tela. */
export const PALAVRA_DO_MOTIVO: Record<TransferDetectUnpairedReason, string> = {
  no_mirror: 'sem a outra perna gravada',
}

/** A explicação dos sem par, uma vez, acima da lista — a mesma copy do
 *  diálogo de `/transferencias`. O sistema **nunca inventa a outra perna**. */
export const DESCRICAO_DOS_SEM_PAR =
  'A descrição bate com uma palavra-chave de conta, mas a outra perna não está gravada em nenhuma conta da casa. Importe o extrato da outra conta e reprocesse.'

/** `1 par` / `3 pares` — `{n}` no plural é trocado pelo número. */
function plural(n: number, singular: string, pluralComN: string): string {
  return n === 1 ? singular : pluralComN.replace('{n}', String(n))
}
