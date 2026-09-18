import type { ReactNode } from 'react'
import { Skeleton } from '../Skeleton/Skeleton'
import styles from './DataTable.module.css'

export type Column<T> = {
  /** Chave estável — usada como `key` de React e nada mais. */
  key: string
  header: string
  /** Valores monetários e contagens vão para a direita; texto fica na esquerda. */
  align?: 'start' | 'end' | undefined
  /** Coluna de ação não recebe cabeçalho visível, mas precisa de nome para o
   *  leitor de tela. */
  headerHidden?: boolean | undefined
  /** `min` encolhe até o conteúdo — coluna de botão, de data, de valor, de
   *  etiqueta. `auto` (o padrão) é coluna de texto.
   *
   *  Quando a tabela tem UMA só coluna `auto`, ela é a elástica: fica com o
   *  espaço que sobra e **trunca** em vez de empurrar as outras para fora da
   *  moldura (ver `colunaElastica`). Por isso o que mora numa coluna `auto`
   *  precisa de `text-overflow: ellipsis` e `title` — quem decide o que pode
   *  sumir do texto é a célula da tela, não a tabela. */
  width?: 'auto' | 'min' | undefined
  /** Esconde a coluna abaixo de 40rem, onde ela não cabe.
   *
   *  Quem esconde é o CSS do próprio componente, não a tela: assim o `th` e as
   *  `td` da coluna somem juntos e a contagem de células da linha continua
   *  batendo. Escondida não é descartada — o dado precisa reaparecer na tela
   *  estreita dentro de outra coluna, senão some de verdade. */
  hideBelow?: 'sm' | undefined
  render: (row: T) => ReactNode
}

/** Bloco de linhas com cabeçalho próprio.
 *
 *  Atende aos dois agrupamentos da entrega E2: o dia em `/lancamentos` (com o
 *  subtotal no `trailing`) e o motivo do bloqueio em `/importar` (com o "por
 *  quê" no `description`, escrito uma vez para o grupo em vez de repetido em
 *  cada linha). */
export type RowGroup<T> = {
  /** Chave estável do grupo — `key` de React e nada mais. */
  key: string
  /** Rótulo do grupo: "segunda, 31 de agosto", "Possível duplicata · 4". */
  label: string
  /** Explicação do grupo, quando o rótulo sozinho não basta. */
  description?: string | undefined
  /** Canto direito do cabeçalho — o subtotal do dia, tipicamente. Mora dentro
   *  do próprio `<th>` do grupo (ver `CabecalhoDeGrupo`). */
  trailing?: ReactNode | undefined
  rows: readonly T[]
}

type DataTableProps<T> = {
  /** Rotula a tabela para quem navega por lista de tabelas. */
  caption: string
  columns: readonly Column<T>[]
  rowKey: (row: T) => string
  /** Atributos por linha — hoje `data-archived`, que apaga a linha
   *  visualmente sem escondê-la. */
  rowAttrs?: ((row: T) => Record<string, string | undefined>) | undefined
  loading?: boolean | undefined
  /** Mostrado no lugar do corpo quando não há linha nenhuma. */
  empty?: ReactNode | undefined
  /** Linha de total, fora do `<tbody>` — vai para o `<tfoot>`, que é onde o
   *  leitor de tela espera encontrar o resumo. */
  footer?: ReactNode | undefined
  /** Linha de DETALHE: quando devolve conteúdo, uma `<tr class="detail">` com
   *  uma única `<td colSpan>` entra logo abaixo da linha — é onde o atalho de
   *  categorização de `/lancamentos` abre o editor "na própria linha"
   *  (docs/DESIGN.md, E2c (h)). Não é camada flutuante: a linha se expande
   *  dentro da `<table>`, sem popover, sem sombra e sem animação de altura.
   *  `null` para as linhas que não têm nada a mostrar. */
  detail?: ((row: T) => ReactNode | null) | undefined
} & (
  | { rows: readonly T[]; groups?: undefined }
  /** Agrupado: uma tabela só, com um `<tbody>` por grupo. A alternativa — uma
   *  `<table>` por dia — repetiria o cabeçalho a cada bloco e quebraria a
   *  navegação por coluna do leitor de tela, que é justamente o que torna a
   *  grade de números navegável sem enxergar. */
  | { rows?: undefined; groups: readonly RowGroup<T>[] }
)

