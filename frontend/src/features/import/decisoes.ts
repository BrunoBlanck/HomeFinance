import type { ImportDecision, ImportDecisionAction, ImportRow } from '@/api/types'
import { BLOCO_DO_STATUS } from './lexico'

/** O que a pessoa escolheu para uma linha. Ausente = o default do servidor. */
export type Escolha = {
  acao: ImportDecisionAction
  /** Só faz sentido com `import`. O contrato recusa categoria em `skip`.
   *
   *  **Tri-estado**, espelhando `ImportDecision.categoryId`:
   *  - `undefined` — a pessoa não mexeu: vale a sugestão do servidor
   *    (`suggestedCategoryId`), se houver;
   *  - `null` — a pessoa LIMPOU a sugestão: entra sem categoria apesar dela;
   *  - texto — a pessoa escolheu esta. */
  categoriaId?: string | null | undefined
  /** A conta da outra perna do par, quando a pessoa a escolheu à mão. Ausente
   *  com `transfer` numa linha com contraparte sugerida = usa a sugerida. */
  contraparteId?: string | undefined
  /** `true` quando a pessoa escolheu "Registrar como transferência para outra
   *  conta…" numa linha que TINHA contraparte sugerida — é o que revela o
   *  segundo seletor e o que torna `contraparteId` obrigatória. */
  outraConta?: boolean | undefined
}

export type Escolhas = Readonly<Record<string, Escolha>>

/** O que acontece com a linha se a pessoa não mexer em mais nada.
 *
 *  **O default é do servidor**, sempre (`defaultAction`). A tela não
 *  reimplementa a tabela de status da spec: uma cópia dela aqui divergiria no
 *  primeiro ajuste do backend, e em silêncio — a tela prometeria uma coisa e o
 *  commit faria outra. */
export function acaoEfetiva(linha: ImportRow, escolhas: Escolhas): ImportDecisionAction {
  return escolhas[linha.id]?.acao ?? linha.defaultAction
}

/** A categoria com que a linha VAI ENTRAR, ou `null`.
 *
 *  É a precedência do servidor, aplicada na tela para o `<select>` mostrar o
 *  que vai ser gravado: escolha da pessoa (inclusive "sem categoria") > sugestão
 *  da análise > nada. Sem isto, o select mostraria vazio numa linha que entra
 *  com "Alimentação" — e a pessoa confirmaria sem saber. */
export function categoriaEfetiva(linha: ImportRow, escolhas: Escolhas): string | null {
  const escolhida = escolhas[linha.id]?.categoriaId
  if (escolhida === undefined) return linha.suggestedCategoryId
  return escolhida
}

/** O corpo do confirm: **apenas as exceções**.
 *
 *  Uma linha entra na lista quando (a) a ação escolhida difere do default, ou
 *  (b) ela carrega algo que o default não carrega — uma categoria diferente da
 *  sugerida, a ordem de entrar SEM categoria apesar da sugestão, ou a conta da
 *  outra perna. Linha que aceita o default inteiro fica de fora, que é o
 *  contrato: com 10.000 `rowId` o corpo estouraria o limite de 1 MiB.
 *
 *  O tri-estado de `categoryId` é honrado à risca: **ausente** quando a pessoa
 *  aceitou a sugestão (ou voltou a ela), **`null`** só quando ela limpou uma
 *  sugestão que existia, **valor** quando trocou. Mandar `null` numa linha sem
 *  sugestão seria uma exceção que não muda nada.
 *
 *  Recusas deliberadas, cada uma para não transformar um engano da tela num
 *  400 que derruba o lote inteiro:
 *
 *  - linha do bloco "ficam de fora" **nunca** vira decisão — ela não tem ação
 *    permitida, e citá-la é 400;
 *  - ação fora de `allowedActions` é descartada aqui, antes de virar
 *    requisição;
 *  - `categoryId` só acompanha `import`, e `counterpartAccountId` só acompanha
 *    `transfer` — o contrato recusa as combinações trocadas;
 *  - `transfer` numa linha com contraparte sugerida vai SEM
 *    `counterpartAccountId` quando a pessoa aceitou a sugerida: o servidor a
 *    usa. Só vai com o campo quando ela escolheu outra conta. */
