import type { ReactNode } from 'react'
import type { Account, CategoryTree, ImportRow } from '@/api/types'
import { MoneyText } from '@/components/MoneyText/MoneyText'
import { Select } from '@/components/Select/Select'
import { contaPorId, opcoesDeConta } from '@/lib/accounts'
import { categoriaPorId, type LadoDoDinheiro, opcoesDeCategoria } from '@/lib/categories'
import { dataCurta } from '@/lib/civil'
import { ehTransferencia, valorComSinal } from '@/lib/kind'
import { formatarDinheiro } from '@/lib/money'
import { acaoEfetiva, categoriaEfetiva, type Escolha, type Escolhas } from '../decisoes'
import { evidenciaDaLinha, opcoesDeDecisao, proveniencia, type ValorDeDecisao } from '../lexico'
import styles from './CelulasDaRevisao.module.css'
import { FichasDeAprender } from './FichasDeAprender'

/** O que os blocos com controle têm em comum e passam às células: o cache de
 *  contas e categorias (para escrever NOMES, nunca ids) e as escolhas. */
export type ContextoDaRevisao = {
  escolhas: Escolhas
  /** Todas as contas da casa — para resolver o nome da contraparte sugerida. */
  contas: readonly Account[]
  /** A conta do lote: nunca é oferecida como outra perna da transferência. */
  contaDoLoteId: string | undefined
  categorias: CategoryTree | undefined
  onEscolher: (rowId: string, escolha: Escolha) => void
}

/** O nome da conta sugerida como contraparte, ou `undefined`. */
export function nomeDaContraparte(
  linha: ImportRow,
  contas: readonly Account[],
): string | undefined {
  return contaPorId(contas, linha.suggestedCounterpartAccountId ?? undefined)?.name
}

/** As células que os blocos da revisão têm em comum.
 *
 *  Ficam num arquivo só porque precisam ser **idênticas** em todos: a mesma
 *  linha aparece com a mesma data, a mesma descrição e o mesmo valor esteja ela
 *  esperando decisão, parecendo transferência, pronta para entrar ou barrada.
 *  Se cada bloco desenhasse a sua, a pessoa compararia maçãs com laranjas ao
 *  rolar de um para o outro.
 *
 *  Linha `rejeitado` é o caso de borda que manda no formato: nela `kind`,
 *  `occurredOn`, `amountCents` e `description` vêm **nulos** — por definição
 *  aquela linha não produziu valores de domínio. Cada célula tem de saber dizer
 *  isso sem quebrar e sem inventar um zero. */

/** A data, ou `—` quando a linha não tem uma que dê para ler. */
export function CelulaData({ linha }: { linha: ImportRow }) {
  if (!linha.occurredOn) {
    return (
      <span className={styles.ausente}>
        <span aria-hidden="true">—</span>
        <span className="sr-only">sem data</span>
      </span>
    )
  }
  return <span className={styles.data}>{dataCurta(linha.occurredOn)}</span>
}

/** A categoria com que a linha vai entrar, para a linha secundária do celular. */
export type CategoriaDaLinha = {
  nome: string
  /** `true` quando veio da sugestão do servidor e a pessoa não mexeu. */
  sugerida: boolean
}

/** Descrição e, embaixo dela, a **frase de evidência** — o "por quê" daquela
 *  linha, com o que o servidor de fato devolveu.
 *
 *  A evidência é um dos quatro portadores que substituem a cor na marcação de
 *  duplicata. Em `novo` ela é vazia de propósito (escrever "Novo" 59 vezes é
 *  ruído), mas nunca vazia para quem ouve: o `sr-only` diz "Sem pendência", e
 *  uma célula muda deixaria a pessoa sem saber se a linha está limpa ou se a
 *  informação se perdeu.
 *
 *  `contraparte` é o nome da conta sugerida — a evidência de transferência
 *  fala dela. `categoria` alimenta a linha secundária que só existe abaixo de
 *  40rem, onde a coluna Categoria foi escondida: a sugestão vai ser gravada, e
 *  esconder a coluna só é honesto se o dado reaparece. `children` são as fichas
 *  de aprender, quando o bloco decide que cabem. */
