import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useState } from 'react'
import type { InvestmentDetectResult, InvestmentDetectUnmatchedReason } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable, type RowGroup } from '@/components/DataTable/DataTable'
import { Dialog } from '@/components/Dialog/Dialog'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { ArrowRightIcon } from '@/components/icons/ArrowRightIcon'
import { useToast } from '@/components/Toast/Toast'
import { isConflict, isLoteGrandeDemais, messageForError } from '@/lib/errors'
import { citarPalavra } from '@/lib/keywords'
import { mesPorExtensoCapitalizado, nomeDoMes } from '@/lib/month'
import { detectInvestments } from '../api/investments'
import styles from './DetectInvestmentsDialog.module.css'

type Props = {
  open: boolean
  /** `AAAA-MM` — o mês da tela, que é o mês de competência do pedido. */
  mes: string
  onClose: () => void
  onIrParaCategorias: () => void
}

/** A FRASE de cada motivo. `Record` exaustivo: um motivo novo no contrato vira
 *  erro de compilação aqui, não `below_threshold` cru na tela. */
const FRASE_DO_MOTIVO: Record<InvestmentDetectUnmatchedReason, string> = {
  below_threshold: 'abaixo de 80%',
  ambiguous: 'empate entre categorias',
  // Houve vencedora, mas ela é de natureza `income`/`expense`. Dizer "abaixo de
  // 80%" aqui seria mentira na tela: a pessoa veria "não bateu com nada" para
  // uma linha que bateu com "Mercado" a 100. A frase é curta como as outras
  // duas — a coluna Categoria é de largura mínima, e uma oração inteira ali
  // quebra em cinco linhas (medido com Playwright a 1100px).
  other_category: 'bateu com outra categoria',
}

const DESCRICAO_DOS_JA_CATEGORIZADOS =
  'Estes não mudam, a não ser que você peça. A categoria atual fica no lugar.'

/** O PORQUÊ dos três motivos, dito uma vez só — regra da E2: a descrição do
 *  grupo explica o motivo para o grupo, e a célula fica com a palavra curta.
 *  O terceiro caso tem próximo passo próprio: quem escreve categoria comum é o
 *  auto-categorize de `/lancamentos`, não esta detecção. */
const DESCRICAO_DOS_SEM_CATEGORIA =
  'Abaixo de 80% de semelhança o app não arrisca; num empate entre duas categorias, também não. E quando a palavra que bate é de uma categoria de despesa ou de receita, esta detecção não mexe — para essa, use Categorizar automaticamente em Lançamentos.'

/** Diálogo "Detectar investimentos" (spec 0006 §3.3).
 *
 *  Abre já pedindo a **prévia** (`dryRun: true`) e só grava quando a pessoa
 *  confirma (`dryRun: false`). O servidor recalcula na confirmação em vez de
 *  confiar na prévia — por isso o toast mostra o número que **voltou**, não o
 *  que a prévia prometeu.
 *
 *  `overwriteCategorized` tem **confirmação própria**, num segundo passo deste
 *  mesmo `<dialog>`: é a primeira escrita do projeto autorizada a substituir
 *  uma categoria escolhida por uma pessoa, e não existe desfazer. Marcar a
 *  caixa não grava nada — ela muda o rótulo do confirmar, e o confirmar leva à
 *  pergunta, que diz em português o que vai ser trocado e que não há volta.
 *  **Ausente é `false`, sempre**: o corpo só leva `true` depois desse segundo
 *  sim.
 *
 *  A prévia é uma `useMutation`, não uma `useQuery`, de propósito: cada chamada
 *  gasta cota do rate limit da casa (60/h, balde próprio), e uma query
 *  refazendo o pedido no foco da janela queimaria a cota sem ninguém pedir. */
