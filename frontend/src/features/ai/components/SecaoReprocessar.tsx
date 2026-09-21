import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { TransferDetectUnpairedReason } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable, type RowGroup } from '@/components/DataTable/DataTable'
import { Panel } from '@/components/Panel/Panel'
import { TextLink } from '@/components/TextLink/TextLink'
import { dataCurta } from '@/lib/civil'
import { mesPorExtensoCapitalizado } from '@/lib/month'
import { categorizarAutomaticamente, detectarTransferencias } from '../api/reprocessamento'
import { capitalizar, enumerarMeses, type JanelaDeTrabalho } from '../janela'
import {
  type Chamadas,
  conferirTudo,
  DESCRICAO_DOS_SEM_PAR,
  type EstadoDoPasso,
  estadosDosPassos,
  executarPlano,
  type Falha,
  fraseDaPrevia,
  fraseDoPasso,
  fraseDoQueFicou,
  fraseDoResultado,
  lerErro,
  nadaAReprocessar,
  PALAVRA_DO_ESTADO,
  PALAVRA_DO_MOTIVO,
  type PassoConcluido,
  type PreviaConsolidada,
  planoDeExecucao,
  type TotalDaExecucao,
  tipoDaFalha,
} from '../reprocessamento'
import { FRASE_JANELA_MUDOU, ID_DO_TITULO_REPROCESSAR } from '../secoes'
import styles from './SecaoReprocessar.module.css'

/** As chamadas reais, na forma que a lógica espera. O `dryRun` é explícito
 *  nas duas (o contrato não tem default — ausente é 400), e é a única coisa
 *  que separa "medir" de "gravar". */
const CHAMADAS: Chamadas = {
  detectar: (month, dryRun) => detectarTransferencias({ month, dryRun }),
  categorizar: (month, dryRun) => categorizarAutomaticamente({ month, dryRun }),
}

/** Um estado por lugar fixo da seção. `chave` é `fromMonth-toMonth`, a janela
 *  em que a prévia foi medida — é ela que a regra de frescor compara. */
type Estado =
  | { tipo: 'ocioso' }
  | { tipo: 'conferindo'; chave: string }
  | { tipo: 'erroDaPrevia'; chave: string; erro: unknown }
  | { tipo: 'previa'; chave: string; previa: PreviaConsolidada }
  | {
      tipo: 'executando'
      chave: string
      previa: PreviaConsolidada
      /** Passos concluídos, na ordem — o registro do progresso. */
      log: readonly PassoConcluido[]
    }
  | {
      tipo: 'parado'
      chave: string
      previa: PreviaConsolidada
      log: readonly PassoConcluido[]
      falha: Falha
      erro: unknown
    }
  | {
      tipo: 'concluido'
      chave: string
      previa: PreviaConsolidada
      log: readonly PassoConcluido[]
      total: TotalDaExecucao
    }