export function montarDecisoes(linhas: readonly ImportRow[], escolhas: Escolhas): ImportDecision[] {
  const decisoes: ImportDecision[] = []

  for (const linha of linhas) {
    if (BLOCO_DO_STATUS[linha.status] === 'fora') continue

    const escolha = escolhas[linha.id]
    const acao = escolha?.acao ?? linha.defaultAction
    if (!linha.allowedActions.includes(acao)) continue

    const categoriaId = acao === 'import' ? categoriaAEnviar(linha, escolha) : undefined
    const contraparteId = acao === 'transfer' ? escolha?.contraparteId : undefined

    const ehExcecao = acao !== linha.defaultAction || categoriaId !== undefined
    if (!ehExcecao) continue

    decisoes.push({
      rowId: linha.id,
      action: acao,
      ...(categoriaId !== undefined ? { categoryId: categoriaId } : {}),
      ...(contraparteId ? { counterpartAccountId: contraparteId } : {}),
    })
  }

  return decisoes
}

/** O `categoryId` da decisão, ou `undefined` para OMITIR o campo. */
function categoriaAEnviar(
  linha: ImportRow,
  escolha: Escolha | undefined,
): string | null | undefined {
  const escolhida = escolha?.categoriaId
  if (escolhida === undefined) return undefined
  if (escolhida === null) {
    // Limpar só é uma ordem quando havia sugestão a contrariar.
    return linha.suggestedCategoryId === null ? undefined : null
  }
  // Escolher a própria sugerida é aceitá-la: o servidor já faria isso.
  return escolhida === linha.suggestedCategoryId ? undefined : escolhida
}

/** Transferências sem conta de destino escolhida.
 *
 *  `counterpartAccountId` é obrigatório com `transfer` quando a linha não traz
 *  contraparte sugerida — ou quando a pessoa recusou a sugerida e escolheu
 *  "outra conta…". Mandar sem ele é 400, e como o confirm é tudo ou nada para o
 *  lote, um `<select>` esquecido derrubaria a importação inteira. A tela cobra
 *  antes de enviar. */
export function transferenciasIncompletas(
  linhas: readonly ImportRow[],
  escolhas: Escolhas,
): ImportRow[] {
  return linhas.filter((linha) => {
    const escolha = escolhas[linha.id]
    if (escolha?.acao !== 'transfer' || escolha.contraparteId) return false
    return !linha.suggestedCounterpartAccountId || escolha.outraConta === true
  })
}

export type ContagemDaRevisao = {
  /** Linhas que vão virar lançamento — inclui restauradas e transferências. */
  vaoEntrar: number
  /** Linhas que a PESSOA mandou ignorar. Não inclui as bloqueadas. */
  ignorados: number
  /** Linhas cuja gêmea estava excluída e serão restauradas, não reinseridas. */
  restaurados: number
  /** Pares de transferência a criar (cada par conta 1). */
  transferencias: number
  /** Linhas vinculadas a uma transferência que já existia (`link`): não criam
   *  lançamento, não entram em `vaoEntrar` nem em `ignorados`. */
  vinculadas: number
  /** Linhas que não podem entrar de jeito nenhum — o bloco "ficam de fora". */
  bloqueadas: number
}

/** O que o botão de confirmar promete, contado a partir das mesmas escolhas que
 *  montam o corpo da requisição.
 *
 *  Contar noutro lugar que não este faria o rótulo dizer "Importar 42" e o
 *  servidor gravar outro número — e o rótulo é a última coisa que a pessoa lê
 *  antes de escrever no banco. */
