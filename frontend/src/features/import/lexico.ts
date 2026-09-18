import type {
  ImportDecisionAction,
  ImportRejectReason,
  ImportRow,
  ImportRowStatus,
} from '@/api/types'
import { dataCurta } from '@/lib/civil'
import { citarPalavra } from '@/lib/keywords'

/** O léxico da conciliação — **palavra, nunca cor**.
 *
 *  Este arquivo é a razão de a revisão sobreviver ao teste do `grayscale(1)`.
 *  Nenhum estado de conciliação tem cor cromática no app: a distinção vem de
 *  quatro portadores, e três deles são texto que mora aqui.
 *
 *  1. **posição** — em qual dos quatro blocos a linha está (`BLOCO_DO_STATUS`);
 *  2. **palavra do grupo** — o cabeçalho explica o motivo uma vez, para o grupo
 *     inteiro (`TITULO_DO_GRUPO` + `DESCRICAO_DO_GRUPO`);
 *  3. **frase de evidência na linha** — o "por quê" daquela linha
 *     (`evidenciaDaLinha`);
 *  4. **forma do controle** — `<select>` com palavras, checkbox, ou controle
 *     nenhum. Esse é decidido pelo bloco, na tela.
 *
 *  A jogada central: o status **não vira etiqueta**. Ele vira o texto das
 *  opções do `<select>` de decisão (`rotuloDaAcao`), que é onde a pessoa lê o
 *  que vai acontecer no momento exato de decidir. A pontuação da correspondência
 *  segue a mesma regra: é **texto** (`88%`), em `tabular-nums`, nunca barra,
 *  anel ou faixa de cor.
 *
 *  O que este arquivo **não** faz: reimplementar a tabela de defaults. Quem
 *  manda no que acontece com uma linha é o servidor, por `defaultAction` e
 *  `allowedActions`. Uma cópia dessa tabela aqui divergiria no primeiro ajuste
 *  do backend, e a divergência seria silenciosa — a tela prometendo uma coisa e
 *  o commit fazendo outra. */

/** Os quatro blocos de trabalho. A tela se organiza por **trabalho a fazer**,
 *  não por status do dado: nove status não são nove coisas, são quatro
 *  situações — perguntas abertas, perguntas com resposta proposta, conferência
 *  e auditoria. */
export type BlocoDaRevisao = 'decisao' | 'transferencias' | 'prontas' | 'fora'

export const BLOCO_DO_STATUS: Record<ImportRowStatus, BlocoDaRevisao> = {
  pagamento_de_fatura: 'decisao',
  possivel_duplicado: 'decisao',
  duplicado_excluido: 'decisao',
  transferencia_interna: 'transferencias',
  transferencia_ja_registrada: 'transferencias',
  novo: 'prontas',
  repetido_no_arquivo: 'prontas',
  duplicado_exato: 'fora',
  rejeitado: 'fora',
}

/** Ordem dos grupos dentro de cada bloco. Decisão primeiro pelo que custa mais
 *  caro errar: pagamento de fatura contado duas vezes estraga o mês inteiro.
 *  Nas transferências, a que ainda espera resposta vem antes da que já resolve
 *  sozinha. */
export const ORDEM_DOS_STATUS: readonly ImportRowStatus[] = [
  'pagamento_de_fatura',
  'possivel_duplicado',
  'duplicado_excluido',
  'transferencia_interna',
  'transferencia_ja_registrada',
  'novo',
  'repetido_no_arquivo',
  'duplicado_exato',
  'rejeitado',
]

/** A palavra do léxico fixo (docs/DESIGN.md). `novo` não tem palavra: a
 *  AUSÊNCIA é a informação, e escrever "Novo" 59 vezes seria ruído. */
export const PALAVRA_DO_STATUS: Record<ImportRowStatus, string> = {
  novo: '',
  repetido_no_arquivo: '2ª ocorrência',
  duplicado_exato: 'Já importada',
  duplicado_excluido: 'Já importada e excluída',
  possivel_duplicado: 'Possível duplicata',
  pagamento_de_fatura: 'Pagamento de fatura',
  transferencia_interna: 'Parece transferência',
  transferencia_ja_registrada: 'Já registrada como transferência',
  rejeitado: 'Linha inválida',
}

/** O "por quê" do grupo, escrito uma vez para todas as linhas dele.
 *
 *  Só os status dos blocos de decisão e de transferências têm parágrafo: são
 *  os únicos em que a pessoa precisa entender a consequência antes de escolher. */
