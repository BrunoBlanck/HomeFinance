import type {
  Account,
  CategoryTree,
  KeywordImportItem,
  KeywordImportNewCategory,
  KeywordImportReport,
} from '@/api/types'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable, type RowGroup } from '@/components/DataTable/DataTable'
import { AlertIcon } from '@/components/icons/AlertIcon'
import {
  categoriasACriar,
  categoriasMescladas,
  citarPalavras,
  entradasDeCategoria,
  type GrupoDeConta,
  gruposDeConta,
  type LinhaDeFora,
  MOTIVO_DO_DESFECHO,
  MUITAS_CATEGORIAS,
  NATUREZA_NA_FRASE,
  nomeDoDono,
  oQueFicaDeFora,
  type PalavraDeConta,
  rotuloDaCaixa,
} from '../importacao'
import styles from './BlocosDaPrevia.module.css'

/** As colunas do bloco A, exportadas porque o esqueleto de carregamento da
 *  seção usa exatamente elas: a tabela falsa tem a forma da verdadeira. */
export const COLUNAS_DE_CRIAR: readonly Column<KeywordImportNewCategory>[] = [
  { key: 'criar', header: 'Criar', headerHidden: true, width: 'min', render: () => null },
  { key: 'categoria', header: 'Categoria', render: () => null },
  { key: 'palavras', header: 'Palavras', hideBelow: 'sm', render: () => null },
]

type Props = {
  relatorio: KeywordImportReport
  desmarcadas: ReadonlySet<string>
  onAlternar: (ref: string, marcada: boolean) => void
  onMarcarTodas: (marcar: boolean) => void
  /** A árvore de categorias e a lista de contas já carregadas — para escrever
   *  o NOME do dono de uma palavra recusada por `keyword_taken`. Nunca texto
   *  do JSON. */
  arvore: CategoryTree | undefined
  contas: readonly Account[]
}

/** Os três blocos da prévia e o `<details>` do que fica de fora (spec 0010
 *  §4.3, ordem normativa): estrutura nova → palavras de conta → palavras de
 *  categoria → o que não entra.
 *
 *  **A ordem é a do risco, e a forma de cada bloco é a da decisão que ele
 *  pede.** O bloco A vem primeiro porque é o único que CRIA e o único com
 *  controle. O B tem número e uma frase de atenção, mas nada para desmarcar —
 *  o contrato só cobre categoria, e a saída é a frase: apague a linha do JSON
 *  e confira de novo. O C não é tabela, é `<dl>`: três tabelas iguais
 *  empilhadas viram parede cinza, e a `<dl>` diz "aqui é volume, não
 *  decisão". O `<details>` fechado não tem controle nenhum — nem desabilitado.
 *
 *  **Cor:** `--warning`/`--warning-ink` aparecem em UM lugar — o filete e o
 *  ícone da linha de atenção do bloco B. Nada mais aqui é cromático, e o
 *  aceite é `filter: grayscale(1)` sem perder decisão nenhuma: a linha de
 *  atenção continua dita pela frase e pelo peso do número. */
export function BlocosDaPrevia({
  relatorio,
  desmarcadas,
  onAlternar,
  onMarcarTodas,
  arvore,
  contas,
}: Props) {
  const criar = categoriasACriar(relatorio)
  const contasQueEntram = gruposDeConta(relatorio)
  const categoriasQueEntram = entradasDeCategoria(relatorio)
  const mescladas = categoriasMescladas(relatorio)
  const fora = oQueFicaDeFora(relatorio, (recusa, tipo) => nomeDoDono(recusa, tipo, arvore, contas))
  const universo = relatorio.totals.periodTransactions

  return (
    <>
      {criar.length > 0 ? (
        <BlocoEstruturaNova
          entradas={criar}
          desmarcadas={desmarcadas}
          onAlternar={onAlternar}
          onMarcarTodas={onMarcarTodas}
        />
      ) : null}

      {contasQueEntram.length > 0 ? (
        <BlocoPalavrasDeConta grupos={contasQueEntram} universo={universo} />
      ) : null}

      {categoriasQueEntram.length > 0 || mescladas.length > 0 ? (
        <BlocoPalavrasDeCategoria itens={categoriasQueEntram} mescladas={mescladas} />
      ) : null}

      <ForaDetails jaEstavam={fora.jaEstavam} recusadas={fora.recusadas} />
    </>
  )
}