/** A coluna que fica com o espaço que sobra.
 *
 *  Só existe quando a tabela tem UMA coluna `auto`: aí o layout automático não
 *  tem a quem dar a sobra além dela, e cortar o texto dessa coluna é o que
 *  mantém a tabela dentro da moldura. Com duas ou mais `auto` (a tabela de
 *  pares de /transferencias) não há eleição — cortar uma delas só engordaria as
 *  outras, e a repartição do navegador já é a certa. */
function colunaElastica<T>(columns: readonly Column<T>[]): string | undefined {
  const elasticas = columns.filter((coluna) => (coluna.width ?? 'auto') === 'auto')
  return elasticas.length === 1 ? elasticas[0]?.key : undefined
}

/** Tabela densa do HomeFinance.
 *
 *  É `<table>` de verdade, e isso não é purismo: um leitor de tela numa tabela
 *  semântica anuncia "coluna Saldo, linha 3" ao navegar, e é isso que torna uma
 *  grade de números navegável sem enxergar. Uma pilha de `<div>` com
 *  `display: grid` perde essa navegação inteira.
 *
 *  Densa porque quem controla contas quer ver o mês todo, não três cartões
 *  gigantes (docs/DESIGN.md, princípio 4). */
export function DataTable<T>({
  caption,
  columns,
  rows,
  groups,
  rowKey,
  rowAttrs,
  loading = false,
  empty,
  footer,
  detail,
}: DataTableProps<T>) {
  if (loading) {
    return <TabelaCarregando caption={caption} columns={columns} />
  }

  const total = groups
    ? groups.reduce((soma, grupo) => soma + grupo.rows.length, 0)
    : (rows?.length ?? 0)

  if (total === 0 && empty) {
    return <div className={styles.emptyWrap}>{empty}</div>
  }

  const elastica = colunaElastica(columns)

  return (
    <div className={styles.scroll}>
      <table className={styles.table}>
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr>
            {columns.map((coluna) => (
              <th
                key={coluna.key}
                scope="col"
                data-align={coluna.align ?? 'start'}
                data-width={coluna.width ?? 'auto'}
                data-flex={coluna.key === elastica ? '' : undefined}
                data-hide={coluna.hideBelow}
              >
                {coluna.headerHidden ? (
                  <span className="sr-only">{coluna.header}</span>
                ) : (
                  coluna.header
                )}
              </th>
            ))}
          </tr>
        </thead>
        {groups ? (
          groups.map((grupo) => (
            <tbody key={grupo.key}>
              <CabecalhoDeGrupo grupo={grupo} colunas={columns.length} />
              {grupo.rows.map((linha) => (
                <LinhaDeDados
                  key={rowKey(linha)}
                  linha={linha}
                  columns={columns}
                  elastica={elastica}
                  rowAttrs={rowAttrs}
                  detail={detail}
                />
              ))}
            </tbody>
          ))
        ) : (
          <tbody>
            {(rows ?? []).map((linha) => (
              <LinhaDeDados
                key={rowKey(linha)}
                linha={linha}
                columns={columns}
                elastica={elastica}
                rowAttrs={rowAttrs}
                detail={detail}
              />
            ))}
          </tbody>
        )}
        {footer ? <tfoot>{footer}</tfoot> : null}
      </table>
    </div>
  )
}

function LinhaDeDados<T>({
  linha,
  columns,
  elastica,
  rowAttrs,
  detail,
}: {
  linha: T
  columns: readonly Column<T>[]
  /** `key` da coluna que fica com a sobra — ver `colunaElastica`. */
  elastica: string | undefined
  rowAttrs: ((row: T) => Record<string, string | undefined>) | undefined
  detail: ((row: T) => ReactNode | null) | undefined
}) {
  const detalhe = detail?.(linha) ?? null
  return (
    <>
      <tr {...(rowAttrs?.(linha) ?? {})}>
        {columns.map((coluna) => (
          <td
            key={coluna.key}
            data-align={coluna.align ?? 'start'}
            data-width={coluna.width ?? 'auto'}
            data-flex={coluna.key === elastica ? '' : undefined}
            data-hide={coluna.hideBelow}
          >
            {coluna.render(linha)}
          </td>
        ))}
      </tr>
      {/* A `<td>` cobre TODAS as colunas, inclusive as escondidas abaixo de
          40rem: coluna em `display: none` não tem largura, então o `colSpan`
          maior que o número de colunas visíveis não cria largura fantasma. */}
      {detalhe !== null ? (
        <tr className={styles.detail}>
          <td colSpan={columns.length}>{detalhe}</td>
        </tr>
      ) : null}
    </>
  )
}

