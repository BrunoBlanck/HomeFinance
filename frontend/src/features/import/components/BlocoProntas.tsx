import type { ImportRow } from '@/api/types'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable } from '@/components/DataTable/DataTable'
import { Panel } from '@/components/Panel/Panel'
import { acaoEfetiva } from '../decisoes'
import styles from './BlocoProntas.module.css'
import {
  CelulaData,
  CelulaDeCategoria,
  CelulaDescricaoDaLinha,
  CelulaValor,
  type ContextoDaRevisao,
  rotuloDaLinha,
} from './CelulasDaRevisao'

type Props = {
  linhas: readonly ImportRow[]
  contexto: ContextoDaRevisao
  onMarcarTodas: () => void
  onDesmarcarTodas: () => void
}

/** Bloco 3 — **Prontas para importar**.
 *
 *  Nenhuma colisão com o que já existe: aqui não há pergunta a responder, só
 *  conferência. Por isso o controle é um checkbox e não um `<select>` — a forma
 *  do controle é um dos portadores que dizem em que situação a linha está, e
 *  ela muda de bloco para bloco de propósito.
 *
 *  A categoria de cada linha já vem com a sugestão das palavras-chave
 *  selecionada no `<select>`, e a linha de proveniência embaixo diz de onde
 *  veio ("88% · «supermercado»"). Numa linha sem sugestão, escolher uma
 *  categoria à mão oferece as palavras da descrição como fichas de aprender.
 *
 *  **Aberto, nunca colapsado.** São as linhas que a pessoa está prestes a
 *  gravar no banco; escondê-las atrás de um `<details>` pouparia rolagem e
 *  custaria a única chance de alguém notar a linha errada antes de ela virar
 *  dinheiro registrado. */
export function BlocoProntas({ linhas, contexto, onMarcarTodas, onDesmarcarTodas }: Props) {
  if (linhas.length === 0) return null

  const { escolhas, onEscolher } = contexto
  const marcadas = linhas.filter((linha) => acaoEfetiva(linha, escolhas) === 'import').length
  const todasMarcadas = marcadas === linhas.length

  const colunas: readonly Column<ImportRow>[] = [
    {
      key: 'importar',
      header: 'Importar',
      headerHidden: true,
      width: 'min',
      render: (linha) => {
        const vaiEntrar = acaoEfetiva(linha, escolhas) === 'import'
        return (
          <input
            type="checkbox"
            className={styles.marca}
            checked={vaiEntrar}
            // Nunca só "Importar": cinquenta e nove caixas com o mesmo nome
            // deixam quem navega por lista de controles sem saber qual é qual.
            aria-label={`Importar ${rotuloDaLinha(linha)}`}
            onChange={(evento) =>
              onEscolher(linha.id, {
                acao: evento.target.checked ? 'import' : 'skip',
                // Desmarcar e marcar de novo não pode apagar a categoria que a
                // pessoa escolheu (nem o "sem categoria" que ela pediu).
                categoriaId: escolhas[linha.id]?.categoriaId,
              })
            }
          />
        )
      },
    },
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
      key: 'categoria',
      header: 'Categoria',
      width: 'min',
      hideBelow: 'sm',
      render: (linha) => <CelulaDeCategoria linha={linha} contexto={contexto} />,
    },
  ]

  return (
    <Panel
      title="Prontas para importar"
      padding="none"
      actions={
        <Button
          size="sm"
          variant="quiet"
          onClick={todasMarcadas ? onDesmarcarTodas : onMarcarTodas}
        >
          {todasMarcadas ? 'Desmarcar todas' : 'Marcar todas'}
        </Button>
      }
    >
      <p className={styles.apoio}>
        Nenhuma colisão com o que já existe. Desmarque o que não quiser.
      </p>
      <DataTable
        caption="Linhas prontas para importar"
        columns={colunas}
        rows={linhas}
        rowKey={(linha) => linha.id}
      />
    </Panel>
  )
}
