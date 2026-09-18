import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import type { TransactionSource, TransferBalance, TransferItem, TransferPair } from '@/api/types'
import { aplicarNaBusca, type MudancaDeBusca, validarBusca } from '@/app/search'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable } from '@/components/DataTable/DataTable'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { ArrowRightIcon } from '@/components/icons/ArrowRightIcon'
import { MoneyText } from '@/components/MoneyText/MoneyText'
import { Panel } from '@/components/Panel/Panel'
import { Select } from '@/components/Select/Select'
import { Skeleton } from '@/components/Skeleton/Skeleton'
import { TextLink } from '@/components/TextLink/TextLink'
import { contaPorId, contasQueryOptions, opcoesDeConta } from '@/lib/accounts'
import { dataCurta } from '@/lib/civil'
import { messageForError } from '@/lib/errors'
import { FUSO_PADRAO, mesDaURL, mesPorExtenso, nomeDoMes } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import { POR_PAGINA, transfersInfiniteQueryOptions } from '../api/transfers'
import { DetectTransfersDialog } from '../components/DetectTransfersDialog'
import { type ContaDoPar, TransferPairPanel } from '../components/TransferPairPanel'
import styles from './TransfersScreen.module.css'

/** A PALAVRA da origem. `Record` exaustivo: uma origem nova no contrato vira
 *  erro de compilação aqui, não "import" cru na tela. */
const ROTULO_DA_ORIGEM: Record<TransactionSource, string> = {
  manual: 'Manual',
  import: 'Importação',
}

/** Tela de transferências (spec 0005 §4.4).
 *
 *  O que mudou de conta dentro da casa no mês — cada PAR de pernas (ADR-016)
 *  uma linha só. Três decisões que esta tela sustenta:
 *
 *  1. **Cromaticamente silenciosa.** Transferência não é receita nem despesa,
 *     então nenhum valor aqui leva `--income`/`--expense`. A única exceção é o
 *     saldo no fim do mês, que é posição — como em `/contas`.
 *  2. **A direção do dinheiro é dita em palavras.** A seta entre as contas tem
 *     o `sr-only` "de X para Y", e o painel do par termina numa frase na voz de
 *     quem mandou mais. Sinal é reforço, nunca o portador único.
 *  3. **Os totais vêm do servidor.** `pairs` e `balances` são sobre o mês
 *     inteiro, não sobre a página carregada — somar `items` aqui divergiria na
 *     segunda página. */