export function CelulaDescricao({
  linha,
  contraparte,
  categoria,
  children,
}: {
  linha: ImportRow
  contraparte?: string | undefined
  categoria?: CategoriaDaLinha | undefined
  children?: ReactNode
}) {
  const descricao = linha.description?.trim()
  const evidencia = evidenciaDaLinha(linha, contraparte)

  return (
    <>
      <span className={styles.descricao} title={descricao || undefined}>
        {descricao || <span className={styles.ausente}>(linha {linha.lineNo} do arquivo)</span>}
      </span>
      {evidencia ? (
        <span className={styles.evidencia}>{evidencia}</span>
      ) : (
        <span className="sr-only">Sem pendência.</span>
      )}
      {categoria ? (
        <span className={styles.secundaria}>
          Categoria: {categoria.nome}
          {categoria.sugerida ? ' · sugerida' : ''}
        </span>
      ) : null}
      {children}
    </>
  )
}

/** A célula de descrição de uma linha COM controle: resolve o nome da
 *  contraparte, a categoria efetiva (para a linha secundária do celular) e
 *  decide se as fichas de aprender cabem.
 *
 *  Fichas só numa linha com ação `import`, **sem** sugestão, com categoria
 *  escolhida à mão: é a linha que a pessoa acabou de classificar sozinha. Linha
 *  com sugestão (mesmo trocada) já ensina sozinha. Ao terminar, o foco vai ao
 *  `<select>` de categoria da linha — a ficha mora noutra célula. */
export function CelulaDescricaoDaLinha({
  linha,
  contexto,
}: {
  linha: ImportRow
  contexto: ContextoDaRevisao
}) {
  const { escolhas, contas, categorias } = contexto
  const acao = acaoEfetiva(linha, escolhas)
  const contraparte = nomeDaContraparte(linha, contas)

  const categoriaId = acao === 'import' ? categoriaEfetiva(linha, escolhas) : null
  const categoria = categoriaPorId(categorias, categoriaId)
  const sugerida = categoriaId !== null && categoriaId === linha.suggestedCategoryId

  const ensina =
    categoria !== undefined && linha.suggestedCategoryId === null && !!linha.description

  return (
    <CelulaDescricao
      linha={linha}
      contraparte={contraparte}
      categoria={categoria ? { nome: categoria.name, sugerida } : undefined}
    >
      {ensina ? (
        <FichasDeAprender
          // Trocar de categoria zera o que esta linha aprendeu: as palavras
          // gravadas eram da outra.
          key={categoria.id}
          descricao={linha.description ?? ''}
          categoria={categoria}
          onAprendida={() => document.getElementById(idDoSelectDeCategoria(linha.id))?.focus()}
        />
      ) : null}
    </CelulaDescricao>
  )
}

/** O valor com sinal explícito. Cor é reforço — quem carrega a direção é o
 *  `+`/`−`, que sobrevive ao preto e branco. Transferência fica neutra. */
export function CelulaValor({ linha }: { linha: ImportRow }) {
  if (linha.amountCents === null || linha.kind === null) {
    return (
      <span className={styles.ausente}>
        <span aria-hidden="true">—</span>
        <span className="sr-only">valor não lido</span>
      </span>
    )
  }

  return (
    <MoneyText
      cents={valorComSinal(linha.kind, linha.amountCents)}
      tone={ehTransferencia(linha.kind) ? 'neutral' : 'semantic'}
      sign="always"
    />
  )
}

/** A célula de decisão: um `<select>` nativo com **palavras**, não uma
 *  etiqueta colorida.
 *
 *  É a jogada central da revisão: o status não vira rótulo ("possível
 *  duplicado"), vira o texto das opções — "Não importar (é a mesma)" e "Importar
 *  assim mesmo (é outra)". A pessoa lê a consequência no momento de escolher,
 *  e não precisa aprender vocabulário nenhum.
 *
 *  A contraparte sugerida mora **no texto da opção** ("Registrar como
 *  transferência para Nubank"), não num segundo select pré-preenchido. Só a
 *  opção com reticências ("para outra conta…") revela o segundo seletor — as
 *  reticências continuam significando "falta escolher". */
