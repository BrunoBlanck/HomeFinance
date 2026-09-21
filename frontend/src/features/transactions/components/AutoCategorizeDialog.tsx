import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import type { AutoCategorizeResult, AutoCategorizeUnmatchedReason } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable, type RowGroup } from '@/components/DataTable/DataTable'
import { Dialog } from '@/components/Dialog/Dialog'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { useToast } from '@/components/Toast/Toast'
import { messageForError } from '@/lib/errors'
import { citarPalavra } from '@/lib/keywords'
import { mesPorExtensoCapitalizado, nomeDoMes } from '@/lib/month'
import { autoCategorize } from '../api/transactions'
import styles from './AutoCategorizeDialog.module.css'

type Props = {
  open: boolean
  /** `AAAA-MM` — o mês da tela, que é o mês de competência do pedido. */
  mes: string
  onClose: () => void
}

/** Teto do contrato: as listas da prévia param em 500, as contagens não. */
const TETO_LISTADO = 500

/** A PALAVRA de cada motivo (docs/DESIGN.md, E2c (e)). `Record` exaustivo: um
 *  motivo novo no contrato vira erro de compilação aqui, não "below_threshold"
 *  cru na tela. */
const PALAVRA_DO_MOTIVO: Record<AutoCategorizeUnmatchedReason, string> = {
  below_threshold: 'abaixo de 80%',
  ambiguous: 'empate entre categorias',
}

const DESCRICAO_DOS_SEM_CATEGORIA =
  'Abaixo de 80% de semelhança o app não arrisca; num empate entre duas categorias, também não. Uma palavra-chave mais específica resolve os dois casos.'

/** Diálogo "Categorizar automaticamente" (spec 0005 §4.3).
 *
 *  Abre já pedindo a **prévia** (`dryRun: true`) e só grava quando a pessoa
 *  confirma (`dryRun: false`). O servidor recalcula na confirmação em vez de
 *  confiar na prévia — por isso o toast mostra o número que **voltou**, não o
 *  que a prévia prometeu: se alguém categorizou à mão no meio, o número real é
 *  menor, e o toast diz isso.
 *
 *  A prévia é uma `useMutation`, não uma `useQuery`, de propósito: cada chamada
 *  gasta cota do rate limit da casa, e uma query refazendo o pedido no foco da
 *  janela queimaria a cota sem ninguém pedir. */