export function DetectInvestmentsDialog({ open, mes, onClose, onIrParaCategorias }: Props) {
  const queryClient = useQueryClient()
  const toast = useToast()

  /** A caixa de troca. Nasce desmarcada em toda abertura — o seguro é o
   *  ausente, e ele nunca é herdado da vez anterior. */
  const [trocarTambem, setTrocarTambem] = useState(false)
  /** `true` enquanto a pergunta da troca está na tela. */
  const [confirmandoTroca, setConfirmandoTroca] = useState(false)

  const previa = useMutation({
    mutationFn: () => detectInvestments({ month: mes, dryRun: true, overwriteCategorized: false }),
  })

  const confirmar = useMutation({
    mutationFn: (comTroca: boolean) =>
      detectInvestments({ month: mes, dryRun: false, overwriteCategorized: comTroca }),
    onSuccess: async (resultado) => {
      toast.sucesso(fraseDoToast(resultado.marked))
      onClose()
      // O prefixo cobre de uma vez a tela de investimentos, a lista, o
      // `summary` e o relatório. `categories` não muda.
      await queryClient.invalidateQueries({ queryKey: ['transactions'] })
    },
  })

  // `mutate` e `reset` são estáveis por contrato do TanStack Query, então isto
  // é estável e o efeito abaixo roda uma vez por abertura.
  const { mutate: pedirPrevia } = previa
  const { reset: limparConfirmacao } = confirmar

  /** Volta ao começo: limpa o erro, desmarca a troca, sai da pergunta e pede a
   *  prévia de novo.
   *
   *  Serve à abertura e ao 409. A caixa de troca **sempre** volta desmarcada:
   *  a prévia nova pode ter outras contagens, e autorização de sobrescrita não
   *  se herda de um pedido que não aconteceu. */
  const reiniciarPrevia = useCallback(() => {
    limparConfirmacao()
    setTrocarTambem(false)
    setConfirmandoTroca(false)
    pedirPrevia()
  }, [pedirPrevia, limparConfirmacao])

  // Abrir é pedir a prévia. O mês não entra nas dependências porque não muda
  // com o diálogo aberto: o `<dialog>` modal deixa o seletor de mês da casca
  // inerte.
  useEffect(() => {
    if (!open) return
    reiniciarPrevia()
  }, [open, reiniciarPrevia])

  // Só entra no DOM a partir da primeira abertura — e depois FICA, para o
  // `<dialog>` fechar com animação e devolver o foco a quem o abriu.
  const [jaAbriu, setJaAbriu] = useState(open)
  useEffect(() => {
    if (open) setJaAbriu(true)
  }, [open])

  const resultado = previa.data
  const carregando = open && (previa.isIdle || previa.isPending)
  const trocas = resultado && trocarTambem ? resultado.alreadyCategorized : 0
  const aMarcar = resultado?.marked ?? 0
  const podeConfirmar = resultado !== undefined && aMarcar + trocas > 0 && !previa.isPending

  function irParaCategorias() {
    onClose()
    onIrParaCategorias()
  }

  /** O confirmar do passo 1. Com troca marcada ele **não grava**: leva à
   *  pergunta própria. Sem troca, grava direto. */
  function confirmarPasso1() {
    if (!podeConfirmar) return
    if (trocas > 0) {
      setConfirmandoTroca(true)
      return
    }
    confirmar.mutate(false)
  }

  if (!jaAbriu) return null

  const rotulo = rotuloDoConfirmar(resultado, trocarTambem)

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Detectar investimentos"
      description={`${mesPorExtensoCapitalizado(mes)} · aplica as palavras-chave das categorias de investimento e de resgate aos lançamentos sem categoria. O que já tem categoria não muda.`}
      footer={
        <div className={styles.rodape}>
          {confirmandoTroca ? (
            <>
              <Button variant="quiet" onClick={() => setConfirmandoTroca(false)}>
                Voltar
              </Button>
              <Button
                variant="primary"
                loading={confirmar.isPending}
                onClick={() => confirmar.mutate(true)}
              >
                {rotulo}
              </Button>
            </>
          ) : (
            <>
              <Button variant="quiet" onClick={onClose}>
                Cancelar
              </Button>
              {/* Nunca `disabled`: o botão continua focável e o rótulo explica
                  por que não há o que confirmar. */}
              <Button
                variant="primary"
                loading={confirmar.isPending}
                aria-disabled={podeConfirmar ? undefined : true}
                onClick={confirmarPasso1}
              >
                {rotulo}
              </Button>
            </>
          )}
        </div>
      }
    >
      <div className={styles.corpo}>
        {confirmar.isError ? (
          <ErroDeGravacao error={confirmar.error} onConferirDeNovo={reiniciarPrevia} />
        ) : null}

        {confirmandoTroca && resultado ? (
          <ConfirmacaoDaTroca resultado={resultado} />
        ) : carregando ? (
          <>
            <p className={styles.resumo} role="status">
              Conferindo as palavras-chave de {nomeDoMes(mes)}…
            </p>
            <div className={styles.tabela}>
              <DataTable
                caption="Prévia da detecção de investimentos"
                columns={COLUNAS}
                rows={[]}
                rowKey={(linha) => linha.id}
                loading
              />
            </div>
          </>
        ) : previa.isError ? (
          isLoteGrandeDemais(previa.error) ? (
            <AvisoDoTeto />
          ) : (
            <Alert
              tone="error"
              title="Não foi possível conferir as palavras-chave."
              action={<Button onClick={() => previa.mutate()}>Tentar de novo</Button>}
            >
              {messageForError(previa.error)}
            </Alert>
          )
        ) : resultado ? (
          <Previa
            resultado={resultado}
            mes={mes}
            trocarTambem={trocarTambem}
            onTrocarTambem={setTrocarTambem}
            onIrParaCategorias={irParaCategorias}
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
  trocarTambem,
  onTrocarTambem,
  onIrParaCategorias,
}: {
  resultado: InvestmentDetectResult
  mes: string
  trocarTambem: boolean
  onTrocarTambem: (valor: boolean) => void
  onIrParaCategorias: () => void
}) {
  const grupoDosSemCategoria = montarGrupoDosSemCategoria(resultado)

  // O vazio só vale quando NÃO há nada a fazer. Com `alreadyCategorized > 0` a
  // pessoa ainda pode pedir a troca — dizer "nenhum lançamento receberia
  // categoria" e esconder a caixa seria fechar a única saída que existe.
  if (resultado.marked === 0 && resultado.alreadyCategorized === 0) {
    return (
      <>
        <EmptyState
          title="Nenhum lançamento receberia categoria de investimento."
          description={`As palavras-chave das suas categorias de investimento e de resgate não batem com as descrições dos lançamentos sem categoria de ${nomeDoMes(mes)}. Cadastre na categoria o nome como ele aparece no extrato — ${citarPalavra('cdb')}, ${citarPalavra('tesouro')}, ${citarPalavra('resgate cdb')}.`}
          action={
            <Button variant="secondary" onClick={onIrParaCategorias}>
              Ir para categorias
            </Button>
          }
        />
        {/* <details> nativo: fechado, é auditoria, não trabalho — a lista dos
            que ficaram de fora só interessa a quem quer saber por quê. */}
        {grupoDosSemCategoria ? (
          <details className={styles.detalhes}>
            <summary className={styles.sumario}>
              {resultado.unmatched === 1
                ? 'Ver o lançamento e o motivo'
                : `Ver os ${resultado.unmatched} lançamentos e o motivo`}
            </summary>
            <div className={styles.tabela}>
              <DataTable
                caption="Lançamentos sem categoria que continuam sem categoria de investimento"
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

  const grupos: RowGroup<LinhaDaPrevia>[] = []

  if (resultado.marked > 0) {
    grupos.push({
      key: 'viram',
      label: `Viram aporte ou resgate · ${resultado.marked}`,
      rows: comCorte(
        resultado.items.map(
          (item): LinhaDaPrevia => ({
            tipo: 'nova',
            id: item.id,
            descricao: item.description,
            flow: item.flow,
            categoria: item.categoryName,
            pontuacao: item.matchScore,
            palavra: item.matchedKeyword,
          }),
        ),
        resultado.marked,
      ),
    })
  }

  if (resultado.alreadyCategorized > 0) {
    grupos.push({
      key: 'jaTem',
      label: `Já têm categoria · ${resultado.alreadyCategorized}`,
      description: DESCRICAO_DOS_JA_CATEGORIZADOS,
      rows: comCorte(
        resultado.alreadyCategorizedItems.map(
          (item): LinhaDaPrevia => ({
            tipo: 'troca',
            id: item.id,
            descricao: item.description,
            flow: item.flow,
            de: item.currentCategoryName,
            para: item.categoryName,
          }),
        ),
        resultado.alreadyCategorized,
      ),
    })
  }

  if (grupoDosSemCategoria) grupos.push(grupoDosSemCategoria)

  return (
    <>
      <p className={styles.resumo} role="status">
        {fraseDoResumo(resultado.marked)}
      </p>
      <div className={styles.tabela}>
        <DataTable
          caption="Prévia da detecção de investimentos"
          columns={COLUNAS}
          groups={grupos}
          rowKey={(linha) => linha.id}
        />
      </div>

      {/* A caixa não é um segundo botão nem uma chave de modo: marcada, ela
          muda o RÓTULO do confirmar, que é quem diz o que vai acontecer. */}
      {resultado.alreadyCategorized > 0 ? (
        <label className={styles.alternador}>
          <input
            type="checkbox"
            checked={trocarTambem}
            onChange={(evento) => onTrocarTambem(evento.target.checked)}
          />
          <span>{fraseDaCaixa(resultado.alreadyCategorized)}</span>
        </label>
      ) : null}
    </>
  )
}

/** O segundo sim, e a única tela do app que pede um.
 *
 *  Substituir uma categoria que uma pessoa escolheu é diferente de preencher
 *  uma que estava vazia, e **não há desfazer em lote**: o caminho de volta é
 *  recategorizar linha a linha em Lançamentos. Isso é dito em português, com o
 *  número na frente, e a lista do que muda continua na tela. */
function ConfirmacaoDaTroca({ resultado }: { resultado: InvestmentDetectResult }) {
  const quantos = resultado.alreadyCategorized
  const linhas = resultado.alreadyCategorizedItems.map(
    (item): LinhaDaPrevia => ({
      tipo: 'troca',
      id: item.id,
      descricao: item.description,
      flow: item.flow,
      de: item.currentCategoryName,
      para: item.categoryName,
    }),
  )

  return (
    <>
      {/* `polite`: a escrita ainda não aconteceu, e o foco já veio para cá —
          `assertive` só depois de uma ação feita (docs/DESIGN.md, E7 (l)). */}
      <Alert
        tone="warning"
        title={
          quantos === 1
            ? 'Isto troca uma categoria que já foi escolhida'
            : `Isto troca ${quantos} categorias que já foram escolhidas`
        }
        live="polite"
        autoFocus
      >
        {quantos === 1
          ? 'Este lançamento já tem categoria, escolhida por você ou pela importação, e ela vai ser substituída pela categoria de investimento. Não dá para desfazer de uma vez: para voltar atrás é preciso recategorizar em Lançamentos, um a um.'
          : `Estes ${quantos} lançamentos já têm categoria, escolhida por você ou pela importação, e ela vai ser substituída pela categoria de investimento. Não dá para desfazer de uma vez: para voltar atrás é preciso recategorizar em Lançamentos, um a um.`}
      </Alert>

      <div className={styles.tabela}>
        <DataTable
          caption="Lançamentos que vão trocar de categoria"
          columns={COLUNAS}
          rows={comCorte(linhas, quantos)}
          rowKey={(linha) => linha.id}
        />
      </div>
    </>
  )
}

/** A falha da GRAVAÇÃO, com a frase escolhida pelo código do erro — nunca pelo
 *  texto do servidor (D4 da spec 0003). São três saídas diferentes, e é a ação
 *  de cada uma que justifica a distinção. */
function ErroDeGravacao({
  error,
  onConferirDeNovo,
}: {
  error: unknown
  onConferirDeNovo: () => void
}) {
  // Teto de 10.000: repetir daria o mesmo 422, então não há ação.
  if (isLoteGrandeDemais(error)) return <AvisoDoTeto />

  // 409: a categoria de destino deixou de ser atribuível entre o cálculo do
  // plano e a escrita. A saída NÃO é "tentar de novo" — gravaria sobre uma
  // prévia velha —, é olhar de novo.
  if (isConflict(error)) return <AvisoDeConflito onConferirDeNovo={onConferirDeNovo} />

  return (
    <Alert tone="error" title="Não foi possível marcar.">
      {messageForError(error)}
    </Alert>
  )
}

/** O 409 `CONFLICT` (spec 0006 §3.3, mesma forma do ADR-028d).
 *
 *  O plano é calculado FORA da transação, de propósito, para não segurar
 *  conexão do pool durante o cálculo — e nessa janela a categoria de destino
 *  pode deixar de qualificar. São CINCO os qualificadores que o servidor
 *  reconfere (contrato: `Conflict` em openapi.yaml): ser da casa, estar viva,
 *  não estar arquivada, não ter subcategoria ativa e continuar sendo de
 *  natureza de investimento compatível com o lado do dinheiro daquele lote.
 *  Faltando qualquer um, a transação inteira é desfeita; por isso a frase
 *  afirma, sem rodeio, que **nada foi marcado**.
 *
 *  ⚠️ A frase é AGNÓSTICA DE CAUSA, e isso é o conserto do achado N4: a versão
 *  anterior nomeava dois dos cinco ("foi excluída, ou ganhou uma
 *  subcategoria"), então quem levasse 409 por ARQUIVAMENTO ou por TROCA DE
 *  NATUREZA — os dois qualificadores novos — lia uma causa que não aconteceu e
 *  ia procurar no lugar errado. Nomear um subconjunto é pior do que não nomear
 *  nenhum: a pessoa confia no que a tela diz. Quem acrescentar um sexto
 *  qualificador no servidor não precisa voltar aqui — e é essa a propriedade
 *  que se está comprando.
 *
 *  Nenhum nome e nenhum id de categoria aparecem: o backend deliberadamente não
 *  os envia, porque o erro atravessa o log, e a tela não os inventa a partir do
 *  cache — o cache está velho, é exatamente essa a notícia. O "o quê" a pessoa
 *  vê na prévia nova. */
function AvisoDeConflito({ onConferirDeNovo }: { onConferirDeNovo: () => void }) {
  return (
    <Alert
      tone="error"
      title="As categorias mudaram enquanto a prévia estava aberta."
      action={<Button onClick={onConferirDeNovo}>Conferir de novo</Button>}
    >
      Nada foi marcado. A detecção só marca em categoria que pode receber lançamento, e uma das
      categorias desta prévia deixou de poder.
    </Alert>
  )
}

/** O teto de 10.000 candidatos: nada foi alterado, e "tentar de novo" não
 *  ajudaria — o que muda o resultado é reduzir o trabalho. Por isso não há ação
 *  no aviso. */
function AvisoDoTeto() {
  return (
    <Alert tone="error" title="Este mês tem lançamentos demais para detectar de uma vez.">
      O limite é 10.000 por execução e nada foi alterado. Use o atalho de categoria em Lançamentos.
    </Alert>
  )
}

// --------------------------------------------------------------- tabela

type LinhaDaPrevia =
  | {
      tipo: 'nova'
      id: string
      descricao: string
      flow: 'contribution' | 'redemption'
      categoria: string
      pontuacao: number
      palavra: string
    }
  | {
      tipo: 'troca'
      id: string
      descricao: string
      flow: 'contribution' | 'redemption'
      de: string
      para: string
    }
  | {
      tipo: 'semCategoria'
      id: string
      descricao: string
      motivo: InvestmentDetectUnmatchedReason
    }
  /** Última linha de um grupo cortado em 500: só a frase, sem dado. */
  | { tipo: 'corte'; id: string }

/** A PALAVRA do movimento — a mesma da tela. */
const PALAVRA_DO_MOVIMENTO: Record<'contribution' | 'redemption', string> = {
  contribution: 'Aporte',
  redemption: 'Resgate',
}

const COLUNAS: readonly Column<LinhaDaPrevia>[] = [
  {
    key: 'descricao',
    header: 'Descrição',
    render: (linha) => <CelulaDeDescricao linha={linha} />,
  },
  {
    key: 'movimento',
    header: 'Movimento',
    width: 'min',
    render: (linha) =>
      linha.tipo === 'nova' || linha.tipo === 'troca' ? (
        <span className={styles.movimento}>{PALAVRA_DO_MOVIMENTO[linha.flow]}</span>
      ) : null,
  },
  {
    key: 'categoria',
    header: 'Categoria',
    width: 'min',
    render: (linha) => <CelulaDeCategoria linha={linha} />,
  },
]

function CelulaDeDescricao({ linha }: { linha: LinhaDaPrevia }) {
  if (linha.tipo === 'corte') {
    return <span className={styles.corte}>Mostrando as primeiras 500.</span>
  }
  const descricao = linha.descricao.trim()
  return (
    <span className={styles.descricao} title={descricao || undefined}>
      {descricao || <span className={styles.semDescricao}>Sem descrição</span>}
    </span>
  )
}

function CelulaDeCategoria({ linha }: { linha: LinhaDaPrevia }) {
  if (linha.tipo === 'corte') return null

  if (linha.tipo === 'semCategoria') {
    return <span className={styles.motivo}>{FRASE_DO_MOTIVO[linha.motivo]}</span>
  }

  if (linha.tipo === 'troca') {
    // O mesmo dispositivo de direção de /transferencias: a seta é decorativa e
    // a direção é dita em palavras para quem não a vê.
    return (
      <span className={styles.categoria}>
        <span className={styles.troca} aria-hidden="true">
          {linha.de} <ArrowRightIcon size={14} /> {linha.para}
        </span>
        <span className="sr-only">
          de {linha.de} para {linha.para}
        </span>
      </span>
    )
  }

  return (
    <span className={styles.categoria}>
      <span className={styles.nomeDaCategoria}>{linha.categoria}</span>
      {/* Proveniência: pontuação é TEXTO em tabular-nums, e 100 não aparece —
          ausência de pontuação significa correspondência exata. */}
      <span className={styles.proveniencia}>
        <span className="sr-only">Reconhecida pela palavra-chave </span>
        {linha.pontuacao === 100
          ? citarPalavra(linha.palavra)
          : `${linha.pontuacao}% · ${citarPalavra(linha.palavra)}`}
      </span>
    </span>
  )
}

function montarGrupoDosSemCategoria(
  resultado: InvestmentDetectResult,
): RowGroup<LinhaDaPrevia> | null {
  if (resultado.unmatched === 0) return null
  return {
    key: 'semCategoria',
    label: `Continuam sem categoria · ${resultado.unmatched}`,
    description: DESCRICAO_DOS_SEM_CATEGORIA,
    rows: comCorte(
      resultado.unmatchedItems.map(
        (item): LinhaDaPrevia => ({
          tipo: 'semCategoria',
          id: item.id,
          descricao: item.description,
          motivo: item.reason,
        }),
      ),
      resultado.unmatched,
    ),
  }
}

/** Acrescenta a linha de corte quando a lista veio menor que a contagem — é
 *  assim que o contrato sinaliza o teto de 500. */
function comCorte(linhas: LinhaDaPrevia[], contagem: number): LinhaDaPrevia[] {
  if (linhas.length >= contagem) return linhas
  return [...linhas, { tipo: 'corte', id: `corte-${linhas.length}` }]
}

// ----------------------------------------------------------------- copy

export function rotuloDoConfirmar(
  resultado: InvestmentDetectResult | undefined,
  trocarTambem: boolean,
): string {
  if (!resultado) return 'Marcar'
  const marcar = resultado.marked
  const trocas = trocarTambem ? resultado.alreadyCategorized : 0
  if (marcar === 0 && trocas === 0) return 'Nada a marcar'
  if (trocas === 0) return marcar === 1 ? 'Marcar 1 lançamento' : `Marcar ${marcar} lançamentos`
  if (marcar === 0) return trocas === 1 ? 'Trocar 1 lançamento' : `Trocar ${trocas} lançamentos`
  return `Marcar ${marcar} e trocar ${trocas}`
}

/** A frase-resumo da prévia. */
export function fraseDoResumo(marcados: number): string {
  return marcados === 1
    ? '1 lançamento vira aporte ou resgate.'
    : `${marcados} lançamentos viram aporte ou resgate.`
}

function fraseDaCaixa(quantos: number): string {
  return quantos === 1
    ? 'Trocar também a categoria de 1 lançamento que já tem uma'
    : `Trocar também a categoria de ${quantos} lançamentos que já têm uma`
}

/** O número é o que o SERVIDOR devolveu — nunca o da prévia. */
export function fraseDoToast(marcados: number): string {
  if (marcados === 0) return 'Nada mudou — nenhuma descrição bateu com as palavras-chave.'
  if (marcados === 1) return '1 lançamento marcado como aporte ou resgate.'
  return `${marcados} lançamentos marcados como aporte ou resgate.`
}
