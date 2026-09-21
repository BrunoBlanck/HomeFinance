import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import type { TransferDetectResult, TransferDetectUnpairedReason } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable, type RowGroup } from '@/components/DataTable/DataTable'
import { Dialog } from '@/components/Dialog/Dialog'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { ArrowRightIcon } from '@/components/icons/ArrowRightIcon'
import { MoneyText } from '@/components/MoneyText/MoneyText'
import { useToast } from '@/components/Toast/Toast'
import { dataCurta } from '@/lib/civil'
import { isConflict, messageForError } from '@/lib/errors'
import { citarPalavra } from '@/lib/keywords'
import { mesPorExtenso, mesPorExtensoCapitalizado, nomeDoMes } from '@/lib/month'
import { detectTransfers } from '../api/transfers'
import styles from './DetectTransfersDialog.module.css'

type Props = {
  open: boolean
  /** `AAAA-MM` — o mês da tela, que é o mês de competência do pedido. */
  mes: string
  onClose: () => void
}

/** A PALAVRA de cada motivo. `Record` exaustivo: um motivo novo no contrato
 *  vira erro de compilação aqui, não "no_mirror" cru na tela. */
const PALAVRA_DO_MOTIVO: Record<TransferDetectUnpairedReason, string> = {
  no_mirror: 'sem a outra perna gravada',
}

const DESCRICAO_DOS_SEM_PAR =
  'A descrição bate com uma palavra-chave de conta, mas a outra perna não está gravada em nenhuma conta da casa. Importe o extrato da outra conta e reprocesse.'

/** Diálogo "Reprocessar transferências" (spec 0005 §13, ADR-028).
 *
 *  Abre já pedindo a **prévia** (`dryRun: true`) e só converte quando a pessoa
 *  confirma (`dryRun: false`). O servidor recalcula na confirmação em vez de
 *  confiar na prévia — por isso o toast mostra o número que **voltou**, não o
 *  que a prévia prometeu: se alguém excluiu uma perna no meio, o número real é
 *  menor, e o toast diz isso. Se uma linha mudou entre a leitura e o `UPDATE`
 *  da mesma execução, nada é gravado e chega 409 `CONFLICT` — aqui isso vira
 *  uma ação própria, "Conferir de novo", que refaz a prévia.
 *
 *  Nada aqui leva `--income`/`--expense`: transferência não é receita nem
 *  despesa, e a direção do dinheiro é dita em palavras ("de X para Y"), como em
 *  `/transferencias`.
 *
 *  A prévia é uma `useMutation`, não uma `useQuery`, de propósito: cada chamada
 *  gasta cota do rate limit da casa, e uma query refazendo o pedido no foco da
 *  janela queimaria a cota sem ninguém pedir. */