export const DESCRICAO_DO_GRUPO: Partial<Record<ImportRowStatus, string>> = {
  pagamento_de_fatura:
    'Dinheiro que sai da conta para abater a fatura do cartão. Se entrar como despesa, o gasto é contado duas vezes: uma na compra, outra no pagamento.',
  possivel_duplicado:
    'Mesmo valor e data a até 3 dias de um lançamento que já existe, com descrição diferente.',
  duplicado_excluido:
    'Este lançamento já existiu e você excluiu. Incluir aqui restaura o original, em vez de criar outro.',
  transferencia_interna:
    'Não entra sem você confirmar. Se não for transferência, importe como despesa ou receita comum.',
  transferencia_ja_registrada:
    'A outra conta já registrou este par. Vincular não cria lançamento: só marca esta linha como importada, para ela não voltar como nova.',
}

/** "Possível duplicata · 4" */
export function tituloDoGrupo(status: ImportRowStatus, quantas: number): string {
  const palavra = PALAVRA_DO_STATUS[status] || 'Sem pendência'
  return `${palavra} · ${quantas}`
}

/** Motivo da rejeição, em pt-BR.
 *
 *  `Record` exaustivo de propósito: motivo novo no backend vira erro de
 *  compilação aqui, em vez de a tela mostrar `invalid_sign` cru para o usuário.
 *  O servidor manda **código**, nunca texto — a linha crua do arquivo não é
 *  guardada nem devolvida. */
export const MOTIVO_DA_REJEICAO: Record<ImportRejectReason, string> = {
  invalid_date: 'a data não pôde ser lida',
  invalid_amount: 'o valor não pôde ser lido',
  invalid_sign: 'não dá para saber se é entrada ou saída',
  invalid_columns: 'as colunas não batem com o formato do arquivo',
  empty_row: 'a linha está vazia',
}

/** A pontuação como TEXTO — `88%` — ou vazio quando é 100.
 *
 *  100 nunca aparece: ausência de pontuação significa correspondência exata,
 *  e "100% · «padaria»" seria o app se gabando de ter achado a palavra
 *  inteira. */
export function pontuacaoVisivel(score: number | null): string {
  if (score === null || score >= 100) return ''
  return `${Math.trunc(score)}%`
}

/** A linha de proveniência de uma sugestão: `88% · «supermercado»`; com 100,
 *  só `«supermercado»`. Vazia quando a linha não traz palavra nenhuma — não há
 *  o que dizer sobre a origem. */
export function proveniencia(linha: Pick<ImportRow, 'matchScore' | 'matchedKeyword'>): string {
  if (!linha.matchedKeyword) return ''
  const pontuacao = pontuacaoVisivel(linha.matchScore)
  const palavra = citarPalavra(linha.matchedKeyword)
  return pontuacao ? `${pontuacao} · ${palavra}` : palavra
}

/** A frase de evidência da linha — o "por quê" específico, na própria linha.
 *
 *  Vazia em `novo`: a célula fica visualmente vazia de propósito. Quem lê por
 *  leitor de tela recebe "Sem pendência" pelo `sr-only` da tela — nunca uma
 *  célula muda.
 *
 *  As frases falam do que o contrato realmente devolve. Para a duplicata,
 *  `ImportRow` traz `matchTransactionId` mas **não** a descrição nem a data do
 *  lançamento que motivou a marcação — então a evidência usa a data e o valor
 *  da própria linha. Para a transferência já registrada, o contrato passou a
 *  trazer `matchOccurredOn` justamente para a frase poder dizer "em 05/09".
 *
 *  `contraparte` é o NOME da conta sugerida, resolvido pela tela a partir do
 *  cache — o id nunca chega a uma frase. Sem nome (cache ainda vazio), a frase
 *  fala em "outra conta" em vez de calar ou inventar. */
export function evidenciaDaLinha(linha: ImportRow, contraparte?: string): string {
  const conta = contraparte || 'outra conta'

  switch (linha.status) {
    case 'novo':
      return ''
    case 'repetido_no_arquivo':
      return '2ª ocorrência idêntica neste arquivo — as duas entram.'
    case 'duplicado_exato':
      return 'Já importada nesta conta · lançamento idêntico.'
    case 'duplicado_excluido':
      return 'Já importada e excluída por você.'
    case 'possivel_duplicado':
      return 'Possível duplicata de um lançamento desta conta · mesmo valor, data a até 3 dias.'
    case 'pagamento_de_fatura': {
      if (!linha.suggestedCounterpartAccountId) return 'Pagamento da fatura de um cartão.'
      // Neste status `matchScore`/`matchedKeyword` descrevem a CATEGORIA quando
      // há uma sugerida (emenda §10.4 da spec); a palavra só é da conta quando
      // não há categoria em jogo. Citar a palavra errada seria pior do que
      // não citar.
      const palavra =
        linha.suggestedCategoryId === null && linha.matchedKeyword
          ? ` · ${citarPalavra(linha.matchedKeyword)}`
          : ''
      return `Pagamento da fatura de um cartão · parece o ${conta}${palavra}`
    }
    case 'transferencia_interna': {
      const origem = proveniencia(linha)
      return origem
        ? `Parece transferência para ${conta} · ${origem}`
        : `Parece transferência para ${conta}`
    }
    case 'transferencia_ja_registrada':
      return linha.matchOccurredOn
        ? `Já registrada em ${dataCurta(linha.matchOccurredOn)} como transferência com ${conta}.`
        : `Já registrada como transferência com ${conta}.`
    case 'rejeitado': {
      const motivo = linha.rejectReason
        ? MOTIVO_DA_REJEICAO[linha.rejectReason]
        : 'formato não reconhecido'
      return `Linha inválida: ${motivo} (linha ${linha.lineNo} do arquivo).`
    }
  }
}