/** Seção 3 — **Reprocessar** (spec 0010 §5).
 *
 *  É a fatia que faz as palavras-chave novas passarem a valer nos lançamentos
 *  já gravados — e **não tem backend novo**: ela orquestra `POST
 *  /transfers/detect` e `POST /transactions/auto-categorize`, que já existem,
 *  em **duas fases, cada uma mês a mês** (ver `planoDeExecucao`). Nunca
 *  automática: a prévia é um clique, a execução é outro.
 *
 *  Cada estado mora num lugar fixo:
 *
 *  - **Ocioso:** o apoio, os meses da janela, a frase da ordem e `Conferir`.
 *  - **Conferindo:** `role="status"` com a frase e a `DataTable loading` —
 *    seis chamadas com `dryRun: true`, em paralelo, porque `dryRun` não
 *    escreve.
 *  - **Prévia pronta:** a frase consolidada (somada aqui — contagens, não
 *    centavos: ADR-036(e)), a tabela por mês, o `<details>` dos sem par e o
 *    botão `Reprocessar julho, agosto e setembro`. Zero em tudo → a frase de
 *    "nada a reprocessar" e o botão com `aria-disabled` (**nunca** `disabled`).
 *  - **Executando:** a tabela vira estado por PALAVRA por célula, uma célula
 *    por etapa; o progresso vai num `<output aria-live="polite">`, uma frase
 *    por chamada concluída. Estritamente sequencial, `dryRun: false`, fase 1
 *    inteira antes da fase 2, e **para no primeiro erro**.
 *  - **Parado (409, 429, rede, 500):** nenhuma chamada depois da falha. O
 *    `Alert` diz exatamente o que ficou feito e o que não, a tabela fica na
 *    tela como registro (`feito` / `não aplicado` / `não chegou a rodar`) e a
 *    ação é `Conferir de novo`. O que já foi aplicado volta com 0 na prévia
 *    nova — e esse 0 é a prova de que foi feito (§5.6), não uma falha.
 *  - **Concluído:** o `<dl>` do total REAL (números das respostas de
 *    execução, nunca da prévia) e os links para conferir. **Sem toast.**
 *
 *  **Regra de frescor:** trocar o mês da casca ou o tamanho da janela descarta
 *  a prévia e o resultado — o mesmo mecanismo e a mesma frase da seção 2:
 *  comparação de chave durante a renderização, sem `key` no pai (a remontagem
 *  abandonaria uma execução em curso). Uma execução em curso e uma execução
 *  **parada** não são descartadas: a primeira está gravando, e a segunda é o
 *  registro de uma aplicação pela metade que a pessoa precisa ler — sumir com
 *  ele esconderia o problema em vez de resolvê-lo.
 *
 *  **Orçamento de `aria-live`:** o `role="status"` da frase de estado (uma
 *  frase por estado, nunca atualizado durante a execução) e o `<output>` do
 *  progresso (só existe da execução em diante). Nunca os dois mudam ao mesmo
 *  tempo. O `Alert` de erro tem o papel próprio do componente.
 *
 *  **Cor:** só `--accent` e `--danger`. Esta tela não tem dinheiro, e a lista
 *  dos sem par também não mostra valor — data, conta e descrição bastam para
 *  saber qual extrato falta. */