/** Cabeçalho do grupo — `<th scope="rowgroup">` de verdade.
 *
 *  `scope="rowgroup"` é o que faz o leitor de tela trazer "Possível duplicata"
 *  junto ao entrar em cada linha do bloco. Como o app não marca estado com cor
 *  (docs/DESIGN.md), a palavra do grupo É a informação — ela precisa chegar a
 *  quem lê linha a linha, não só a quem vê o bloco inteiro de uma vez.
 *
 *  **Uma célula só, cobrindo a linha inteira** — e isso é correção de layout,
 *  não preferência. A versão anterior punha o `trailing` numa `<td>` separada,
 *  com o `th` em `colSpan={colunas - 1}`; abaixo de 40rem, onde `hideBelow`
 *  apaga colunas, esse par pedia mais colunas do que a tabela tinha e o
 *  navegador inventava as que faltavam: a 375px, 94 px de coluna fantasma
 *  comiam a largura da Descrição (medido em 17/09/2026). `colSpan` é fixo e
 *  `display: none` é por largura — as duas contas nunca fecham. Com uma célula
 *  cobrindo tudo, o `colSpan` maior que o número de colunas visíveis não cria
 *  largura nenhuma, em qualquer largura de tela.
 *
 *  O subtotal passa a fazer parte do que o cabeçalho do grupo DIZ ("segunda, 31
 *  de agosto · subtotal do dia −R$ 5.029,00"), e não de uma célula vizinha. Ele
 *  continua exatamente onde estava na tela — encostado na borda direita, na
 *  altura da coluna de valores. */
function CabecalhoDeGrupo<T>({ grupo, colunas }: { grupo: RowGroup<T>; colunas: number }) {
  return (
    <tr className={styles.groupHead}>
      <th scope="rowgroup" colSpan={colunas}>
        {/* A caixa do cabeçalho tem a largura da MOLDURA (`100cqi`), não a da
            tabela: numa tabela que rola na horizontal, um parágrafo que
            acompanha a largura da tabela nasce cortado. `sticky` mantém a
            palavra do grupo à vista enquanto as colunas escorregam por baixo —
            e a palavra do grupo é justamente o que substitui a cor aqui. */}
        <span className={styles.groupContent}>
          <span className={styles.groupText}>
            <span className={styles.groupLabel}>{grupo.label}</span>
            {grupo.description ? (
              <span className={styles.groupDescription}>{grupo.description}</span>
            ) : null}
          </span>
          {grupo.trailing !== undefined ? (
            <span className={styles.groupTrailing}>{grupo.trailing}</span>
          ) : null}
        </span>
      </th>
    </tr>
  )
}

/** Esqueleto com a MESMA estrutura da tabela real: o cabeçalho já aparece, e
 *  as linhas falsas ocupam a altura que as verdadeiras vão ocupar. Sem isso, a
 *  tela salta quando o dado chega. */
function TabelaCarregando<T>({
  caption,
  columns,
}: {
  caption: string
  columns: readonly Column<T>[]
}) {
  const elastica = colunaElastica(columns)

  return (
    <div className={styles.scroll} aria-busy="true">
      {/* O anúncio fica FORA da tabela, e a tabela inteira sai da árvore de
          acessibilidade. Marcar linha a linha com aria-hidden deixaria o
          cabeçalho exposto sobre um corpo vazio — o leitor de tela anunciaria
          "tabela com 0 linhas", que é falso: os dados estão a caminho. */}
      <p className="sr-only" role="status">
        Carregando {caption.toLowerCase()}
      </p>
      <table className={styles.table} aria-hidden="true">
        <thead>
          <tr>
            {columns.map((coluna) => (
              <th
                key={coluna.key}
                scope="col"
                data-align={coluna.align ?? 'start'}
                data-width={coluna.width ?? 'auto'}
                data-flex={coluna.key === elastica ? '' : undefined}
                data-hide={coluna.hideBelow}
              >
                {coluna.headerHidden ? '' : coluna.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {[0, 1, 2].map((indice) => (
            <tr key={indice}>
              {columns.map((coluna) => (
                <td
                  key={coluna.key}
                  data-align={coluna.align ?? 'start'}
                  data-width={coluna.width ?? 'auto'}
                  data-flex={coluna.key === elastica ? '' : undefined}
                  data-hide={coluna.hideBelow}
                >
                  <Skeleton width={coluna.align === 'end' ? '4.5rem' : '9rem'} height="1rem" />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
