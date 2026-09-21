import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import type { InvestmentFlow, InvestmentItem, InvestmentSeriesPoint } from '@/api/types'
import { validarBusca } from '@/app/search'
import { Alert } from '@/components/Alert/Alert'
import { BarChart, type ColunaDoGrafico } from '@/components/BarChart/BarChart'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable } from '@/components/DataTable/DataTable'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { MoneyText } from '@/components/MoneyText/MoneyText'
import { Panel } from '@/components/Panel/Panel'
import { Skeleton } from '@/components/Skeleton/Skeleton'
import { categoriasQueryOptions } from '@/lib/categories'
import { dataCurta } from '@/lib/civil'
import { messageForError } from '@/lib/errors'
import { formatarDinheiro } from '@/lib/money'
import { FUSO_PADRAO, mesCurto, mesDaURL, mesPorExtenso, nomeDoMes, somarMeses } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import { investmentsInfiniteQueryOptions, POR_PAGINA } from '../api/investments'
import { DetectInvestmentsDialog } from '../components/DetectInvestmentsDialog'
import styles from './InvestmentsScreen.module.css'

/** A PALAVRA do movimento. `Record` exaustivo: um fluxo novo no contrato vira
 *  erro de compilação aqui, não `contribution` cru numa coluna.
 *
 *  É "aporte", e nunca "investimento", para o movimento: a tela conta eventos
 *  ("3 aportes em setembro"), e "3 investimentos" prometeria carteira — que é
 *  exatamente o que a spec 0006 §2.2 tira do escopo. */
const PALAVRA_DO_MOVIMENTO: Record<InvestmentFlow, string> = {
  contribution: 'Aporte',
  redemption: 'Resgate',
}

/** Os números de uma janela, como o contrato os entrega. */
type Totais = {
  contributionsCents: number
  contributionCount: number
  redemptionsCents: number
  redemptionCount: number
}

/** Tela de investimentos (spec 0006 §3.4).
 *
 *  Quanto saiu da conta para investir e quanto voltou — no mês, no ano e nos
 *  últimos 12 meses. Três decisões que esta tela sustenta:
 *
 *  1. **Cromaticamente silenciosa.** `--income`/`--expense` são PROIBIDOS aqui,
 *     e não por escassez de cor: aporte não é gasto (o dinheiro continua sendo
 *     da casa) e resgate não é ganho (é dinheiro que já era dela voltando).
 *     Pintar o aporte de vermelho ensinaria justamente o erro que a entrega
 *     existe para corrigir. Todo valor é `MoneyText` neutro, sem sinal.
 *  2. **A distinção é posição + palavra, e só então tinta.** Aporte é sempre o
 *     primeiro; o rótulo está sempre presente; a cor existe só nas marcas do
 *     gráfico (`--chart-1` e `--chart-3`, nunca 1 e 2).
 *  3. **Nada de derivado.** Sem líquido, sem saldo investido, sem variação
 *     contra o mês anterior, sem projeção. Quem quer saber "quanto eu tenho"
 *     está na tela errada, e a tela não finge o contrário.
 *
 *     ⚠️ Escopo: isto vale AQUI, e continua valendo por decisão explícita do
 *     usuário em 18/09/2026. O **painel** (`/`) mostra, desde a mesma data, o
 *     investimento do mês como UM líquido com sinal (aportes − resgates,
 *     podendo ser negativo): ele responde "quanto ficou investido neste
 *     mês", esta tela responde "quanto entrou e quanto saiu". Ver
 *     `LICOES-FRONTEND.md`. */