export function SecaoReprocessar({ janela }: { janela: JanelaDeTrabalho }) {
  const queryClient = useQueryClient()
  const [estado, setEstado] = useState<Estado>({ tipo: 'ocioso' })
  const [janelaMudou, setJanelaMudou] = useState(false)

  const chave = `${janela.fromMonth}-${janela.toMonth}`

  // A regra de frescor, sem efeito: a prévia (ou o resultado) foi medida
  // noutra janela, então morre AGORA, antes de qualquer número dela chegar à
  // tela — e a frase avisa. Execução em curso e execução parada ficam (ver o
  // comentário do componente).
  if (
    (estado.tipo === 'conferindo' ||
      estado.tipo === 'erroDaPrevia' ||
      estado.tipo === 'previa' ||
      estado.tipo === 'concluido') &&
    estado.chave !== chave
  ) {
    setEstado({ tipo: 'ocioso' })
    setJanelaMudou(true)
  }

  const ocupado = estado.tipo === 'conferindo' || estado.tipo === 'executando'

  function conferir() {
    if (ocupado) return
    const chaveDoInicio = chave
    const meses = janela.meses
    setJanelaMudou(false)
    setEstado({ tipo: 'conferindo', chave: chaveDoInicio })
    void conferirTudo(meses, CHAMADAS).then(
      (previa) => {
        // Só a conferência que ainda está em curso PARA ESTA janela pode
        // publicar o resultado: uma prévia velha que chegue depois de a
        // janela mudar (ou de outra conferência começar) é descartada.
        setEstado((atual) =>
          atual.tipo === 'conferindo' && atual.chave === chaveDoInicio
            ? { tipo: 'previa', chave: chaveDoInicio, previa }
            : atual,
        )
      },
      (erro: unknown) => {
        setEstado((atual) =>
          atual.tipo === 'conferindo' && atual.chave === chaveDoInicio
            ? { tipo: 'erroDaPrevia', chave: chaveDoInicio, erro }
            : atual,
        )
      },
    )
  }

  async function reprocessar() {
    if (estado.tipo !== 'previa' || nadaAReprocessar(estado.previa)) return
    const { previa } = estado
    const chaveDoInicio = estado.chave
    // Os meses da PRÉVIA, e não da janela: são os mesmos (a chave bate, senão
    // a prévia teria sido descartada), e é o que o botão prometeu.
    const meses = previa.meses.map((p) => p.mes)
    setEstado({ tipo: 'executando', chave: chaveDoInicio, previa, log: [] })

    const desfecho = await executarPlano(planoDeExecucao(meses), CHAMADAS, (concluido) => {
      setEstado((atual) =>
        atual.tipo === 'executando' ? { ...atual, log: [...atual.log, concluido] } : atual,
      )
    })

    // As leituras que a execução muda — mesmo parada no meio, o que foi
    // aplicado ficou aplicado: a lista de transferências ganhou pares, o
    // `summary` de /lancamentos perdeu receita e despesa, e a conta é relida
    // por segurança (ADR-028d). O prompt da seção 1 NÃO é invalidado: ele é
    // um retrato, e reescrevê-lo pelas costas de quem copia é o que a E9a
    // proibiu.
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['transfers'] }),
      queryClient.invalidateQueries({ queryKey: ['transactions'] }),
      queryClient.invalidateQueries({ queryKey: ['accounts'] }),
    ])

    setEstado((atual) => {
      if (atual.tipo !== 'executando') return atual
      if (desfecho.ok) return { ...atual, tipo: 'concluido', total: desfecho.total }
      return {
        ...atual,
        tipo: 'parado',
        falha: { indice: desfecho.indiceDaFalha, tipo: tipoDaFalha(desfecho.erro) },
        erro: desfecho.erro,
      }
    })
  }

  const mesesDaJanela = capitalizar(enumerarMeses(janela.meses))

  // A frase do `role="status"`: um nó, uma frase por estado, cada estado
  // anunciado uma vez. Durante a execução ela NÃO muda — o `<output>` é quem
  // fala.
  let fraseDeEstado = ''
  if (estado.tipo === 'conferindo') fraseDeEstado = `Conferindo ${enumerarMeses(janela.meses)}…`
  else if (janelaMudou && estado.tipo === 'ocioso') fraseDeEstado = FRASE_JANELA_MUDOU
  else if (estado.tipo === 'previa' || estado.tipo === 'executando') {
    fraseDeEstado = fraseDaPrevia(estado.previa)
  }

  const semPrevia =
    estado.tipo === 'ocioso' || estado.tipo === 'conferindo' || estado.tipo === 'erroDaPrevia'

  return (
    <Panel as="section" titleId={ID_DO_TITULO_REPROCESSAR} padding="none">
      <div className={styles.secao}>
        <header className={styles.cabecalho}>
          {/* `tabIndex={-1}`: focável por script, fora da ordem do Tab. É o que
              faz "Ir para Reprocessar" (seção 2) rolar até aqui sem `scrollTo`. */}
          <h2 className={styles.titulo} id={ID_DO_TITULO_REPROCESSAR} tabIndex={-1}>
            3 · Reprocessar
          </h2>
          <p className={styles.apoio}>
            Palavra-chave nova não mexe sozinha no que já está gravado. Aqui ela passa a valer.
          </p>
        </header>

        {semPrevia ? (
          <div className={styles.explicacao}>
            {/* Os meses vêm de `janelaDeTrabalho`, como em toda a tela — a
                seção não recalcula data nenhuma. */}
            <p className={styles.meses}>{mesesDaJanela}.</p>
            <p className={styles.ordem}>
              Primeiro as transferências, em todos os meses; depois a categorização. Converter um
              par apaga a categoria das duas pernas — categorizar antes seria trabalho perdido.
            </p>
          </div>
        ) : null}

        {estado.tipo === 'parado' ? (
          <AlertaDaParada
            meses={estado.previa.meses.map((p) => p.mes)}
            falha={estado.falha}
            erro={estado.erro}
            onConferirDeNovo={conferir}
          />
        ) : null}

        {estado.tipo === 'erroDaPrevia' ? (
          <AlertaDaPrevia erro={estado.erro} onTentarDeNovo={conferir} />
        ) : null}

        {fraseDeEstado ? (
          <p className={styles.totais} role="status">
            {fraseDeEstado}
          </p>
        ) : null}

        {estado.tipo === 'conferindo' ? (
          <div className={styles.tabela}>
            <DataTable
              caption="Prévia do reprocessamento por mês"
              columns={COLUNAS_DA_PREVIA}
              rows={[]}
              rowKey={(linha) => linha.mes}
              loading
            />
          </div>
        ) : null}

        {estado.tipo === 'previa' ? <TabelaDaPrevia previa={estado.previa} /> : null}

        {estado.tipo === 'executando' || estado.tipo === 'parado' || estado.tipo === 'concluido' ? (
          <>
            <TabelaDaExecucao
              meses={estado.previa.meses.map((p) => p.mes)}
              feitos={estado.log.length}
              falha={estado.tipo === 'parado' ? estado.falha : null}
            />
            {/* `<output>` é o elemento do RESULTADO de uma ação, e já é uma
                live region. Nasce vazio no instante em que a execução começa;
                cada frase é uma ADIÇÃO, que é o que o leitor de tela anuncia. */}
            <output className={styles.log} aria-live="polite">
              {estado.log.map((concluido) => (
                <span
                  className={styles.linhaDoLog}
                  key={`${concluido.passo.mes}-${concluido.passo.etapa}`}
                >
                  {fraseDoPasso(concluido.passo, concluido.quantidade)}
                </span>
              ))}
              {/* A frase final é ANUNCIADA sempre (é a live region), mas só
                  fica visível quando não há `<dl>` — no caso idempotente. Com
                  números, o `<dl>` abaixo é o resultado, dito uma vez. */}
              {estado.tipo === 'concluido' ? (
                <span
                  className={temNumeros(estado.total) ? 'sr-only' : styles.linhaDoLog}
                  data-final=""
                >
                  {fraseDoResultado(estado.total)}
                </span>
              ) : null}
            </output>
          </>
        ) : null}

        {estado.tipo === 'concluido' ? (
          <Resultado
            total={estado.total}
            ultimoMes={estado.previa.meses[estado.previa.meses.length - 1]?.mes ?? janela.toMonth}
          />
        ) : null}

        {/* As ações fecham a seção: são o PRÓXIMO passo depois de ler os
            números, não um cabeçalho. Na parada não há ação aqui — a única
            saída é o "Conferir de novo" do próprio aviso. */}
        {estado.tipo !== 'parado' ? (
          <div className={styles.acoes}>
            {estado.tipo === 'previa' || estado.tipo === 'executando' ? (
              <>
                {/* NUNCA `disabled`: com zero em tudo o botão diz o que falta
                    e continua focável; o clique morre no guarda da função. */}
                <Button
                  variant="primary"
                  onClick={() => void reprocessar()}
                  loading={estado.tipo === 'executando'}
                  aria-disabled={nadaAReprocessar(estado.previa) ? true : undefined}
                >
                  {nadaAReprocessar(estado.previa)
                    ? 'Nada a reprocessar'
                    : `Reprocessar ${enumerarMeses(janela.meses)}`}
                </Button>
                {estado.tipo === 'previa' ? (
                  <Button variant="quiet" onClick={conferir}>
                    Conferir de novo
                  </Button>
                ) : null}
              </>
            ) : (
              <Button variant="secondary" onClick={conferir} loading={estado.tipo === 'conferindo'}>
                {estado.tipo === 'concluido' ? 'Conferir de novo' : 'Conferir'}
              </Button>
            )}
          </div>
        ) : null}
      </div>
    </Panel>
  )
}