/** Bloco A — **Categorias a criar**. Checkbox NATIVO marcado por padrão, com
 *  o caminho inteiro e a contagem no nome acessível. Desmarcar cancela a
 *  categoria e as palavras que nasceriam com ela — o rótulo do botão de
 *  confirmar, lá embaixo, é onde isso fica visível (aceite 48). */
function BlocoEstruturaNova({
  entradas,
  desmarcadas,
  onAlternar,
  onMarcarTodas,
}: {
  entradas: readonly KeywordImportNewCategory[]
  desmarcadas: ReadonlySet<string>
  onAlternar: (ref: string, marcada: boolean) => void
  onMarcarTodas: (marcar: boolean) => void
}) {
  const marcadas = entradas.filter((entrada) => !desmarcadas.has(entrada.ref)).length
  const todasMarcadas = marcadas === entradas.length

  const colunas: readonly Column<KeywordImportNewCategory>[] = [
    {
      key: 'criar',
      header: 'Criar',
      headerHidden: true,
      width: 'min',
      render: (entrada) => (
        <input
          type="checkbox"
          className={styles.marca}
          checked={!desmarcadas.has(entrada.ref)}
          aria-label={rotuloDaCaixa(entrada)}
          onChange={(evento) => onAlternar(entrada.ref, evento.target.checked)}
        />
      ),
    },
    {
      key: 'categoria',
      header: 'Categoria',
      render: (entrada) => (
        <>
          {/* O grupo recua, a folha salta: é a folha que nasce. */}
          <span className={styles.caminho}>
            <span className={styles.grupo}>{entrada.group}</span>
            <span className={styles.folha}> &gt; {entrada.name}</span>
          </span>
          {entrada.groupIsNew && entrada.kind ? (
            <span className={styles.nota}>
              Grupo novo · natureza: {NATUREZA_NA_FRASE[entrada.kind]}
            </span>
          ) : null}
          {/* Abaixo de 40rem a coluna Palavras some, e as palavras reaparecem
              aqui — esconder a coluna só é honesto se o dado volta. */}
          {entrada.add.length > 0 ? (
            <span className={styles.secundaria}>{citarPalavras(entrada.add)}</span>
          ) : null}
        </>
      ),
    },
    {
      key: 'palavras',
      header: 'Palavras',
      hideBelow: 'sm',
      render: (entrada) =>
        entrada.add.length > 0 ? (
          <span className={styles.citadas}>{citarPalavras(entrada.add)}</span>
        ) : (
          <span className={styles.ausente}>
            <span aria-hidden="true">—</span>
            <span className="sr-only">sem palavra-chave</span>
          </span>
        ),
    },
  ]

  return (
    <div className={styles.bloco}>
      <div className={styles.cabecalhoDoBloco}>
        <h3 className={styles.tituloDoBloco}>Categorias a criar · {entradas.length}</h3>
        <Button size="sm" variant="quiet" onClick={() => onMarcarTodas(!todasMarcadas)}>
          {todasMarcadas ? 'Desmarcar todas' : 'Marcar todas'}
        </Button>
      </div>
      <p className={styles.apoio}>
        Marcadas entram com as palavras delas. Desmarcar uma cancela a categoria e as palavras que
        nasceriam com ela.
        {entradas.length >= MUITAS_CATEGORIAS
          ? ` São ${entradas.length} categorias novas — a IA costuma criar demais. Confira se alguma já existe com outro nome.`
          : ''}
      </p>
      <DataTable
        caption="Categorias a criar"
        columns={colunas}
        rows={entradas}
        rowKey={(entrada) => entrada.ref}
        // A caixa é o portador; a tinta apagada é só reforço.
        rowAttrs={(entrada) => ({
          'data-desmarcada': desmarcadas.has(entrada.ref) ? 'true' : undefined,
        })}
      />
    </div>
  )
}