export function InvestmentsScreen() {
  const navigate = useNavigate()
  const tituloRef = useRef<HTMLHeadingElement>(null)

  // Validada aqui, e não lida crua de `location.search` — é a fronteira entre a
  // URL (que a pessoa edita) e a query da API.
  const buscaBruta = useRouterState({ select: (estado) => estado.location.search })
  const busca = validarBusca(buscaBruta as Record<string, unknown>)

  const session = useQuery(sessionQueryOptions)
  const fuso = session.data?.household.timezone ?? FUSO_PADRAO
  const mes = mesDaURL(busca.mes, fuso)

  const categorias = useQuery(categoriasQueryOptions(false))
  const lista = useInfiniteQuery({
    ...investmentsInfiniteQueryOptions(mes),
    // Trocar de mês é a interação principal desta tela: o quadro não pode
    // piscar a cada seta. Com dado na tela, o refetch baixa a opacidade em vez
    // de trocar tudo por esqueleto.
    placeholderData: keepPreviousData,
  })

  const [detectando, setDetectando] = useState(false)
  const [anuncio, setAnuncio] = useState('')

  useEffect(() => {
    document.title = 'Investimentos · HomeFinance'
  }, [])

  // Navegação de rota move o foco para o <h1> da tela nova (docs/DESIGN.md).
  useEffect(() => {
    tituloRef.current?.focus()
  }, [])

  const paginas = lista.data?.pages ?? []
  const primeira = paginas[0]
  const itens = paginas.flatMap((pagina) => pagina.items)
  const { hasNextPage, isFetchingNextPage, isPending } = lista
  const atualizando = lista.isFetching && !isPending && !isFetchingNextPage

  const nomeDoMesAtual = nomeDoMes(mes)
  const total =
    (primeira?.monthly.contributionCount ?? 0) + (primeira?.monthly.redemptionCount ?? 0)

  // O eixo e a base do gráfico derivam do MÊS DA URL, não do servidor: enquanto
  // carrega, eles já são reais e só as barras são platô.
  const serie = primeira?.series ?? serieVazia(mes)
  const maximoCents = maximoDaSerie(serie)
  const temSerie = maximoCents > 0

  // A árvore vem sem arquivadas (`includeArchived=false`), então toda categoria
  // que chega aqui está ativa.
  const arvore = categorias.data
  const semCategoria =
    arvore !== undefined && arvore.investment.length === 0 && arvore.redemption.length === 0

  async function carregarMais() {
    const antes = itens.length
    const resultado = await lista.fetchNextPage()
    const depois = (resultado.data?.pages ?? []).flatMap((pagina) => pagina.items).length
    const novas = depois - antes
    const quantas =
      novas === 1 ? 'Mais 1 lançamento carregado' : `Mais ${novas} lançamentos carregados`
    setAnuncio(`${quantas}. ${depois} de ${total}.`)
  }

  function irParaCategorias() {
    void navigate({ to: '/categorias', search: { mes } })
  }

  const colunas: readonly Column<InvestmentItem>[] = [
    {
      key: 'data',
      header: 'Data',
      width: 'min',
      render: (linha) => dataCurta(linha.occurredOn),
    },
    {
      key: 'movimento',
      header: 'Movimento',
      width: 'min',
      // Abaixo de 40rem a coluna some e a palavra reaparece na segunda linha da
      // descrição — em --ink, nunca apagada: é o portador principal da
      // distinção e não pode enfraquecer na tela estreita.
      hideBelow: 'sm',
      // Texto puro, nunca `Badge`: dezoito etiquetas numa coluna em que toda
      // linha tem valor viram poluição.
      render: (linha) => (
        <span className={styles.movimento}>{PALAVRA_DO_MOVIMENTO[linha.flow]}</span>
      ),
    },
    {
      key: 'conta',
      header: 'Conta',
      width: 'min',
      hideBelow: 'sm',
      render: (linha) => <span className={styles.apoio}>{linha.accountName}</span>,
    },
    {
      key: 'descricao',
      header: 'Descrição',
      render: (linha) => <CelulaDeDescricao linha={linha} />,
    },
    {
      key: 'categoria',
      header: 'Categoria',
      width: 'min',
      hideBelow: 'sm',
      render: (linha) => <span className={styles.categoria}>{linha.categoryName}</span>,
    },
    {
      key: 'valor',
      header: 'Valor',
      align: 'end',
      width: 'min',
      // Neutro e SEM SINAL: aporte não é despesa e resgate não é receita. A
      // palavra da coluna Movimento é quem diz a direção.
      render: (linha) => <MoneyText cents={linha.amountCents} />,
    },
  ]

  const colunasDaTabelaDeMeses: readonly Column<InvestmentSeriesPoint>[] = [
    {
      key: 'mes',
      header: 'Mês',
      // Por extenso: abreviação existe só no eixo decorativo do gráfico.
      render: (ponto) => mesPorExtenso(ponto.month),
    },
    {
      key: 'aportes',
      header: 'Aportes',
      align: 'end',
      width: 'min',
      // Mês sem movimento mostra `0,00`, nunca travessão: zero é um valor, e a
      // coluna tabular o faz recuar sozinho.
      render: (ponto) => <MoneyText cents={ponto.contributionsCents} />,
    },
    {
      key: 'resgates',
      header: 'Resgates',
      align: 'end',
      width: 'min',
      render: (ponto) => <MoneyText cents={ponto.redemptionsCents} />,
    },
  ]

  return (
    <div className={styles.pagina}>
      <div className={styles.cabecalho}>
        <div>
          <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
            Investimentos
          </h1>
          {/* A frase diz de saída que aporte é dinheiro que SAI da conta — a
              dúvida número um desta tela. */}
          <p className={styles.apoioDoTitulo}>
            O que saiu para investir e o que voltou em {nomeDoMesAtual}.
          </p>
        </div>
        {/* Sem nenhuma categoria a tela inteira é o vazio abaixo, que já tem a
            sua própria chamada: dois convites para o mesmo passo é ruído. Com
            categoria e sem palavra-chave o botão CONTINUA existindo — é o
            diálogo que ensina. Nunca `disabled`. */}
        {semCategoria ? null : (
          <div className={styles.acoesDoTopo}>
            <Button variant="secondary" onClick={() => setDetectando(true)}>
              Detectar investimentos
            </Button>
          </div>
        )}
      </div>

      {lista.isError ? (
        <Alert
          tone="error"
          title="Não foi possível carregar os investimentos."
          action={
            <Button onClick={() => void lista.refetch()} loading={lista.isFetching}>
              Tentar de novo
            </Button>
          }
        >
          {messageForError(lista.error)}
        </Alert>
      ) : semCategoria ? (
        <Panel>
          <EmptyState
            title="Nenhuma categoria de investimento ainda."
            description="Esta tela mostra o que sai da conta para investir e o que volta em resgates. Crie em Categorias um grupo de natureza Investimentos — CDB, Tesouro, previdência — e os lançamentos passam a aparecer aqui."
            action={
              <Button variant="primary" onClick={irParaCategorias}>
                Ir para categorias
              </Button>
            }
          />
        </Panel>
      ) : (
        <Panel padding="none">
          <section className={styles.secao} aria-busy={atualizando || undefined}>
            <div className={styles.numeros}>
              {/* Colunas por PERÍODO, não por fluxo: a comparação que a pessoa
                  faz é "este mês contra o ano". */}
              <ColunaDeNumeros
                titulo={`Em ${nomeDoMesAtual}`}
                sufixo={` em ${nomeDoMesAtual}`}
                totais={primeira?.monthly}
                carregando={isPending}
                destaque
              />
              <ColunaDeNumeros
                titulo={`No ano, até ${nomeDoMesAtual}`}
                sufixo={` no ano, até ${nomeDoMesAtual}`}
                totais={primeira?.yearToDate}
                carregando={isPending}
              />
            </div>
          </section>

          {/* Série inteiramente zerada: a seção NÃO é renderizada. Nem gráfico
              vazio, nem 12 linhas de 0,00 — não há o que ilustrar, e o vazio da
              lista já diz o que fazer. */}
          {isPending || temSerie ? (
            <section
              className={styles.secao}
              aria-labelledby="investimentos-serie"
              aria-busy={atualizando || undefined}
            >
              <h2 className={styles.tituloDaSecao} id="investimentos-serie">
                Últimos 12 meses, até {nomeDoMesAtual}
              </h2>
              <div className={styles.containerDaSerie}>
                <div className={styles.serie}>
                  <BarChart
                    colunas={colunasDaSerie(serie)}
                    series={SERIES_DO_GRAFICO}
                    maximoCents={maximoCents}
                    legenda={legendaDoGrafico(maximoCents)}
                    loading={isPending}
                  />
                  <div className={styles.tabelaDeMeses}>
                    {/* A FONTE DA VERDADE: o gráfico é ilustração, e o caminho
                        de teclado são estas 12 linhas. */}
                    <DataTable
                      caption={captionDaSerie(serie)}
                      columns={colunasDaTabelaDeMeses}
                      rows={serie}
                      rowKey={(ponto) => ponto.month}
                      loading={isPending}
                    />
                  </div>
                </div>
              </div>
            </section>
          ) : null}

          <section
            className={styles.secao}
            aria-labelledby={itens.length > 0 || isPending ? 'investimentos-itens' : undefined}
            aria-busy={atualizando || undefined}
          >
            {itens.length > 0 || isPending ? (
              <h2 className={styles.tituloDaSecao} id="investimentos-itens">
                Lançamentos de {nomeDoMesAtual}
              </h2>
            ) : null}
            {/* Lista PLANA, na ordem do servidor: sem agrupamento por dia e sem
                subtotal — aporte e resgate não somam, e um cabeçalho de dia sem
                subtotal é só ruído. */}
            <DataTable
              caption={`Aportes e resgates de ${mesPorExtenso(mes)}`}
              columns={colunas}
              rows={itens}
              rowKey={(linha) => linha.id}
              loading={isPending}
              empty={
                <EmptyState
                  title={`Nenhum aporte ou resgate em ${mesPorExtenso(mes)}.`}
                  description="Um lançamento aparece aqui quando recebe uma categoria de investimento ou de resgate. Se o extrato do mês já foi importado, detecte pelas palavras-chave."
                  action={
                    <div className={styles.acoesDoVazio}>
                      <Button variant="primary" onClick={() => setDetectando(true)}>
                        Detectar investimentos
                      </Button>
                      <Button
                        variant="quiet"
                        onClick={() => void navigate({ to: '/lancamentos', search: { mes } })}
                      >
                        Ver os lançamentos de {nomeDoMesAtual}
                      </Button>
                    </div>
                  }
                />
              }
            />
          </section>

          {itens.length > 0 ? (
            <div className={styles.rodape}>
              <p className={styles.contagem}>
                {hasNextPage
                  ? `Mostrando ${itens.length} de ${total} ${total === 1 ? 'lançamento' : 'lançamentos'}`
                  : `${itens.length} ${itens.length === 1 ? 'lançamento' : 'lançamentos'} — é tudo o que existe no mês.`}
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
                  Não foi possível carregar mais lançamentos.
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

      <DetectInvestmentsDialog
        open={detectando}
        mes={mes}
        onClose={() => setDetectando(false)}
        onIrParaCategorias={irParaCategorias}
      />
    </div>
  )
}

/** Aporte primeiro e com `--chart-1`; resgate depois e com `--chart-3`. Um
 *  passo de separação basta para vizinhas de uma rampa ordinal; duas séries que
 *  se comparam barra contra barra precisam do dobro da distância. */
const SERIES_DO_GRAFICO = [
  { papel: '1', nome: 'Aportes' },
  { papel: '3', nome: 'Resgates' },
] as const

// -------------------------------------------------------------- pedaços

/** Uma coluna de números: o título do PERÍODO e uma `<dl>` de duas linhas.
 *
 *  Não são quatro cartões com número gigante e ícone: conferir o que saiu e o
 *  que voltou é uma leitura vertical. Aporte é sempre a primeira linha. */
function ColunaDeNumeros({
  titulo,
  sufixo,
  totais,
  carregando,
  destaque = false,
}: {
  titulo: string
  /** O período, em `sr-only`: quatro rótulos "Aportes"/"Resgates" soltos numa
   *  lista de definição não se distinguem no áudio. */
  sufixo: string
  totais: Totais | undefined
  carregando: boolean
  /** O mês leva `emphasis="total"`; o ano fica no corpo normal. Dois degraus de
   *  hierarquia com variantes que já existem — nenhum número hero nesta tela. */
  destaque?: boolean
}) {
  return (
    <div className={styles.coluna}>
      <p className={styles.tituloDaLista}>{titulo}</p>
      <dl className={styles.lista}>
        <LinhaDeNumero
          palavra="Aportes"
          sufixo={sufixo}
          cents={totais?.contributionsCents}
          contagem={totais?.contributionCount}
          carregando={carregando}
          destaque={destaque}
        />
        <LinhaDeNumero
          palavra="Resgates"
          sufixo={sufixo}
          cents={totais?.redemptionsCents}
          contagem={totais?.redemptionCount}
          carregando={carregando}
          destaque={destaque}
        />
      </dl>
    </div>
  )
}

function LinhaDeNumero({
  palavra,
  sufixo,
  cents,
  contagem,
  carregando,
  destaque,
}: {
  palavra: string
  sufixo: string
  cents: number | undefined
  contagem: number | undefined
  carregando: boolean
  destaque: boolean
}) {
  const mostrando = !carregando && cents !== undefined && contagem !== undefined
  return (
    <div className={styles.linha}>
      <dt className={styles.termo}>
        <span>
          {palavra}
          <span className="sr-only">{sufixo}</span>
        </span>
        {/* Carregando NÃO mostra a linha de contagem: contagem inventada é pior
            que ausência. */}
        {mostrando ? (
          <span className={styles.contagemDaLinha}>{fraseDaContagem(contagem)}</span>
        ) : null}
      </dt>
      <dd className={styles.valor}>
        {mostrando ? (
          <MoneyText cents={cents} emphasis={destaque ? 'total' : 'normal'} />
        ) : (
          <Skeleton width="4.5rem" height="1rem" />
        )}
      </dd>
    </div>
  )
}

function CelulaDeDescricao({ linha }: { linha: InvestmentItem }) {
  const descricao = linha.description.trim()
  return (
    <>
      <span className={styles.descricao} title={descricao || undefined}>
        {descricao || <span className={styles.semDescricao}>Sem descrição</span>}
      </span>
      {/* Só aparece abaixo de 40rem, onde o DataTable escondeu Movimento, Conta
          e Categoria. O dado não some — ele muda de lugar. */}
      <span className={styles.secundaria}>
        <span className={styles.movimento}>{PALAVRA_DO_MOVIMENTO[linha.flow]}</span>
        <span className={styles.separador} aria-hidden="true">
          ·
        </span>
        {linha.accountName}
        <span className={styles.separador} aria-hidden="true">
          ·
        </span>
        {linha.categoryName}
      </span>
    </>
  )
}

// --------------------------------------------------------------- cálculo

/** Os 12 meses da série, com zeros: o esqueleto do gráfico e da tabela enquanto
 *  o servidor não respondeu. Deriva do mês da URL, então o eixo já é o certo. */
export function serieVazia(mes: string): InvestmentSeriesPoint[] {
  return Array.from({ length: 12 }, (_, indice) => ({
    month: somarMeses(mes, indice - 11),
    contributionsCents: 0,
    redemptionsCents: 0,
  }))
}

/** O maior dos 24 inteiros — SELEÇÃO (`Math.max`), nunca cálculo. O cliente não
 *  faz aritmética de dinheiro (ADR-003). */
export function maximoDaSerie(serie: readonly InvestmentSeriesPoint[]): number {
  return serie.reduce(
    (maximo, ponto) => Math.max(maximo, ponto.contributionsCents, ponto.redemptionsCents),
    0,
  )
}

/** A série do contrato virada em colunas do gráfico.
 *
 *  Aporte é SEMPRE o primeiro valor (barra da esquerda, `--chart-1`); resgate é
 *  sempre o segundo (`--chart-3`). A virada de ano é marcada ANTES da primeira
 *  coluna de um ano novo — nunca na primeira coluna da série, que não tem
 *  vizinha à esquerda. */
export function colunasDaSerie(serie: readonly InvestmentSeriesPoint[]): ColunaDoGrafico[] {
  return serie.map((ponto, indice) => {
    const anterior = serie[indice - 1]
    const porExtenso = mesPorExtenso(ponto.month)
    const coluna: ColunaDoGrafico = {
      key: ponto.month,
      rotulo: mesCurto(ponto.month),
      valores: [
        { papel: '1', cents: ponto.contributionsCents },
        { papel: '3', cents: ponto.redemptionsCents },
      ],
      titulos: [
        `${porExtenso} · Aportes · ${formatarDinheiro(ponto.contributionsCents)}`,
        `${porExtenso} · Resgates · ${formatarDinheiro(ponto.redemptionsCents)}`,
      ],
    }
    if (anterior !== undefined && anoDe(ponto.month) !== anoDe(anterior.month)) {
      coluna.viradaDeAno = true
    }
    return coluna
  })
}

function anoDe(mes: string): string {
  return mes.slice(0, 4)
}

/** A escala é dita em PALAVRAS, porque o desenho não tem eixo Y, nem malha, nem
 *  número sobre barra. */
export function legendaDoGrafico(maximoCents: number): string {
  return `Aportes e resgates dos últimos 12 meses — a barra mais alta é ${formatarDinheiro(maximoCents)}. Os valores mês a mês estão na tabela.`
}

export function captionDaSerie(serie: readonly InvestmentSeriesPoint[]): string {
  const primeiro = serie[0]
  const ultimo = serie[serie.length - 1]
  if (!primeiro || !ultimo) return 'Aportes e resgates mês a mês'
  return `Aportes e resgates mês a mês, de ${mesPorExtenso(primeiro.month)} a ${mesPorExtenso(ultimo.month)}`
}

function fraseDaContagem(contagem: number): string {
  if (contagem === 0) return 'nenhum lançamento'
  if (contagem === 1) return '1 lançamento'
  return `${contagem} lançamentos`
}