export function CelulaDeDecisao({
  linha,
  contexto,
  faltaDestino,
  placeholderDaConta,
}: {
  linha: ImportRow
  contexto: ContextoDaRevisao
  faltaDestino: boolean
  /** "Escolha o cartão" na fatura, "Escolha a conta" na transferência. */
  placeholderDaConta: string
}) {
  const { escolhas, contas, contaDoLoteId, onEscolher } = contexto
  const acao = acaoEfetiva(linha, escolhas)
  const escolha = escolhas[linha.id]
  const opcoes = opcoesDeDecisao(linha, nomeDaContraparte(linha, contas))

  // A outra perna nunca é a conta do lote (400) nem uma conta arquivada: as
  // duas seriam opções que só dão erro no confirm — e derrubam o lote inteiro.
  const contasDeDestino = contas.filter(
    (conta) => conta.id !== contaDoLoteId && conta.archivedAt === null,
  )

  const temSugestao = Boolean(linha.suggestedCounterpartAccountId)
  const valor: ValorDeDecisao =
    acao === 'transfer' && temSugestao && escolha?.outraConta ? 'transfer:outra' : acao
  // O segundo seletor aparece quando falta escolher a conta: sem sugestão, ou
  // com a sugestão recusada.
  const pedeConta = acao === 'transfer' && (!temSugestao || escolha?.outraConta === true)

  return (
    <div className={styles.decisao}>
      <Select
        label="Decisão"
        labelHidden
        density="compact"
        // O nome acessível precisa dizer de QUAL linha é esta decisão: seis
        // selects chamados "Decisão" não dizem nada a quem navega por lista de
        // controles.
        aria-label={`Decisão para ${rotuloDaLinha(linha)}`}
        options={opcoes}
        value={valor}
        onChange={(evento) => {
          // Sem cast: a opção escolhida tem de estar entre as que o SERVIDOR
          // ofereceu para esta linha. Um `as ImportDecisionAction` aceitaria
          // qualquer texto e o 400 só apareceria no confirm, derrubando o lote.
          const escolhida = opcoes.find((opcao) => opcao.value === evento.target.value)
          if (!escolhida) return
          if (escolhida.value === 'transfer:outra') {
            onEscolher(linha.id, { acao: 'transfer', outraConta: true })
            return
          }
          onEscolher(linha.id, { acao: escolhida.value })
        }}
      />

      {pedeConta ? (
        <Select
          label="Conta de destino"
          labelHidden
          density="compact"
          placeholder={placeholderDaConta}
          aria-label={`Conta de destino da transferência de ${rotuloDaLinha(linha)}`}
          options={opcoesDeConta(contasDeDestino)}
          value={escolha?.contraparteId ?? ''}
          {...(faltaDestino ? { error: 'Escolha a conta de destino.' } : {})}
          onChange={(evento) =>
            onEscolher(linha.id, {
              acao: 'transfer',
              contraparteId: evento.target.value || undefined,
              ...(temSugestao ? { outraConta: true } : {}),
            })
          }
        />
      ) : null}
    </div>
  )
}

/** O id do `<select>` de categoria de uma linha — é para onde o foco volta
 *  depois que uma ficha de aprender termina. Determinístico de propósito: a
 *  ficha mora noutra célula e não tem ref para o select. */
export function idDoSelectDeCategoria(rowId: string): string {
  return `categoria-${rowId}`
}

/** A célula de categoria: o `<select>` compacto já com a sugestão selecionada
 *  e, embaixo, a **linha de proveniência** — de onde a sugestão veio.
 *
 *  A sugestão vive no controle, não numa etiqueta: não existe `Badge`
 *  "sugerida". A pontuação é texto (`88%`, `tabular-nums`), nunca barra nem
 *  cor. A linha de proveniência existe **enquanto** o valor do select for o
 *  sugerido: trocar ou limpar a apaga; voltar à sugerida a traz de volta. Ela
 *  descreve a origem do que está selecionado, não um histórico.
 *
 *  O placeholder é `Sem categoria`, e não `—`: com sugestão em jogo, escolher a
 *  opção vazia é uma decisão ("entrar sem categoria apesar da sugestão"), e um
 *  travessão não diz isso. */