/** Bloco B — **Palavras-chave de conta**, com o impacto medido (§4.4).
 *
 *  `DataTable` agrupada por conta (`RowGroup`, `Nubank · 3`), **uma linha por
 *  palavra** com o número de `impact.byKeyword`, em impacto decrescente
 *  dentro de cada conta — a palavra genérica é a primeira que o olho
 *  encontra, e é ela que chama atenção, não a conta. O número é texto
 *  (`87 de 212`, `tabular-nums`), nunca barra, anel ou semáforo. Nada aqui é
 *  desmarcável (o contrato só cobre categoria); a saída é a frase: apague a
 *  palavra do JSON e confira de novo. */
function BlocoPalavrasDeConta({
  grupos,
  universo,
}: {
  grupos: readonly GrupoDeConta[]
  universo: number
}) {
  const quantas = grupos.reduce((soma, grupo) => soma + grupo.palavras.length, 0)

  const colunas: readonly Column<PalavraDeConta>[] = [
    {
      key: 'palavra',
      header: 'Palavra',
      render: (linha) => (
        <>
          <span className={styles.citadas}>{citarPalavras([linha.palavra])}</span>
          {/* Abaixo de 40rem a coluna do impacto some e o número reaparece
              aqui: a frase de atenção precisa da largura inteira da moldura,
              e um "87 de 212" escondido seria o dado que mais importa
              sumindo. */}
          <span className={styles.secundaria}>
            {universo === 0
              ? 'sem lançamentos no período para medir'
              : `${linha.candidatos} de ${universo} candidatos a transferência`}
          </span>
          {linha.atencao ? (
            <span className={styles.atencao}>
              <AlertIcon size={14} />
              <span>{fraseDeAtencao(linha.palavra, linha.candidatos, universo)}</span>
            </span>
          ) : null}
        </>
      ),
    },
    {
      key: 'impacto',
      header: 'Candidatos a transferência',
      align: 'end',
      width: 'min',
      hideBelow: 'sm',
      render: (linha) =>
        // Janela sem lançamento: "0 de 0" é verdadeiro e não diz nada. A frase
        // diz o que aconteceu — não houve o que medir.
        universo === 0 ? (
          <span className={styles.ausente}>sem lançamentos no período</span>
        ) : (
          <span className={styles.impacto}>
            <span className={styles.numero} data-atencao={linha.atencao ? 'true' : undefined}>
              {linha.candidatos}
            </span>{' '}
            de {universo}
            <span className="sr-only"> lançamentos do período</span>
          </span>
        ),
    },
  ]

  const rowGroups: RowGroup<PalavraDeConta>[] = grupos.map((grupo) => ({
    key: grupo.chave,
    label: grupo.rotulo,
    rows: grupo.palavras,
  }))

  return (
    <div className={styles.bloco}>
      <div className={styles.cabecalhoDoBloco}>
        <h3 className={styles.tituloDoBloco}>Palavras-chave de conta · {quantas}</h3>
      </div>
      <p className={styles.apoio}>
        Palavra-chave de conta é o gatilho de transferência interna: quando ela casa com a
        descrição, o lançamento passa a ser candidato a virar transferência.
      </p>
      <div className={styles.contas}>
        <DataTable
          caption="Palavras-chave de conta e o impacto no período"
          columns={colunas}
          groups={rowGroups}
          rowKey={(linha) => linha.chave}
          rowAttrs={(linha) => ({ 'data-atencao': linha.atencao ? 'true' : undefined })}
        />
      </div>
    </div>
  )
}

/** A frase da linha de atenção — cita a palavra, porque é ela o problema. */
function fraseDeAtencao(palavra: string, candidatos: number, universo: number): string {
  return `Palavra genérica: ${citarPalavras([palavra])} casa com ${candidatos} dos ${universo} lançamentos do período. Para deixá-la de fora, apague-a do JSON e confira de novo.`
}

/** Bloco C — **Palavras-chave de categoria**, como `<dl>`: o caminho em `dt`,
 *  as palavras citadas em `dd`. As entradas `merged_into_existing` de
 *  `newCategories` entram aqui (não são criação) com a nota de que a
 *  categoria já existia. */