// -------------------------------------------------------------- avisos

/** O `Alert` da execução que parou. O título diz o que aconteceu; o corpo diz
 *  **exatamente** o que ficou feito e o que não, e a ação é uma só: conferir
 *  de novo. Nada de "tentar de novo" — repetir sobre uma prévia velha é
 *  gravar às cegas. */
function AlertaDaParada({
  meses,
  falha,
  erro,
  onConferirDeNovo,
}: {
  meses: readonly string[]
  falha: Falha
  erro: unknown
  onConferirDeNovo: () => void
}) {
  const lido = lerErro(erro)
  const titulo =
    lido.tipo === 'conflito'
      ? 'O estado mudou no meio do reprocessamento.'
      : 'O reprocessamento parou.'
  return (
    <Alert
      tone="error"
      title={titulo}
      action={<Button onClick={onConferirDeNovo}>Conferir de novo</Button>}
    >
      <div className={styles.corpoDoAviso}>
        <p>{fraseDoQueFicou(meses, falha)}</p>
        {lido.tipo === 'conflito' ? null : <p>{lido.mensagem}</p>}
        <p>Na prévia nova, o que já foi aplicado volta com 0 — é a prova de que está feito.</p>
      </div>
    </Alert>
  )
}

/** O `Alert` da prévia que falhou. 429 e mês grande demais NÃO ganham "Tentar
 *  de novo" — repetir daria o mesmo erro; a ação é esperar ou encurtar a
 *  janela no topo. */
