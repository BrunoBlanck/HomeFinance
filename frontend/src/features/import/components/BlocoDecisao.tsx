import type { ImportRow } from '@/api/types'
import { Badge } from '@/components/Badge/Badge'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable, type RowGroup } from '@/components/DataTable/DataTable'
import { Panel } from '@/components/Panel/Panel'
import { BLOCO_DO_STATUS, DESCRICAO_DO_GRUPO, ORDEM_DOS_STATUS, tituloDoGrupo } from '../lexico'
import styles from './BlocoDecisao.module.css'
import {
  CelulaData,
  CelulaDeCategoria,
  CelulaDeDecisao,
  CelulaDescricaoDaLinha,
  CelulaValor,
  type ContextoDaRevisao,
} from './CelulasDaRevisao'

type Props = {
  linhas: readonly ImportRow[]
  contexto: ContextoDaRevisao
  onDesfazer: () => void
  /** Ids de linhas marcadas como transferência sem conta de destino. */
  faltandoDestino: ReadonlySet<string>
}

/** Bloco 1 — **Precisam da sua decisão**.
 *
 *  É a tela onde a pessoa impede que uma linha duplicada entre, e o desenho
 *  inteiro existe para uma pergunta só por linha: *isso entra?*
 *
 *  O controle é um `<select>` nativo com **palavras**, não uma etiqueta
 *  colorida. Essa é a jogada central do bloco: o status não vira rótulo
 *  ("possível duplicado"), vira o texto das opções — "Não importar (é a mesma)"
 *  e "Importar assim mesmo (é outra)". A pessoa lê a consequência no momento de
 *  escolher, e não precisa aprender vocabulário nenhum.
 *
 *  **Nenhuma marcação aqui usa cor.** A distinção vem de posição (este bloco),
 *  palavra do grupo, frase de evidência na linha e forma do controle. A única
 *  cor cromática do bloco é o contador no título — `--warning` é "algo
 *  esperando decisão sua", que é literalmente o que este bloco é. */
export function BlocoDecisao({ linhas, contexto, onDesfazer, faltandoDestino }: Props) {
  if (linhas.length === 0) return null

  const grupos: RowGroup<ImportRow>[] = ORDEM_DOS_STATUS.filter(
    (status) => BLOCO_DO_STATUS[status] === 'decisao',
  )
    .map((status) => ({ status, rows: linhas.filter((linha) => linha.status === status) }))
    .filter((grupo) => grupo.rows.length > 0)
    .map(({ status, rows }) => ({
      key: status,
      label: tituloDoGrupo(status, rows.length),
      ...(DESCRICAO_DO_GRUPO[status] ? { description: DESCRICAO_DO_GRUPO[status] } : {}),
      rows,
    }))

  const alguemMudou = linhas.some((linha) => contexto.escolhas[linha.id] !== undefined)

  const colunas: readonly Column<ImportRow>[] = [
    {
      key: 'data',
      header: 'Data',
      width: 'min',
      render: (linha) => <CelulaData linha={linha} />,
    },
    {
      key: 'descricao',
      header: 'Descrição',
      render: (linha) => <CelulaDescricaoDaLinha linha={linha} contexto={contexto} />,
    },
    {
      key: 'valor',
      header: 'Valor',
      align: 'end',
      width: 'min',
      render: (linha) => <CelulaValor linha={linha} />,
    },
    {
      key: 'decisao',
      header: 'Decisão',
      width: 'min',
      render: (linha) => (
        <CelulaDeDecisao
          linha={linha}
          contexto={contexto}
          faltaDestino={faltandoDestino.has(linha.id)}
          placeholderDaConta="Escolha o cartão"
        />
      ),
    },
    {
      key: 'categoria',
      header: 'Categoria',
      width: 'min',
      hideBelow: 'sm',
      render: (linha) => <CelulaDeCategoria linha={linha} contexto={contexto} />,
    },
  ]

  return (
    <Panel
      title="Precisam da sua decisão"
      padding="none"
      actions={
        <div className={styles.cabecalho}>
          <Badge tone="warning">{linhas.length}</Badge>
          {/* Só aparece depois que alguma linha mudou: é um DESFAZER, não um
              atalho para ignorar tudo de uma vez. Oferecido antes, ele
              convidaria a pular justamente o trabalho deste bloco. */}
          {alguemMudou ? (
            <Button size="sm" variant="quiet" onClick={onDesfazer}>
              Decidir tudo como "não importar"
            </Button>
          ) : null}
        </div>
      }
    >
      <p className={styles.apoio}>
        Barramos estas linhas porque elas podem virar dinheiro contado duas vezes. Nada aqui entra
        sem você mandar.
      </p>
      <DataTable
        caption="Linhas que precisam da sua decisão, agrupadas pelo motivo"
        columns={colunas}
        groups={grupos}
        rowKey={(linha) => linha.id}
      />
    </Panel>
  )
}