/** O que o `<select>` de decisão oferece. É a ação do contrato, com uma
 *  exceção: `transfer` se desdobra em duas opções quando há contraparte
 *  sugerida — "para Nubank" (a sugerida) e "para outra conta…" (a escolher).
 *  As duas são a MESMA ação `transfer`; a diferença é quem escolhe a conta. */
export type ValorDeDecisao = ImportDecisionAction | 'transfer:outra'

/** O texto de cada opção do `<select>` de decisão.
 *
 *  É aqui que o status vira linguagem: em vez de uma etiqueta "possivel
 *  duplicado" que não diz o que fazer, a pessoa lê "Não importar (é a mesma)" e
 *  "Importar assim mesmo (é outra)" — as duas consequências, em palavras, no
 *  momento de escolher.
 *
 *  O `…` em "Registrar como transferência para…" é literal e significa que
 *  falta escolher a conta: escolher essa opção revela um segundo seletor na
 *  mesma célula. Com contraparte sugerida, a conta mora no texto da opção
 *  ("para Nubank") e as reticências ficam só na opção "para outra conta…". */
export function rotuloDaAcao(linha: ImportRow, acao: ValorDeDecisao, contraparte?: string): string {
  const { status } = linha

  if (acao === 'transfer') {
    return contraparte
      ? `Registrar como transferência para ${contraparte}`
      : 'Registrar como transferência para…'
  }
  if (acao === 'transfer:outra') {
    return status === 'pagamento_de_fatura'
      ? 'Registrar como transferência para outro cartão…'
      : 'Registrar como transferência para outra conta…'
  }
  if (acao === 'link') return 'Vincular à transferência já registrada'

  if (status === 'possivel_duplicado') {
    return acao === 'skip' ? 'Não importar (é a mesma)' : 'Importar assim mesmo (é outra)'
  }
  if (status === 'duplicado_excluido') {
    return acao === 'skip' ? 'Não importar' : 'Restaurar o lançamento que eu excluí'
  }
  if (status === 'pagamento_de_fatura') {
    return acao === 'skip' ? 'Não importar' : 'Importar como despesa mesmo assim'
  }
  if (status === 'transferencia_interna') {
    if (acao === 'skip') return 'Não importar'
    return linha.kind === 'income'
      ? 'Importar como receita comum (não é transferência)'
      : 'Importar como despesa comum (não é transferência)'
  }
  return acao === 'skip' ? 'Não importar' : 'Importar'
}

/** As ações oferecidas, com o **default do servidor em primeiro**.
 *
 *  A primeira opção de um `<select>` é a que a pessoa vê sem abrir nada, então
 *  ela tem de ser exatamente a que acontece se ninguém mexer. Ordenar diferente
 *  faria a tela mostrar uma coisa e o servidor fazer outra. */
export function acoesOferecidas(linha: ImportRow): readonly ImportDecisionAction[] {
  const resto = linha.allowedActions.filter((acao) => acao !== linha.defaultAction)
  return [linha.defaultAction, ...resto]
}

/** As opções do `<select>` de decisão, já com o texto de cada uma.
 *
 *  `transfer` vira duas opções quando a linha traz contraparte sugerida: a
 *  sugerida (com o nome da conta no texto) e "outra conta…". Sem sugestão,
 *  continua uma só, com as reticências que pedem o segundo seletor. */
export function opcoesDeDecisao(
  linha: ImportRow,
  contraparte: string | undefined,
): readonly { value: ValorDeDecisao; label: string }[] {
  const opcoes: { value: ValorDeDecisao; label: string }[] = []
  for (const acao of acoesOferecidas(linha)) {
    if (acao === 'transfer' && linha.suggestedCounterpartAccountId) {
      // Sem o nome ainda (cache de contas vazio), a opção não pode virar
      // "para…" — isso significaria "falta escolher", e não falta.
      opcoes.push({
        value: 'transfer',
        label: rotuloDaAcao(linha, 'transfer', contraparte || 'a conta sugerida'),
      })
      opcoes.push({ value: 'transfer:outra', label: rotuloDaAcao(linha, 'transfer:outra') })
      continue
    }
    opcoes.push({ value: acao, label: rotuloDaAcao(linha, acao) })
  }
  return opcoes
}