function AlertaDaPrevia({ erro, onTentarDeNovo }: { erro: unknown; onTentarDeNovo: () => void }) {
  const lido = lerErro(erro)
  return (
    <Alert
      tone="error"
      title="Não foi possível conferir."
      action={
        lido.tipo === 'limite' ? undefined : (
          <Button onClick={onTentarDeNovo}>Tentar de novo</Button>
        )
      }
    >
      {lido.mensagem}
    </Alert>
  )
}

// ------------------------------------------------------ tabela da prévia

type LinhaDaPrevia = { mes: string; pares: number; categorizados: number; semCategoria: number }

const COLUNAS_DA_PREVIA: readonly Column<LinhaDaPrevia>[] = [
  {
    key: 'mes',
    header: 'Mês',
    // Por extenso e com o ano: uma janela que atravessa a virada ("Novembro
    // de 2025", "Janeiro de 2026") precisa dizer de que ano é cada linha.
    render: (linha) => mesPorExtensoCapitalizado(linha.mes),
  },
  {
    key: 'pares',
    header: 'Pares',
    align: 'end',
    width: 'min',
    render: (linha) => <Numero valor={linha.pares} />,
  },
  {
    key: 'categorizados',
    header: 'Categorizados',
    align: 'end',
    width: 'min',
    render: (linha) => <Numero valor={linha.categorizados} />,
  },
  {
    key: 'semCategoria',
    header: 'Sem categoria',
    align: 'end',
    width: 'min',
    render: (linha) => <Numero valor={linha.semCategoria} />,
  },
]

function Numero({ valor }: { valor: number }) {
  return <span className={styles.numero}>{valor}</span>
}