export function CelulaDeCategoria({
  linha,
  contexto,
}: {
  linha: ImportRow
  contexto: ContextoDaRevisao
}) {
  const { escolhas, categorias, onEscolher } = contexto
  const acao = acaoEfetiva(linha, escolhas)
  const lado = ladoDaLinha(linha)

  // Categoria só faz sentido no que vai entrar como receita ou despesa: o
  // contrato recusa categoria em `skip`, e transferência nunca tem categoria.
  // E é OPCIONAL de propósito: obrigar a classificar 59 linhas antes de
  // confirmar mataria o fluxo — a faixa de pendência de `/lancamentos` cobra o
  // resto depois.
  if (acao !== 'import' || !lado) {
    return (
      <span className={styles.semCategoria} aria-hidden="true">
        —
      </span>
    )
  }

  const efetiva = categoriaEfetiva(linha, escolhas)
  const ehASugerida = linha.suggestedCategoryId !== null && efetiva === linha.suggestedCategoryId
  const origem = ehASugerida ? proveniencia(linha) : ''

  return (
    <div className={styles.categoria}>
      <Select
        id={idDoSelectDeCategoria(linha.id)}
        label="Categoria"
        labelHidden
        density="compact"
        placeholder="Sem categoria"
        aria-label={`Categoria de ${rotuloDaLinha(linha)}`}
        options={opcoesDeCategoria(categorias, lado)}
        value={efetiva ?? ''}
        onChange={(evento) => {
          const valor = evento.target.value
          // Tri-estado: vazio numa linha COM sugestão é "limpar" (`null`), e
          // numa linha sem sugestão é só "não escolhi" (`undefined`). Escolher
          // a própria sugerida é aceitá-la de volta.
          let categoriaId: string | null | undefined
          if (valor === '') {
            categoriaId = linha.suggestedCategoryId === null ? undefined : null
          } else {
            categoriaId = valor === linha.suggestedCategoryId ? undefined : valor
          }
          onEscolher(linha.id, { acao: 'import', categoriaId })
        }}
      />
      {origem ? (
        <span className={styles.proveniencia}>
          <span className="sr-only">Sugerida pela palavra-chave </span>
          {origem}
        </span>
      ) : null}
    </div>
  )
}

/** O LADO DO DINHEIRO da linha — `null` em transferência e em linha sem
 *  `kind`.
 *
 *  É o que impede a tela de oferecer "Salário" para uma despesa: oferecer o
 *  outro lado é oferecer um erro. Dentro do lado, o seletor traz as DUAS
 *  naturezas que ele aceita (ADR-029b) — sem isso a pessoa veria a sugestão de
 *  investimento na revisão e não conseguiria escolhê-la à mão. */
export function ladoDaLinha(linha: ImportRow): LadoDoDinheiro | null {
  if (linha.kind === 'income') return 'income'
  if (linha.kind === 'expense') return 'expense'
  return null
}

/** Nome da linha para os `aria-label` dos controles.
 *
 *  Nunca só "Importar": quem navega por lista de controles ouviria cinquenta
 *  vezes a mesma palavra e não saberia qual linha está marcando — e aqui marcar
 *  a linha errada grava dinheiro que não existe, ou deixa de fora dinheiro que
 *  existe. */
export function rotuloDaLinha(linha: ImportRow): string {
  const descricao = linha.description?.trim() || `linha ${linha.lineNo} do arquivo`
  const data = linha.occurredOn ? dataCurta(linha.occurredOn) : 'sem data'
  return `${descricao}, ${data}, ${valorFalado(linha)}`
}

/** "11 reais negativos" — o que o leitor de tela recebe no lugar de "−11,00",
 *  que sairia como "traço onze". */
function valorFalado(linha: ImportRow): string {
  if (linha.amountCents === null || linha.kind === null) return 'valor não lido'

  const assinado = valorComSinal(linha.kind, linha.amountCents)
  const absoluto = formatarDinheiro(Math.abs(assinado))
  if (assinado < 0) return `${absoluto} negativos`
  if (assinado > 0) return `${absoluto} positivos`
  return absoluto
}