export function AutoCategorizeDialog({ open, mes, onClose }: Props) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const toast = useToast()

  const previa = useMutation({
    mutationFn: () => autoCategorize({ month: mes, dryRun: true }),
  })

  const confirmar = useMutation({
    mutationFn: () => autoCategorize({ month: mes, dryRun: false }),
    onSuccess: async (resultado) => {
      toast.sucesso(fraseDoToast(resultado.categorized))
      onClose()
      await queryClient.invalidateQueries({ queryKey: ['transactions'] })
    },
  })

  // Abrir é pedir a prévia. `mutate` e `reset` são estáveis por contrato do
  // TanStack Query, então o efeito roda uma vez por abertura. O mês não entra
  // nas dependências porque não muda com o diálogo aberto: o `<dialog>` modal
  // deixa o seletor de mês da casca inerte.
  const { mutate: pedirPrevia } = previa
  const { reset: limparConfirmacao } = confirmar
  useEffect(() => {
    if (!open) return
    limparConfirmacao()
    pedirPrevia()
  }, [open, pedirPrevia, limparConfirmacao])

  // Só entra no DOM a partir da primeira abertura — e depois FICA, para o
  // `<dialog>` fechar com animação e devolver o foco a quem o abriu. Antes
  // disso, a tela não carrega um diálogo inteiro (título, descrição, tabela)
  // que ninguém pediu. Mesmo padrão do `ExcluirLancamentoDialog`.
  const [jaAbriu, setJaAbriu] = useState(open)
  useEffect(() => {
    if (open) setJaAbriu(true)
  }, [open])

  const resultado = previa.data
  const carregando = open && (previa.isIdle || previa.isPending)
  const nomeDoMesAtual = nomeDoMes(mes)
  const podeConfirmar = resultado !== undefined && resultado.categorized > 0 && !previa.isPending

  function irParaCategorias() {
    onClose()
    void navigate({
      to: '/categorias',
      search: (anterior) => (anterior.mes ? { mes: anterior.mes } : {}),
    })
  }

  if (!jaAbriu) return null

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Categorizar automaticamente"
      description={`${mesPorExtensoCapitalizado(mes)} · aplica as palavras-chave das categorias aos lançamentos sem categoria. O que já tem categoria não muda.`}
      footer={
        <div className={styles.rodape}>
          <Button variant="quiet" onClick={onClose}>
            Cancelar
          </Button>
          {/* Nunca `disabled`: o botão continua focável e o rótulo explica por
              que não há o que confirmar. */}
          <Button
            variant="primary"
            loading={confirmar.isPending}
            aria-disabled={podeConfirmar ? undefined : true}
            onClick={() => {
              if (podeConfirmar) confirmar.mutate()
            }}
          >
            {rotuloDoConfirmar(resultado)}
          </Button>
        </div>
      }
    >
      <div className={styles.corpo}>
        {confirmar.isError ? (
          <Alert tone="error" title="Não foi possível categorizar.">
            {messageForError(confirmar.error)}
          </Alert>
        ) : null}

        {carregando ? (
          <>
            <p className={styles.resumo} role="status">
              Conferindo as palavras-chave de {nomeDoMesAtual}…
            </p>
            <div className={styles.tabela}>
              <DataTable
                caption="Prévia da categorização"
                columns={COLUNAS}
                rows={[]}
                rowKey={(linha) => linha.id}
                loading
              />
            </div>
          </>
        ) : previa.isError ? (
          <Alert
            tone="error"
            title="Não foi possível conferir as palavras-chave."
            action={<Button onClick={() => previa.mutate()}>Tentar de novo</Button>}
          >
            {messageForError(previa.error)}
          </Alert>
        ) : resultado ? (
          <Previa resultado={resultado} onIrParaCategorias={irParaCategorias} />
        ) : null}
      </div>
    </Dialog>
  )
}

// -------------------------------------------------------------- pedaços

function Previa({
  resultado,
  onIrParaCategorias,
}: {
  resultado: AutoCategorizeResult
  onIrParaCategorias: () => void
}) {
  const total = resultado.categorized + resultado.unmatched
  const grupoDosSemCategoria = montarGrupoDosSemCategoria(resultado)

  if (resultado.categorized === 0) {
    return (
      <>
        <EmptyState
          title="Nenhum lançamento receberia categoria."
          description={descricaoDoVazio(resultado.unmatched)}
          action={
            <Button variant="secondary" onClick={onIrParaCategorias}>
              Ir para categorias
            </Button>
          }
        />
        {/* <details> nativo: fechado, é auditoria, não trabalho — a lista dos
            motivos só interessa a quem quer entender por que nada casou. */}
        {grupoDosSemCategoria ? (
          <details className={styles.detalhes}>
            <summary className={styles.sumario}>
              {resultado.unmatched === 1
                ? 'Ver o lançamento e o motivo'
                : `Ver os ${resultado.unmatched} lançamentos e o motivo`}
            </summary>
            <div className={styles.tabela}>
              <DataTable
                caption="Lançamentos que continuam sem categoria e o motivo"
                columns={COLUNAS}
                groups={[grupoDosSemCategoria]}
                rowKey={(linha) => linha.id}
              />
            </div>
          </details>
        ) : null}
      </>
    )
  }

  const grupos: RowGroup<LinhaDaPrevia>[] = [
    {
      key: 'recebem',
      label: `Recebem categoria · ${resultado.categorized}`,
      rows: comCorte(
        resultado.items.map(
          (item): LinhaDaPrevia => ({
            tipo: 'recebe',
            id: item.id,
            descricao: item.description,
            categoria: item.categoryName,
            pontuacao: item.matchScore,
            palavra: item.matchedKeyword,
          }),
        ),
        resultado.categorized,
      ),
    },
  ]
  if (grupoDosSemCategoria) grupos.push(grupoDosSemCategoria)

  return (
    <>
      <p className={styles.resumo} role="status">
        {resultado.categorized === 1
          ? `1 de ${total} recebe categoria.`
          : `${resultado.categorized} de ${total} recebem categoria.`}
      </p>
      <div className={styles.tabela}>
        <DataTable
          caption="Prévia da categorização"
          columns={COLUNAS}
          groups={grupos}
          rowKey={(linha) => linha.id}
        />
      </div>
    </>
  )
}