export function TransfersScreen() {
  const navigate = useNavigate()
  const tituloRef = useRef<HTMLHeadingElement>(null)

  // Validada aqui, e não lida crua de `location.search` — é a fronteira entre a
  // URL (que a pessoa edita) e a query da API. `contraparte` só sobrevive com
  // `conta`, e nunca igual a ela: é `validarBusca` que garante isso.
  const buscaBruta = useRouterState({ select: (estado) => estado.location.search })
  const busca = validarBusca(buscaBruta as Record<string, unknown>)

  const session = useQuery(sessionQueryOptions)
  const fuso = session.data?.household.timezone ?? FUSO_PADRAO
  const mes = mesDaURL(busca.mes, fuso)
  const contaId = busca.conta
  const contraparteId = busca.contraparte
  const modoPar = contaId !== undefined && contraparteId !== undefined

  const contas = useQuery(contasQueryOptions(false))
  const lista = useInfiniteQuery(transfersInfiniteQueryOptions({ mes, contaId, contraparteId }))

  const [anuncio, setAnuncio] = useState('')
  const [reprocessando, setReprocessando] = useState(false)

  useEffect(() => {
    document.title = 'Transferências · HomeFinance'
  }, [])

  // Navegação de rota move o foco para o <h1> da tela nova (docs/DESIGN.md).
  useEffect(() => {
    tituloRef.current?.focus()
  }, [])

  const paginas = lista.data?.pages ?? []
  const primeira = paginas[0]
  const itens = paginas.flatMap((pagina) => pagina.items)
  const pares = primeira?.pairs ?? []
  const saldos = primeira?.balances ?? []
  const total = pares.reduce((soma, par) => soma + par.count, 0)
  const nomeDoMesAtual = nomeDoMes(mes)
  const { hasNextPage, isFetchingNextPage, isPending } = lista

  const todasAsContas = contas.data?.items ?? []
  const contaFiltrada = contaPorId(todasAsContas, contaId)
  const contraparteFiltrada = contaPorId(todasAsContas, contraparteId)

  function trocarBusca(mudanca: MudancaDeBusca) {
    void navigate({
      to: '/transferencias',
      search: (anterior) => aplicarNaBusca(anterior, mudanca),
      replace: true,
    })
  }

  async function carregarMais() {
    const antes = itens.length
    const resultado = await lista.fetchNextPage()
    const depois = (resultado.data?.pages ?? []).flatMap((pagina) => pagina.items).length
    const novas = depois - antes
    const quantas =
      novas === 1 ? 'Mais 1 transferência carregada' : `Mais ${novas} transferências carregadas`
    setAnuncio(`${quantas}. ${depois} de ${total}.`)
  }

  const colunasDeItens: readonly Column<TransferItem>[] = [
    {
      key: 'data',
      header: 'Data',
      width: 'min',
      render: (linha) => dataCurta(linha.occurredOn),
    },
    {
      key: 'contas',
      header: 'Contas',
      width: 'min',
      // Abaixo de 40rem a coluna some e "Nubank → C6" reaparece como segunda
      // linha da descrição: "Nubank → Cartão Nubank" ao lado de Data, Valor e
      // Descrição não cabe em 375px, e a direção do dinheiro é o dado que menos
      // pode sumir nesta tabela.
      hideBelow: 'sm',
      render: (linha) => <CelulaDeContas de={linha.fromAccountName} para={linha.toAccountName} />,
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
      render: (linha) => <MoneyText cents={linha.amountCents} />,
    },
    {
      key: 'registro',
      header: 'Registro',
      width: 'min',
      hideBelow: 'sm',
      render: (linha) => <span className={styles.registro}>{ROTULO_DA_ORIGEM[linha.source]}</span>,
    },
  ]

  const colunasDePares: readonly Column<ParNormalizado>[] = [
    {
      key: 'contas',
      header: 'Contas',
      render: (par) => (
        <>
          {/* Navegar é trabalho de link: abre em nova aba, tem menu de contexto
              e o leitor de tela anuncia "link". O rótulo diz aonde vai. */}
          <TextLink
            to="/transferencias"
            search={{ mes, conta: par.de.id, contraparte: par.para.id }}
            aria-label={`Ver as transferências entre ${par.de.nome} e ${par.para.nome}`}
          >
            <span className={styles.setaEntreContas}>
              {par.de.nome} <ArrowRightIcon size={14} /> {par.para.nome}
            </span>
          </TextLink>
          {/* Abaixo de 40rem, Enviado, Recebido e Transferências saem da grade
              e reaparecem aqui — só o Líquido fica como coluna. */}
          <span className={styles.secundaria}>
            Enviado <MoneyText cents={par.enviadoCents} />
            <span className={styles.separador} aria-hidden="true">
              ·
            </span>
            Recebido <MoneyText cents={par.recebidoCents} />
            <span className={styles.separador} aria-hidden="true">
              ·
            </span>
            {par.count === 1 ? '1 transferência' : `${par.count} transferências`}
          </span>
        </>
      ),
    },
    {
      key: 'enviado',
      header: 'Enviado',
      align: 'end',
      hideBelow: 'sm',
      render: (par) => <MoneyText cents={par.enviadoCents} />,
    },
    {
      key: 'recebido',
      header: 'Recebido',
      align: 'end',
      hideBelow: 'sm',
      render: (par) => <MoneyText cents={par.recebidoCents} />,
    },
    {
      key: 'liquido',
      header: 'Líquido',
      align: 'end',
      render: (par) => <MoneyText cents={par.liquidoCents} />,
    },
    {
      key: 'transferencias',
      header: 'Transferências',
      align: 'end',
      width: 'min',
      hideBelow: 'sm',
      render: (par) => <span className={styles.contagemDoPar}>{par.count}</span>,
    },
  ]

  return (
    <div className={styles.pagina}>
      <div className={styles.cabecalho}>
        <div>
          <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
            Transferências
          </h1>
          <p className={styles.apoio}>O que mudou de conta dentro da casa em {nomeDoMesAtual}.</p>
        </div>
        <div className={styles.acoesDoTopo}>
          {/* Reprocessar é secundário: a ação frequente é olhar o mês; converter
              o que entrou como receita e despesa é o conserto de quem importou
              antes de cadastrar as palavras-chave das contas (ADR-028). */}
          <Button variant="secondary" onClick={() => setReprocessando(true)}>
            Reprocessar transferências
          </Button>
        </div>
      </div>

      {lista.isError ? (
        <Alert
          tone="error"
          title="Não foi possível carregar as transferências."
          action={
            <Button onClick={() => void lista.refetch()} loading={lista.isFetching}>
              Tentar de novo
            </Button>
          }
        >
          {messageForError(lista.error)}
        </Alert>
      ) : (
        <Panel padding="none">
          <div className={styles.faixa}>
            <div className={styles.filtros}>
              <Select
                label="Conta"
                density="compact"
                placeholder="Todas as contas"
                options={opcoesDeConta(todasAsContas)}
                value={contaId ?? ''}
                onChange={(evento) =>
                  trocarBusca({
                    conta: evento.target.value === '' ? undefined : evento.target.value,
                  })
                }
              />
              {contaId ? (
                <>
                  <span className={styles.conjuncao} aria-hidden="true">
                    e
                  </span>
                  <Select
                    label="Outra conta"
                    density="compact"
                    placeholder="Qualquer conta"
                    options={opcoesDeConta(todasAsContas.filter((conta) => conta.id !== contaId))}
                    value={contraparteId ?? ''}
                    onChange={(evento) =>
                      trocarBusca({
                        contraparte: evento.target.value === '' ? undefined : evento.target.value,
                      })
                    }
                  />
                </>
              ) : null}
            </div>

            {contaId && !contraparteId ? (
              <SaldoDaConta
                nome={nomeDaConta(contaId, saldos, contaFiltrada?.name)}
                mes={nomeDoMesAtual}
                saldo={saldos.find((saldo) => saldo.accountId === contaId)}
                carregando={isPending}
              />
            ) : null}
          </div>

          {modoPar && (isPending || pares.length > 0) ? (
            <TransferPairPanel
              conta={{ id: contaId, nome: nomeDaConta(contaId, saldos, contaFiltrada?.name) }}
              contraparte={{
                id: contraparteId,
                nome: nomeDaConta(contraparteId, saldos, contraparteFiltrada?.name),
              }}
              par={pares.find((par) => envolve(par, contaId, contraparteId))}
              saldos={saldos}
              mes={mes}
              carregando={isPending}
            />
          ) : null}

          {!modoPar && (isPending || pares.length > 0) ? (
            <section className={styles.secao} aria-labelledby="transferencias-pares">
              <h2 className={styles.tituloDaSecao} id="transferencias-pares">
                Pares de contas
              </h2>
              <DataTable
                caption="Pares de contas com transferência no mês"
                columns={colunasDePares}
                rows={pares.map(normalizarPar)}
                rowKey={(par) => par.chave}
                loading={isPending}
              />
            </section>
          ) : null}

          <section
            className={styles.secao}
            aria-labelledby={itens.length > 0 || isPending ? 'transferencias-itens' : undefined}
          >
            {itens.length > 0 || isPending ? (
              <h2 className={styles.tituloDaSecao} id="transferencias-itens">
                Transferências de {nomeDoMesAtual}
              </h2>
            ) : null}
            <DataTable
              caption={`Transferências de ${nomeDoMesAtual}`}
              columns={colunasDeItens}
              rows={itens}
              rowKey={(linha) => linha.groupId}
              loading={isPending}
              empty={
                <VazioComOrientacao
                  mes={mes}
                  nomeDaConta={
                    contaId ? nomeDaConta(contaId, saldos, contaFiltrada?.name) : undefined
                  }
                  nomeDaContraparte={
                    contraparteId
                      ? nomeDaConta(contraparteId, saldos, contraparteFiltrada?.name)
                      : undefined
                  }
                  onLimparConta={() => trocarBusca({ conta: undefined })}
                  onLimparPar={() => trocarBusca({ conta: undefined, contraparte: undefined })}
                  onImportar={() => void navigate({ to: '/importar' })}
                  onReprocessar={() => setReprocessando(true)}
                />
              }
            />
          </section>

          {itens.length > 0 ? (
            <div className={styles.rodape}>
              <p className={styles.contagem}>
                {hasNextPage
                  ? `Mostrando ${itens.length} de ${total} ${total === 1 ? 'transferência' : 'transferências'}`
                  : `${itens.length} ${itens.length === 1 ? 'transferência' : 'transferências'} — é tudo o que existe no filtro.`}
              </p>

              {/* Erro ao carregar mais NUNCA troca a tabela por um erro: as
                  linhas já lidas continuam lá e a falha fica onde estava o
                  botão. */}
              {lista.isFetchNextPageError ? (
                <Alert
                  tone="error"
                  action={
                    <Button size="sm" onClick={() => void carregarMais()}>
                      Tentar de novo
                    </Button>
                  }
                >
                  Não foi possível carregar mais transferências.
                </Alert>
              ) : hasNextPage ? (
                <Button
                  variant="secondary"
                  onClick={() => void carregarMais()}
                  loading={isFetchingNextPage}
                >
                  Carregar mais {Math.min(POR_PAGINA, Math.max(total - itens.length, 1))}
                </Button>
              ) : null}
            </div>
          ) : null}
        </Panel>
      )}

      {/* O foco fica no botão depois de carregar mais; quem não vê a tabela
          crescer precisa ouvir que ela cresceu. */}
      <p className="sr-only" role="status">
        {anuncio}
      </p>

      <DetectTransfersDialog
        open={reprocessando}
        mes={mes}
        onClose={() => setReprocessando(false)}
      />
    </div>
  )
}