export function contarRevisao(linhas: readonly ImportRow[], escolhas: Escolhas): ContagemDaRevisao {
  const contagem: ContagemDaRevisao = {
    vaoEntrar: 0,
    ignorados: 0,
    restaurados: 0,
    transferencias: 0,
    vinculadas: 0,
    bloqueadas: 0,
  }

  for (const linha of linhas) {
    if (BLOCO_DO_STATUS[linha.status] === 'fora') {
      contagem.bloqueadas += 1
      continue
    }

    const acao = acaoEfetiva(linha, escolhas)
    if (acao === 'skip') {
      contagem.ignorados += 1
      continue
    }
    if (acao === 'link') {
      contagem.vinculadas += 1
      continue
    }

    contagem.vaoEntrar += 1
    if (acao === 'transfer') contagem.transferencias += 1
    if (acao === 'import' && linha.status === 'duplicado_excluido') contagem.restaurados += 1
  }

  return contagem
}

/** `true` quando confirmar não faria nada — nem lançamento, nem vínculo. */
export function nadaMarcado(contagem: ContagemDaRevisao): boolean {
  return contagem.vaoEntrar === 0 && contagem.vinculadas === 0
}

/** O rótulo do botão de confirmar — ele diz o que vai acontecer.
 *
 *  Com nada marcado o rótulo MUDA (em vez de o botão apagar): quem chega nele
 *  pelo teclado precisa descobrir por que não pode prosseguir, e um botão
 *  `disabled` não conta nada a ninguém. O `aria-disabled` fica com a tela.
 *
 *  Só vínculos ("Vincular 2 lançamentos") é rótulo próprio: vincular não grava
 *  lançamento nenhum, e "Importar 0" seria mentira. */
export function rotuloDoConfirmar(contagem: ContagemDaRevisao): string {
  if (nadaMarcado(contagem)) return 'Nada marcado para importar'

  if (contagem.vaoEntrar === 0) {
    return contagem.vinculadas === 1
      ? 'Vincular 1 lançamento'
      : `Vincular ${contagem.vinculadas} lançamentos`
  }

  const partes = [
    contagem.vaoEntrar === 1
      ? 'Importar 1 lançamento'
      : `Importar ${contagem.vaoEntrar} lançamentos`,
  ]
  if (contagem.vinculadas > 0) {
    partes.push(`${contagem.vinculadas} ${contagem.vinculadas === 1 ? 'vinculado' : 'vinculados'}`)
  }
  if (contagem.ignorados > 0) {
    partes.push(`${contagem.ignorados} ${contagem.ignorados === 1 ? 'ignorado' : 'ignorados'}`)
  }
  return partes.join(' · ')
}

/** A nuance da barra de confirmação, quando há uma.
 *
 *  Restaurar, transferir e vincular não são "importar mais uma linha": a
 *  primeira mexe num lançamento que já existiu, a segunda cria um par em DUAS
 *  contas, a terceira não cria nada. Quem confirma merece ler isso antes, e não
 *  descobrir no resultado. */
export function nuanceDaConfirmacao(contagem: ContagemDaRevisao): string {
  const partes: string[] = []
  if (contagem.restaurados > 0) {
    partes.push(
      contagem.restaurados === 1
        ? '1 lançamento restaurado'
        : `${contagem.restaurados} lançamentos restaurados`,
    )
  }
  if (contagem.transferencias > 0) {
    partes.push(
      contagem.transferencias === 1
        ? '1 transferência'
        : `${contagem.transferencias} transferências`,
    )
  }
  if (contagem.vinculadas > 0) {
    partes.push(
      contagem.vinculadas === 1
        ? '1 vínculo a transferência já registrada'
        : `${contagem.vinculadas} vínculos a transferências já registradas`,
    )
  }
  if (partes.length === 0) return ''
  if (partes.length === 1) return `Inclui ${partes[0]}.`
  const ultima = partes.pop()
  return `Inclui ${partes.join(', ')} e ${ultima}.`
}
