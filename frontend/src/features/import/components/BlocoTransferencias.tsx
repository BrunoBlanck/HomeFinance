import type { ImportRow } from '@/api/types'
import { Badge } from '@/components/Badge/Badge'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable, type RowGroup } from '@/components/DataTable/DataTable'
import { Panel } from '@/components/Panel/Panel'
import { acaoEfetiva } from '../decisoes'
import { BLOCO_DO_STATUS, DESCRICAO_DO_GRUPO, ORDEM_DOS_STATUS, tituloDoGrupo } from '../lexico'
import styles from './BlocoTransferencias.module.css'
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
  /** Marca `transfer` (com a contraparte sugerida) em TODAS as
   *  `transferencia_interna` deste bloco — e em nenhuma outra linha. */
  onAceitarTodas: () => void
  /** Volta todas as `transferencia_interna` para "não importar". */
  onDesfazerAceite: () => void
  /** Ids de linhas marcadas como transferência sem conta de destino. */
  faltandoDestino: ReadonlySet<string>
}

/** Bloco 2 — **Transferências detectadas**.
 *
 *  Fica entre "Precisam da sua decisão" e "Prontas para importar" porque a
 *  ordem dos blocos é um gradiente de trabalho: perguntas abertas (bloco 1) →
 *  perguntas **com resposta proposta** (este) → conferência → auditoria.
 *
 *  Dois grupos, e a diferença entre eles é o que a pessoa precisa fazer:
 *
 *  - *Parece transferência* — a descrição bate com a palavra-chave de outra
 *    conta da casa. Não entra sem confirmar; a contraparte sugerida mora **no
 *    texto da opção** do `<select>` ("Registrar como transferência para
 *    Nubank"), e o botão do cabeçalho aceita todas de uma vez;
 *  - *Já registrada como transferência* — a outra conta já registrou o par.
 *    O default é `link`, que não cria lançamento: só marca esta linha como
 *    importada, para ela não voltar como nova.
 *
 *  O botão é `secondary`, e não `quiet` como "Marcar todas": aqui ele **é** o
 *  caminho principal do bloco, não um atalho redundante. Ele some quando não há
 *  `transferencia_interna` (só `link` não pede aceite). Nenhuma `Badge`
 *  "detectada": o status é posição, palavra do grupo, evidência e opção do
 *  select — e a pontuação da correspondência é texto na evidência. */
export function BlocoTransferencias({
  linhas,
  contexto,
  onAceitarTodas,
  onDesfazerAceite,
  faltandoDestino,
}: Props) {
  if (linhas.length === 0) return null

  const grupos: RowGroup<ImportRow>[] = ORDEM_DOS_STATUS.filter(
    (status) => BLOCO_DO_STATUS[status] === 'transferencias',
  )
    .map((status) => ({ status, rows: linhas.filter((linha) => linha.status === status) }))
    .filter((grupo) => grupo.rows.length > 0)
    .map(({ status, rows }) => ({
      key: status,
      label: tituloDoGrupo(status, rows.length),
      ...(DESCRICAO_DO_GRUPO[status] ? { description: DESCRICAO_DO_GRUPO[status] } : {}),
      rows,
    }))

  const internas = linhas.filter((linha) => linha.status === 'transferencia_interna')
  const todasAceitas =
    internas.length > 0 &&
    internas.every((linha) => acaoEfetiva(linha, contexto.escolhas) === 'transfer')

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
          placeholderDaConta="Escolha a conta"
        />
      ),
    },
    {
      key: 'categoria',
      header: 'Categoria',
      width: 'min',
      hideBelow: 'sm',
      // Só com ação `import` ("não é transferência") a célula vira select;
      // transferência e vínculo nunca têm categoria — a própria célula sabe.
      render: (linha) => <CelulaDeCategoria linha={linha} contexto={contexto} />,
    },
  ]

  return (
    <Panel
      title="Transferências detectadas"
      padding="none"
      actions={
        internas.length > 0 ? (
          <div className={styles.cabecalho}>
            <Badge tone="warning">{internas.length}</Badge>
            {todasAceitas ? (
              <Button size="sm" variant="quiet" onClick={onDesfazerAceite}>
                Desfazer o aceite de todas
              </Button>
            ) : (
              <Button size="sm" variant="secondary" onClick={onAceitarTodas}>
                {internas.length === 1
                  ? 'Aceitar a transferência sugerida'
                  : `Aceitar as ${internas.length} transferências sugeridas`}
              </Button>
            )}
          </div>
        ) : undefined
      }
    >
      <p className={styles.apoio}>
        A descrição bate com a palavra-chave de outra conta da casa. Registrada como transferência,
        a linha não conta como receita nem como despesa — o dinheiro só mudou de conta.
      </p>
      <DataTable
        caption="Linhas que parecem transferências entre as suas contas, agrupadas pela situação"
        columns={colunas}
        groups={grupos}
        rowKey={(linha) => linha.id}
      />
    </Panel>
  )
}