function TabelaDaPrevia({ previa }: { previa: PreviaConsolidada }) {
  const linhas = previa.meses.map(
    (p): LinhaDaPrevia => ({
      mes: p.mes,
      pares: p.transferencias.paired,
      categorizados: p.categorizacao.categorized,
      semCategoria: p.categorizacao.unmatched,
    }),
  )
  const grupos = gruposDosSemPar(previa)
  const haSobreposicao = previa.pares > 0 && previa.categorizados > 0

  return (
    <>
      <div className={styles.tabela}>
        <DataTable
          caption="Prévia do reprocessamento por mês"
          columns={COLUNAS_DA_PREVIA}
          rows={linhas}
          rowKey={(linha) => linha.mes}
        />
      </div>
      {/* A prévia da categorização é medida ANTES das transferências, e as
          duas contagens podem se sobrepor numa perna: ela sai da conta na
          execução, e o número final é o das respostas de execução. A nota
          existe só quando a sobreposição é possível. */}
      {haSobreposicao ? (
        <p className={styles.nota}>
          A categorização foi medida antes das transferências: uma perna de par que também bateria
          com uma palavra-chave de categoria vira transferência e não é categorizada. O total real é
          o da execução.
        </p>
      ) : null}
      {grupos.length > 0 ? (
        // <details> nativo: fechado, é auditoria, não trabalho — a lista dos
        // sem par só interessa a quem quer saber qual extrato falta.
        <details className={styles.detalhes}>
          <summary className={styles.sumario}>
            {previa.semPar === 1
              ? 'Ver o lançamento sem par e o motivo'
              : `Ver os ${previa.semPar} lançamentos sem par e o motivo`}
          </summary>
          <p className={styles.apoio}>{DESCRICAO_DOS_SEM_PAR}</p>
          <div className={styles.moldura}>
            <DataTable
              caption="Lançamentos que parecem transferência, mas não têm par"
              columns={COLUNAS_DOS_SEM_PAR}
              groups={grupos}
              rowKey={(linha) => linha.id}
            />
          </div>
        </details>
      ) : null}
    </>
  )
}

// ----------------------------------------------------- tabela da execução

type LinhaDaExecucao = { mes: string; transferencias: EstadoDoPasso; categorizacao: EstadoDoPasso }

const COLUNAS_DA_EXECUCAO: readonly Column<LinhaDaExecucao>[] = [
  { key: 'mes', header: 'Mês', render: (linha) => mesPorExtensoCapitalizado(linha.mes) },
  {
    key: 'transferencias',
    header: 'Transferências',
    width: 'min',
    render: (linha) => <Palavra estado={linha.transferencias} />,
  },
  {
    key: 'categorizacao',
    header: 'Categorização',
    width: 'min',
    render: (linha) => <Palavra estado={linha.categorizacao} />,
  },
]

/** A palavra é o portador do estado; o peso e a tinta são reforço. */
function Palavra({ estado }: { estado: EstadoDoPasso }) {
  return (
    <span className={styles.estado} data-estado={estado}>
      {PALAVRA_DO_ESTADO[estado]}
    </span>
  )
}

function TabelaDaExecucao({
  meses,
  feitos,
  falha,
}: {
  meses: readonly string[]
  feitos: number
  falha: Falha | null
}) {
  const estados = estadosDosPassos(meses.length * 2, feitos, falha)
  const linhas = meses.map(
    (mes, indice): LinhaDaExecucao => ({
      mes,
      transferencias: estados[indice] ?? 'na_fila',
      categorizacao: estados[meses.length + indice] ?? 'na_fila',
    }),
  )
  return (
    <div className={styles.tabela}>
      <DataTable
        caption="Andamento do reprocessamento por mês"
        columns={COLUNAS_DA_EXECUCAO}
        rows={linhas}
        rowKey={(linha) => linha.mes}
      />
    </div>
  )
}

// --------------------------------------------------------------- sem par

type LinhaSemPar =
  | {
      tipo: 'semPar'
      id: string
      data: string
      conta: string
      /** Só receita e despesa são candidatas — é o que diz de que lado do Pix
       *  esta linha está, e portanto qual extrato falta. */
      kind: 'income' | 'expense'
      descricao: string
      motivo: TransferDetectUnpairedReason
    }
  /** Última linha de um grupo cortado em 500: só a frase, sem dado. */
  | { tipo: 'corte'; id: string; listados: number; contagem: number }