// -------------------------------------------------------------- pedaços

function SaldoDaConta({
  nome,
  mes,
  saldo,
  carregando,
}: {
  nome: string
  mes: string
  saldo: TransferBalance | undefined
  carregando: boolean
}) {
  return (
    <p className={styles.saldo}>
      <span>
        Saldo da {nome} no fim de {mes}
      </span>
      {carregando || !saldo ? (
        <Skeleton width="4.5rem" height="1rem" />
      ) : (
        <MoneyText cents={saldo.balanceAtMonthEndCents} tone="semantic" />
      )}
    </p>
  )
}

/** "Nubank → C6", com a direção dita em palavras para quem não vê a seta. */
function CelulaDeContas({ de, para }: { de: string; para: string }) {
  return (
    <span className={styles.contas}>
      <span className={styles.setaEntreContas} aria-hidden="true">
        {de} <ArrowRightIcon size={14} /> {para}
      </span>
      <span className="sr-only">
        de {de} para {para}
      </span>
    </span>
  )
}

function CelulaDeDescricao({ linha }: { linha: TransferItem }) {
  const descricao = linha.description.trim()
  return (
    <>
      <span className={styles.descricao} title={descricao || undefined}>
        {descricao || <span className={styles.semDescricao}>Sem descrição</span>}
      </span>
      {/* Só aparece abaixo de 40rem, onde as colunas Contas e Registro foram
          escondidas pelo DataTable. O dado não some — ele muda de lugar. */}
      <span className={styles.secundaria}>
        <CelulaDeContas de={linha.fromAccountName} para={linha.toAccountName} />
        <span className={styles.separador} aria-hidden="true">
          ·
        </span>
        {ROTULO_DA_ORIGEM[linha.source]}
      </span>
    </>
  )
}