// --------------------------------------------------------------- tabela

type LinhaDaPrevia =
  | {
      tipo: 'recebe'
      id: string
      descricao: string
      categoria: string
      pontuacao: number
      palavra: string
    }
  | { tipo: 'continua'; id: string; descricao: string; motivo: AutoCategorizeUnmatchedReason }
  /** Última linha de um grupo cortado em 500: só a frase, sem dado. */
  | { tipo: 'corte'; id: string }

const COLUNAS: readonly Column<LinhaDaPrevia>[] = [
  {
    key: 'descricao',
    header: 'Descrição',
    render: (linha) => {
      if (linha.tipo === 'corte') {
        return <span className={styles.corte}>Mostrando as primeiras {TETO_LISTADO}.</span>
      }
      const descricao = linha.descricao.trim()
      return (
        <span className={styles.descricao} title={descricao || undefined}>
          {descricao || <span className={styles.semDescricao}>Sem descrição</span>}
        </span>
      )
    },
  },
  {
    key: 'categoria',
    header: 'Categoria',
    width: 'min',
    render: (linha) => {
      if (linha.tipo === 'corte') return null
      if (linha.tipo === 'continua') {
        return <span className={styles.motivo}>{PALAVRA_DO_MOTIVO[linha.motivo]}</span>
      }
      return (
        <span className={styles.celulaDeCategoria}>
          <span className={styles.categoria}>{linha.categoria}</span>
          {/* Proveniência: pontuação é TEXTO em tabular-nums, e 100 não aparece —
              ausência de pontuação significa correspondência exata. */}
          <span className={styles.proveniencia}>
            <span className="sr-only">Sugerida pela palavra-chave </span>
            {linha.pontuacao === 100
              ? citarPalavra(linha.palavra)
              : `${linha.pontuacao}% · ${citarPalavra(linha.palavra)}`}
          </span>
        </span>
      )
    },
  },
]

function montarGrupoDosSemCategoria(
  resultado: AutoCategorizeResult,
): RowGroup<LinhaDaPrevia> | null {
  if (resultado.unmatched === 0) return null
  return {
    key: 'continuam',
    label: `Continuam sem categoria · ${resultado.unmatched}`,
    description: DESCRICAO_DOS_SEM_CATEGORIA,
    rows: comCorte(
      resultado.unmatchedItems.map(
        (item): LinhaDaPrevia => ({
          tipo: 'continua',
          id: item.id,
          descricao: item.description,
          motivo: item.reason,
        }),
      ),
      resultado.unmatched,
    ),
  }
}

/** Acrescenta a linha de corte quando a lista veio menor que a contagem —
 *  é assim que o contrato sinaliza o teto de 500. */
function comCorte(linhas: LinhaDaPrevia[], contagem: number): LinhaDaPrevia[] {
  if (linhas.length >= contagem) return linhas
  return [...linhas, { tipo: 'corte', id: `corte-${linhas.length}` }]
}

// ----------------------------------------------------------------- copy

function rotuloDoConfirmar(resultado: AutoCategorizeResult | undefined): string {
  if (!resultado) return 'Categorizar'
  if (resultado.categorized === 0) return 'Nada a categorizar'
  if (resultado.categorized === 1) return 'Categorizar 1 lançamento'
  return `Categorizar ${resultado.categorized} lançamentos`
}

function descricaoDoVazio(semCategoria: number): string {
  if (semCategoria === 0) return 'Todos os lançamentos deste mês já têm categoria.'
  const destes = semCategoria === 1 ? 'deste lançamento' : `destes ${semCategoria} lançamentos`
  return `As palavras-chave cadastradas não batem com as descrições ${destes}. Cadastre nas categorias o nome do estabelecimento como ele aparece no extrato.`
}

/** O número é o que o SERVIDOR devolveu — nunca o da prévia. */
export function fraseDoToast(categorizados: number): string {
  if (categorizados === 0) return 'Nada foi categorizado — os lançamentos já tinham categoria.'
  if (categorizados === 1) return '1 lançamento categorizado.'
  return `${categorizados} lançamentos categorizados.`
}