export function DetectTransfersDialog({ open, mes, onClose }: Props) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const toast = useToast()

  const previa = useMutation({
    mutationFn: () => detectTransfers({ month: mes, dryRun: true }),
  })

  const confirmar = useMutation({
    mutationFn: () => detectTransfers({ month: mes, dryRun: false }),
    onSuccess: async (resultado) => {
      toast.sucesso(fraseDoToast(resultado.paired))
      onClose()
      // As três leituras que a conversão muda: a lista de transferências
      // ganha os pares, o `summary` de /lancamentos perde receita e despesa,
      // e a conta — embora o saldo não mude — é relida por segurança (ADR-028d).
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['transfers'] }),
        queryClient.invalidateQueries({ queryKey: ['transactions'] }),
        queryClient.invalidateQueries({ queryKey: ['accounts'] }),
      ])
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
  // `<dialog>` fechar com animação e devolver o foco a quem o abriu. Mesmo
  // padrão do `AutoCategorizeDialog`.
  const [jaAbriu, setJaAbriu] = useState(open)
  useEffect(() => {
    if (open) setJaAbriu(true)
  }, [open])

  const resultado = previa.data
  const carregando = open && (previa.isIdle || previa.isPending)
  const nomeDoMesAtual = nomeDoMes(mes)
  const podeConfirmar = resultado !== undefined && resultado.paired > 0 && !previa.isPending

  /** O 409: o mês mudou entre a prévia e a gravação. A saída não é "tentar de
   *  novo" (gravaria sobre uma prévia velha), é olhar de novo. */
  function conferirDeNovo() {
    limparConfirmacao()
    pedirPrevia()
  }

  function irPara(destino: '/contas' | '/importar') {
    onClose()
    void navigate({
      to: destino,
      search: (anterior) => (anterior.mes ? { mes: anterior.mes } : {}),
    })
  }

  if (!jaAbriu) return null

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Reprocessar transferências"
      description={`${mesPorExtensoCapitalizado(mes)} · junta receita e despesa que são o mesmo Pix entre as suas contas. Nada é criado — só o que já existe muda de tipo.`}
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
          isConflict(confirmar.error) ? (
            <Alert
              tone="error"
              title="O mês mudou enquanto a prévia estava aberta."
              action={<Button onClick={conferirDeNovo}>Conferir de novo</Button>}
            >
              {messageForError(confirmar.error)}
            </Alert>
          ) : (
            <Alert tone="error" title="Não foi possível converter.">
              {messageForError(confirmar.error)}
            </Alert>
          )
        ) : null}

        {carregando ? (
          <>
            <p className={styles.resumo} role="status">
              Conferindo as palavras-chave das contas de {nomeDoMesAtual}…
            </p>
            <div className={styles.tabela}>
              <DataTable
                caption="Prévia do reprocessamento"
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
          <Previa
            resultado={resultado}
            mes={mes}
            onIrParaContas={() => irPara('/contas')}
            onImportar={() => irPara('/importar')}
          />
        ) : null}
      </div>
    </Dialog>
  )
}

// -------------------------------------------------------------- pedaços