function VazioComOrientacao({
  mes,
  nomeDaConta,
  nomeDaContraparte,
  onLimparConta,
  onLimparPar,
  onImportar,
  onReprocessar,
}: {
  mes: string
  nomeDaConta: string | undefined
  nomeDaContraparte: string | undefined
  onLimparConta: () => void
  onLimparPar: () => void
  onImportar: () => void
  onReprocessar: () => void
}) {
  const nome = nomeDoMes(mes)

  if (nomeDaConta && nomeDaContraparte) {
    return (
      <EmptyState
        title={`Nenhuma transferência entre ${nomeDaConta} e ${nomeDaContraparte} em ${nome}.`}
        description="Troque uma das contas no filtro ou volte para todos os pares."
        action={
          <Button variant="primary" onClick={onLimparPar}>
            Mostrar todos os pares
          </Button>
        }
      />
    )
  }

  if (nomeDaConta) {
    return (
      <EmptyState
        title={`Nenhuma transferência envolvendo a ${nomeDaConta} em ${nome}.`}
        description="Troque a conta no filtro ou volte para todas."
        action={
          <Button variant="primary" onClick={onLimparConta}>
            Mostrar todas as contas
          </Button>
        }
      />
    )
  }

  return (
    <EmptyState
      title={`Nenhuma transferência entre as suas contas em ${mesPorExtenso(mes)}.`}
      description="Transferências aparecem aqui quando um Pix entre as suas contas ou o pagamento de uma fatura é registrado como transferência — na importação, o app as detecta pelas palavras-chave das contas. Se os extratos já foram importados, reprocesse para reconhecer os pares."
      action={
        <div className={styles.acoesDoVazio}>
          <Button variant="primary" onClick={onImportar}>
            Importar extrato
          </Button>
          <Button variant="quiet" onClick={onReprocessar}>
            Reprocessar transferências
          </Button>
        </div>
      }
    />
  )
}