function BlocoPalavrasDeCategoria({
  itens,
  mescladas,
}: {
  itens: ReadonlyArray<{ indice: number; item: KeywordImportItem }>
  mescladas: readonly KeywordImportNewCategory[]
}) {
  return (
    <div className={styles.bloco}>
      <div className={styles.cabecalhoDoBloco}>
        <h3 className={styles.tituloDoBloco}>
          Palavras-chave de categoria · {itens.length + mescladas.length}
        </h3>
      </div>
      <dl className={styles.lista}>
        {itens.map(({ indice, item }) => (
          // Chave pelo índice no relatório: o `id` pode vir `""`.
          <div className={styles.linha} key={`item-${indice}`}>
            <dt className={styles.termo}>
              <Caminho caminho={item.name ?? '—'} />
            </dt>
            <dd className={styles.definicao}>{citarPalavras(item.added)}</dd>
          </div>
        ))}
        {mescladas.map((entrada) => (
          <div className={styles.linha} key={entrada.ref}>
            <dt className={styles.termo}>
              <Caminho caminho={`${entrada.group} > ${entrada.name}`} />
            </dt>
            <dd className={styles.definicao}>
              {citarPalavras(entrada.add)}
              <span className={styles.nota}>{MOTIVO_DO_DESFECHO.merged_into_existing}</span>
            </dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

/** `Grupo > Folha` com o grupo recuado e a folha em tinta cheia. O nome vem
 *  do servidor já no formato do caminho; sem ` > ` (um grupo folha), vai
 *  inteiro em tinta cheia. */
function Caminho({ caminho }: { caminho: string }) {
  const separador = caminho.indexOf(' > ')
  if (separador === -1) return <span className={styles.folha}>{caminho}</span>
  return (
    <span className={styles.caminho}>
      <span className={styles.grupo}>{caminho.slice(0, separador)}</span>
      <span className={styles.folha}> &gt; {caminho.slice(separador + 3)}</span>
    </span>
  )
}

/** `<details>` fechado — **Ver o que não entra**. Dois grupos, sem nenhum
 *  controle: reimportar o mesmo JSON é inofensivo (o que já estava lá é
 *  pulado), e o que foi recusado volta com o motivo, sem derrubar o resto. */
function ForaDetails({
  jaEstavam,
  recusadas,
}: {
  jaEstavam: readonly LinhaDeFora[]
  recusadas: readonly LinhaDeFora[]
}) {
  const total = jaEstavam.length + recusadas.length
  if (total === 0) return null

  const colunas: readonly Column<LinhaDeFora>[] = [
    {
      key: 'palavra',
      header: 'Palavra',
      width: 'min',
      render: (linha) => (
        <>
          <span className={styles.citadas}>{linha.palavra}</span>
          {/* O item reaparece aqui abaixo de 40rem, onde a coluna dele some. */}
          {linha.item !== null ? <span className={styles.secundaria}>{linha.item}</span> : null}
        </>
      ),
    },
    {
      key: 'item',
      header: 'Item',
      hideBelow: 'sm',
      render: (linha) =>
        linha.item === null ? (
          <span className={styles.ausente}>
            <span aria-hidden="true">—</span>
            <span className="sr-only">item não encontrado</span>
          </span>
        ) : (
          <span className={styles.item}>{linha.item}</span>
        ),
    },
    {
      key: 'motivo',
      header: 'Motivo',
      render: (linha) => <span className={styles.motivo}>{linha.motivo}</span>,
    },
  ]

  const grupos: RowGroup<LinhaDeFora>[] = []
  if (jaEstavam.length > 0) {
    grupos.push({
      key: 'ja-estavam',
      label: `Já estavam lá · ${jaEstavam.length}`,
      description: 'Reimportar o mesmo JSON é inofensivo: palavra que o item já tem é pulada.',
      rows: jaEstavam,
    })
  }
  if (recusadas.length > 0) {
    grupos.push({
      key: 'recusadas',
      label: `Recusadas · ${recusadas.length}`,
      description: 'Nada foi gravado para estas. O resto do lote entra normalmente.',
      rows: recusadas,
    })
  }

  return (
    <details className={styles.fora}>
      <summary className={styles.resumo}>Ver o que não entra · {total}</summary>
      <div className={styles.tabelaDeFora}>
        <DataTable
          caption="O que não entra"
          columns={colunas}
          groups={grupos}
          rowKey={(linha) => linha.chave}
        />
      </div>
    </details>
  )
}