const COLUNAS_DOS_SEM_PAR: readonly Column<LinhaSemPar>[] = [
  {
    key: 'data',
    header: 'Data',
    width: 'min',
    render: (linha) => (linha.tipo === 'corte' ? null : dataCurta(linha.data)),
  },
  {
    key: 'conta',
    header: 'Conta',
    width: 'min',
    // Abaixo de 40rem a coluna some e a conta reaparece como segunda linha da
    // descrição — o dado não some, muda de lugar.
    hideBelow: 'sm',
    render: (linha) => <Conta linha={linha} />,
  },
  {
    key: 'descricao',
    header: 'Descrição',
    render: (linha) => <CelulaDeDescricao linha={linha} />,
  },
]

/** A direção — que no par estaria na seta — vira a preposição: é ela que diz
 *  qual extrato falta importar ("saiu de Nubank" → procure quem recebeu). */
function Conta({ linha }: { linha: LinhaSemPar }) {
  if (linha.tipo === 'corte') return null
  return (
    <span className={styles.conta}>
      {linha.kind === 'expense' ? 'saiu de ' : 'entrou em '}
      {linha.conta}
    </span>
  )
}

function CelulaDeDescricao({ linha }: { linha: LinhaSemPar }) {
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
      <span className={styles.apoioDaLinha}>
        <span className={styles.contaEstreita}>
          <Conta linha={linha} />
        </span>
        <span className={styles.motivo}>{PALAVRA_DO_MOTIVO[linha.motivo]}</span>
      </span>
    </>
  )
}

/** Um grupo por mês com candidatas sem par, na ordem da janela. */
function gruposDosSemPar(previa: PreviaConsolidada): RowGroup<LinhaSemPar>[] {
  const grupos: RowGroup<LinhaSemPar>[] = []
  for (const p of previa.meses) {
    const { unpaired, unpairedItems } = p.transferencias
    if (unpaired === 0) continue
    const linhas = unpairedItems.map(
      (item): LinhaSemPar => ({
        tipo: 'semPar',
        id: item.id,
        data: item.occurredOn,
        conta: item.accountName,
        kind: item.kind,
        descricao: item.description,
        motivo: item.reason,
      }),
    )
    // A linha de corte, quando a lista veio menor que a contagem — é assim
    // que o contrato sinaliza o teto de 500.
    if (linhas.length < unpaired) {
      linhas.push({
        tipo: 'corte',
        id: `corte-${p.mes}`,
        listados: linhas.length,
        contagem: unpaired,
      })
    }
    grupos.push({
      key: p.mes,
      label: `${mesPorExtensoCapitalizado(p.mes)} · ${unpaired}`,
      rows: linhas,
    })
  }
  return grupos
}

// ------------------------------------------------------------- resultado

function temNumeros(total: TotalDaExecucao): boolean {
  return total.pares > 0 || total.categorizados > 0
}

/** O total REAL, numa `<dl>` (as linhas zeradas não aparecem), e os dois links
 *  para conferir — no último mês da janela. Sem toast: o resultado é o
 *  conteúdo da seção, não um aviso que some. */
function Resultado({ total, ultimoMes }: { total: TotalDaExecucao; ultimoMes: string }) {
  const linhas: Array<[string, number]> = [
    ['Pares de transferência', total.pares],
    ['Lançamentos categorizados', total.categorizados],
  ]
  const comValor = linhas.filter(([, quantidade]) => quantidade > 0)
  return (
    <div className={styles.resultado}>
      {temNumeros(total) ? (
        <dl className={styles.lista}>
          {comValor.map(([rotulo, quantidade]) => (
            <div className={styles.linha} key={rotulo}>
              <dt>{rotulo}</dt>
              <dd>{quantidade}</dd>
            </div>
          ))}
        </dl>
      ) : null}
      <p className={styles.links}>
        <TextLink to="/transferencias" search={{ mes: ultimoMes }}>
          Ver as transferências
        </TextLink>
        <span className={styles.separador} aria-hidden="true">
          ·
        </span>
        <TextLink to="/lancamentos" search={{ mes: ultimoMes }}>
          Ver os lançamentos
        </TextLink>
      </p>
    </div>
  )
}