// --------------------------------------------------------------- cálculo

/** Um par com a seta apontada para quem mandou MAIS.
 *
 *  O servidor orienta por `A` = menor id, que é bom para identificar o par e
 *  ruim para ler: "C6 → Nubank · enviado 800 · recebido 2.500" obriga a pessoa
 *  a inverter de cabeça. Normalizado, a linha diz de cara quem ficou com o
 *  dinheiro. Empate mantém a ordem do servidor e líquido `0,00`. */
export type ParNormalizado = {
  chave: string
  de: ContaDoPar
  para: ContaDoPar
  /** No sentido da seta. */
  enviadoCents: number
  /** No sentido contrário. */
  recebidoCents: number
  /** `enviado − recebido`, sempre ≥ 0. */
  liquidoCents: number
  count: number
}

export function normalizarPar(par: TransferPair): ParNormalizado {
  const a: ContaDoPar = { id: par.accountAId, nome: par.accountAName }
  const b: ContaDoPar = { id: par.accountBId, nome: par.accountBName }
  const chave = `${par.accountAId}:${par.accountBId}`

  if (par.netCents < 0) {
    return {
      chave,
      de: b,
      para: a,
      enviadoCents: par.bToACents,
      recebidoCents: par.aToBCents,
      liquidoCents: 0 - par.netCents,
      count: par.count,
    }
  }
  return {
    chave,
    de: a,
    para: b,
    enviadoCents: par.aToBCents,
    recebidoCents: par.bToACents,
    liquidoCents: par.netCents,
    count: par.count,
  }
}

function envolve(par: TransferPair, contaId: string, contraparteId: string): boolean {
  return (
    (par.accountAId === contaId && par.accountBId === contraparteId) ||
    (par.accountBId === contaId && par.accountAId === contraparteId)
  )
}

/** O nome da conta para a tela ESCREVER: o servidor manda o nome junto do
 *  saldo, e é essa a fonte quando o dado já chegou; antes disso, o cache de
 *  contas. Nunca o id cru. */
function nomeDaConta(
  id: string,
  saldos: readonly TransferBalance[],
  doCache: string | undefined,
): string {
  return saldos.find((saldo) => saldo.accountId === id)?.accountName ?? doCache ?? ''
}