function Previa({
  resultado,
  mes,
  onIrParaContas,
  onImportar,
}: {
  resultado: TransferDetectResult
  mes: string
  onIrParaContas: () => void
  onImportar: () => void
}) {
  const grupoDosSemPar = montarGrupoDosSemPar(resultado)

  if (resultado.paired === 0) {
    return (
      <>
        <EmptyState
          title={`Nenhum par para reconhecer em ${mesPorExtenso(mes)}.`}
          description={descricaoDoVazio(resultado.unpaired)}
          action={
            resultado.unpaired > 0 ? (
              <Button variant="secondary" onClick={onImportar}>
                Importar extrato
              </Button>
            ) : (
              <Button variant="secondary" onClick={onIrParaContas}>
                Ir para contas
              </Button>
            )
          }
        />
        {/* <details> nativo: fechado, é auditoria, não trabalho — a lista dos
            sem par só interessa a quem quer saber qual extrato falta. */}
        {grupoDosSemPar ? (
          <details className={styles.detalhes}>
            <summary className={styles.sumario}>
              {resultado.unpaired === 1
                ? 'Ver o lançamento sem par'
                : `Ver os ${resultado.unpaired} lançamentos sem par`}
            </summary>
            <div className={styles.tabela}>
              <DataTable
                caption="Lançamentos que parecem transferência, mas não têm par"
                columns={COLUNAS}
                groups={[grupoDosSemPar]}
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
      key: 'viram',
      label: `Viram transferência · ${resultado.paired}`,
      rows: comCorte(
        resultado.items.map(
          (item): LinhaDaPrevia => ({
            tipo: 'par',
            id: item.outTransactionId,
            data: item.occurredOn,
            de: item.fromAccountName,
            para: item.toAccountName,
            descricao: item.description,
            valorCents: item.amountCents,
            pontuacao: item.matchScore,
            palavra: item.matchedKeyword,
          }),
        ),
        resultado.paired,
      ),
    },
  ]
  if (grupoDosSemPar) grupos.push(grupoDosSemPar)

  return (
    <>
      <p className={styles.resumo} role="status">
        {fraseDoResumo(resultado)}
      </p>
      <div className={styles.tabela}>
        <DataTable
          caption="Prévia do reprocessamento"
          columns={COLUNAS}
          groups={grupos}
          rowKey={(linha) => linha.id}
        />
      </div>
    </>
  )
}

/** "Nubank → C6", com a direção dita em palavras para quem não vê a seta —
 *  o mesmo portador da tabela de `/transferencias`. */
function CelulaDeContas({ de, para }: { de: string; para: string }) {
  return (
    <span className={styles.contas}>
      {/* Cada conta é um pedaço indivisível, e a seta anda colada no destino:
          numa coluna estreita a quebra acontece ENTRE as contas ("Bruno Nubank"
          / "→ Bruno C6"), nunca no meio de um nome. */}
      <span className={styles.setaEntreContas} aria-hidden="true">
        <span className={styles.nomeDeConta}>{de}</span>
        <span className={styles.nomeDeConta}>
          <ArrowRightIcon size={14} /> {para}
        </span>
      </span>
      <span className="sr-only">
        de {de} para {para}
      </span>
    </span>
  )
}

// --------------------------------------------------------------- tabela

type LinhaDaPrevia =
  | {
      tipo: 'par'
      /** O id da perna de saída — estável e único por par. */
      id: string
      data: string
      de: string
      para: string
      descricao: string
      valorCents: number
      pontuacao: number
      palavra: string
    }
  | {
      tipo: 'semPar'
      id: string
      data: string
      conta: string
      /** Só receita e despesa são candidatas — é o que diz de que lado do Pix
       *  esta linha está, e portanto qual extrato falta. */
      kind: 'income' | 'expense'
      descricao: string
      valorCents: number
      motivo: TransferDetectUnpairedReason
    }
  /** Última linha de um grupo cortado em 500: só a frase, sem dado. */
  | { tipo: 'corte'; id: string; listados: number; contagem: number }

const COLUNAS: readonly Column<LinhaDaPrevia>[] = [
  {
    key: 'data',
    header: 'Data',
    width: 'min',
    render: (linha) => (linha.tipo === 'corte' ? null : dataCurta(linha.data)),
  },
  {
    key: 'contas',
    header: 'Contas',
    width: 'min',
    // Abaixo de 40rem a coluna some e as contas reaparecem como segunda linha
    // da descrição: "Nubank → C6" ao lado de Data, Descrição e Valor não cabe
    // em 375px, e a direção do dinheiro é o dado que menos pode sumir aqui.
    hideBelow: 'sm',
    render: (linha) => <Contas linha={linha} />,
  },
  {
    key: 'descricao',
    header: 'Descrição',
    render: (linha) => <CelulaDeDescricao linha={linha} />,
  },
  {
    key: 'valor',
    header: 'Valor',
    align: 'end',
    width: 'min',
    // Neutro e sem sinal: a direção está na coluna Contas.
    render: (linha) => (linha.tipo === 'corte' ? null : <MoneyText cents={linha.valorCents} />),
  },
]

function Contas({ linha }: { linha: LinhaDaPrevia }) {
  if (linha.tipo === 'corte') return null
  if (linha.tipo === 'par') return <CelulaDeContas de={linha.de} para={linha.para} />
  // Sem par existe uma conta só, e a direção — que no par está na seta — vira
  // a preposição: é ela que diz qual extrato falta importar ("saiu da C6" →
  // procure quem recebeu). Nunca sinal nem cor no valor.
  return (
    <span className={styles.contas}>
      {linha.kind === 'expense' ? 'saiu de ' : 'entrou em '}
      {linha.conta}
    </span>
  )
}

function CelulaDeDescricao({ linha }: { linha: LinhaDaPrevia }) {
  if (linha.tipo === 'corte') {
    return (
      <span className={styles.corte}>
        Mostrando {linha.listados} de {linha.contagem}.
      </span>
    )
  }
  const descricao = linha.descricao.trim()
  return (
    <>
      <span className={styles.descricao} title={descricao || undefined}>
        {descricao || <span className={styles.semDescricao}>Sem descrição</span>}
      </span>
      <span className={styles.apoio}>
        {/* Só aparece abaixo de 40rem, onde a coluna Contas foi escondida pelo
            DataTable. O dado não some — ele muda de lugar, e ocupa a linha
            inteira: espremido ao lado da proveniência ele quebraria no meio do
            nome da conta. */}
        <span className={styles.contasEstreitas}>
          <Contas linha={linha} />
        </span>
        {linha.tipo === 'par' ? (
          // Proveniência: pontuação é TEXTO em tabular-nums, e 100 não aparece —
          // ausência de pontuação significa correspondência exata.
          <span className={styles.proveniencia}>
            <span className="sr-only">Reconhecida pela palavra-chave </span>
            {linha.pontuacao === 100
              ? citarPalavra(linha.palavra)
              : `${linha.pontuacao}% · ${citarPalavra(linha.palavra)}`}
          </span>
        ) : (
          <span className={styles.motivo}>{PALAVRA_DO_MOTIVO[linha.motivo]}</span>
        )}
      </span>
    </>
  )
}

function montarGrupoDosSemPar(resultado: TransferDetectResult): RowGroup<LinhaDaPrevia> | null {
  if (resultado.unpaired === 0) return null
  return {
    key: 'semPar',
    label: `Parecem transferência, mas não têm par · ${resultado.unpaired}`,
    description: DESCRICAO_DOS_SEM_PAR,
    rows: comCorte(
      resultado.unpairedItems.map(
        (item): LinhaDaPrevia => ({
          tipo: 'semPar',
          id: item.id,
          data: item.occurredOn,
          conta: item.accountName,
          kind: item.kind,
          descricao: item.description,
          valorCents: item.amountCents,
          motivo: item.reason,
        }),
      ),
      resultado.unpaired,
    ),
  }
}

/** Acrescenta a linha de corte quando a lista veio menor que a contagem —
 *  é assim que o contrato sinaliza o teto de 500. */
function comCorte(linhas: LinhaDaPrevia[], contagem: number): LinhaDaPrevia[] {
  if (linhas.length >= contagem) return linhas
  return [
    ...linhas,
    { tipo: 'corte', id: `corte-${linhas.length}`, listados: linhas.length, contagem },
  ]
}

// ----------------------------------------------------------------- copy

function rotuloDoConfirmar(resultado: TransferDetectResult | undefined): string {
  if (!resultado) return 'Converter'
  if (resultado.paired === 0) return 'Nada a converter'
  if (resultado.paired === 1) return 'Converter 1 par'
  return `Converter ${resultado.paired} pares`
}

/** A frase-resumo da prévia: os dois números do contrato, em uma linha. */
export function fraseDoResumo(resultado: Pick<TransferDetectResult, 'paired' | 'unpaired'>) {
  const pares =
    resultado.paired === 1
      ? '1 par vira transferência'
      : `${resultado.paired} pares viram transferência`
  if (resultado.unpaired === 0) return `${pares}.`
  const semPar =
    resultado.unpaired === 1
      ? '1 lançamento fica como está'
      : `${resultado.unpaired} lançamentos ficam como estão`
  return `${pares}; ${semPar}.`
}

function descricaoDoVazio(semPar: number): string {
  if (semPar === 0) {
    return 'Nenhuma receita ou despesa deste mês bate com as palavras-chave das suas contas. Cadastre em cada conta o texto do Pix como ele aparece no extrato.'
  }
  const quantos =
    semPar === 1
      ? '1 lançamento parece transferência, mas a outra perna não está gravada'
      : `${semPar} lançamentos parecem transferência, mas a outra perna não está gravada`
  return `${quantos}. Importe o extrato da outra conta e reprocesse.`
}

/** O número é o que o SERVIDOR devolveu — nunca o da prévia. */
export function fraseDoToast(pares: number): string {
  if (pares === 0) return 'Nada mudou — não havia par para reconhecer.'
  if (pares === 1) return '1 transferência reconhecida.'
  return `${pares} transferências reconhecidas.`
}
