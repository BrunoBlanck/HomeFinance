import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import type { Transaction, TransactionList } from '@/api/types'
import { aplicarNaBusca, type MudancaDeBusca, validarBusca } from '@/app/search'
import { Alert } from '@/components/Alert/Alert'
import { Badge } from '@/components/Badge/Badge'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable, type RowGroup } from '@/components/DataTable/DataTable'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { TrashIcon } from '@/components/icons/TrashIcon'
import { MoneyText } from '@/components/MoneyText/MoneyText'
import { Panel } from '@/components/Panel/Panel'
import { Select } from '@/components/Select/Select'
import { Skeleton } from '@/components/Skeleton/Skeleton'
import { useToast } from '@/components/Toast/Toast'
import { contaPorId, contasQueryOptions, opcoesDeConta } from '@/lib/accounts'
import { dataCompleta, diaPorExtenso } from '@/lib/civil'
import { messageForError } from '@/lib/errors'
import { useFocoNoTitulo } from '@/lib/focus'
import { formatarDinheiro } from '@/lib/money'
import { FUSO_PADRAO, mesDaURL, nomeDoMes } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import {
  deleteTransaction,
  ehTransferencia,
  POR_PAGINA,
  transactionsInfiniteQueryOptions,
  transactionsQueryKey,
  valorComSinal,
} from '../api/transactions'
import {
  BotaoSemCategoria,
  EditorDeCategoria,
  type GravacaoConcluida,
  type InstanciaDoAtalho,
  podeCategorizar,
  proximaLacuna,
} from '../components/AtalhoDeCategoria'
import { AutoCategorizeDialog } from '../components/AutoCategorizeDialog'
import { ExcluirLancamentoDialog } from '../components/ExcluirLancamentoDialog'
import { rotuloDaLinha } from '../lexico'
import styles from './TransactionsScreen.module.css'

/** Tela de lançamentos.
 *
 *  Uma tabela densa do mês, agrupada por DIA, com o subtotal do dia no
 *  cabeçalho do grupo. Agrupar por dia não é estética: é como se lê um extrato
 *  — a pessoa procura "o que aconteceu na terça", não a linha 47.
 *
 *  Três regras do dinheiro que esta tela sustenta:
 *
 *  1. **O sinal é explícito** (`+1.600,00` / `−11,00`). Numa coluna onde receita
 *     e despesa convivem, a direção não pode depender de cor: em escala de
 *     cinza, no daltonismo ou impresso, `--income` e `--expense` viram o mesmo
 *     tom.
 *  2. **Transferência é neutra e não entra no subtotal.** Ela não é receita nem
 *     despesa — dinheiro que troca de bolso dentro da casa não mudou o
 *     patrimônio da casa. Quando o dia tem transferência, o cabeçalho do dia
 *     diz `· 1 transferência`: é a explicação de por que as linhas visíveis não
 *     fecham com o subtotal.
 *  3. **Os totais vêm do servidor.** Somar centavos também aqui criaria duas
 *     fontes para o mesmo número, e elas divergem.
 *
 *  E uma da dívida: lançamento importado nasce **sem categoria** por decisão de
 *  arquitetura, e a faixa de pendência é o que impede essa dívida de ficar
 *  invisível. Ela filtra a própria tela e some sozinha quando zera. A célula
 *  `Sem categoria` é o atalho para pagar essa dívida linha a linha
 *  (spec 0005 §11): a linha se expande num editor — uma aberta por vez. */
export function TransactionsScreen() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const toast = useToast()
  // Foco no <h1> na entrada da rota (docs/DESIGN.md).
  const tituloRef = useFocoNoTitulo()
  const carregarMaisRef = useRef<HTMLDivElement>(null)

  // A busca é VALIDADA aqui, e não lida crua de `location.search`.
  //
  // Isso não é cinto e suspensório: `location.search` é o resultado do PARSE da
  // URL, não do `validateSearch` — um `?conta=' OR 1=1 --` sobrevive ali
  // inteiro. Ler de lá direto mandaria a entrada da pessoa para dentro de uma
  // query da API. Passar por `validarBusca` é o mesmo que a casca já faz com o
  // mês em `mesDaURL`.
  const buscaBruta = useRouterState({ select: (estado) => estado.location.search })
  const busca = validarBusca(buscaBruta as Record<string, unknown>)
  const transferenciaPendente = useRouterState({
    select: (estado) => estado.location.state.novaTransferencia,
  })

  const session = useQuery(sessionQueryOptions)
  const fuso = session.data?.household.timezone ?? FUSO_PADRAO
  const mes = mesDaURL(busca.mes, fuso)
  const contaId = busca.conta
  const soSemCategoria = busca.semCategoria === 1

  const contas = useQuery(contasQueryOptions(false))
  const lista = useInfiniteQuery(
    transactionsInfiniteQueryOptions({ mes, ...(contaId ? { contaId } : {}) }),
  )

  const [alvoDaExclusao, setAlvoDaExclusao] = useState<Transaction | null>(null)
  const [erroDaExclusao, setErroDaExclusao] = useState<string | undefined>(undefined)
  const [categorizando, setCategorizando] = useState(false)
  const [anuncio, setAnuncio] = useState('')

  // A linha com o editor de categoria aberto — uma por vez. Guarda a busca em
  // que abriu: trocar mês, conta ou filtro fecha, sem efeito e sem tocar no
  // foco (o estado simplesmente deixa de valer para a busca nova).
  const chaveDaBusca = `${mes}|${contaId ?? ''}|${soSemCategoria ? 1 : 0}`
  const [editando, setEditando] = useState<{ id: string; busca: string } | null>(null)
  const editandoId = editando?.busca === chaveDaBusca ? editando.id : null

  // Depois de gravar, o foco vai para a PRÓXIMA lacuna — mas só numa
  // renderização que já tenha o dado novo (`dataUpdatedAt`): as linhas que o
  // reprocessamento categorizou já perderam a lacuna, e focar uma que está
  // prestes a sumir jogaria o foco no <body>.
  const [focoPendente, setFocoPendente] = useState<
    (GravacaoConcluida & { aPartirDe: number }) | null
  >(null)

  useEffect(() => {
    document.title = 'Lançamentos · HomeFinance'
  }, [])

  const paginas = lista.data?.pages ?? []
  const resumo = paginas[0]?.summary
  const visiveis = linhasVisiveis(paginas, soSemCategoria)
  const nomeDoMesAtual = nomeDoMes(mes)

  const { hasNextPage, isFetchingNextPage, isPending, fetchNextPage } = lista

  // Com o filtro ligado, a tela pode receber uma página inteira sem nenhuma
  // linha sem categoria — o contrato da E2 filtra por mês e conta, não por
  // categoria. Buscar a próxima é o que honra a promessa do botão que ligou o
  // filtro ("Ver só esses 12"); o laço para sozinho quando `hasNextPage` acaba.
  useEffect(() => {
    if (!soSemCategoria) return
    if (visiveis.length > 0) return
    if (!hasNextPage || isFetchingNextPage || isPending) return
    void fetchNextPage()
  }, [soSemCategoria, visiveis.length, hasNextPage, isFetchingNextPage, isPending, fetchNextPage])

  const { dataUpdatedAt } = lista
  useEffect(() => {
    if (!focoPendente || dataUpdatedAt < focoPendente.aPartirDe) return
    setFocoPendente(null)
    const proxima = proximaLacuna(focoPendente.id, focoPendente.lacunasAntes)
    if (proxima) {
      proxima.focus()
      return
    }
    // Sem lacuna à vista: o botão de carregar mais, se existir; senão o título.
    const carregarMais = carregarMaisRef.current?.querySelector<HTMLElement>('button')
    ;(carregarMais ?? tituloRef.current)?.focus()
  }, [focoPendente, dataUpdatedAt])

  const excluir = useMutation({
    mutationFn: (lancamento: Transaction) => deleteTransaction(lancamento.id),
    onSuccess: async (_resultado, lancamento) => {
      // Invalida também contas: excluir lançamento recalcula o saldo, e a tela
      // de contas não pode ficar mostrando o saldo de antes.
      await queryClient.invalidateQueries({ queryKey: ['transactions'] })
      await queryClient.invalidateQueries({ queryKey: ['accounts'] })
      setAlvoDaExclusao(null)
      setErroDaExclusao(undefined)
      toast.sucesso(
        ehTransferencia(lancamento.kind)
          ? 'Transferência excluída — as duas pernas.'
          : 'Lançamento excluído.',
      )
    },
    onError: (error) => setErroDaExclusao(messageForError(error)),
  })

  function trocarBusca(mudanca: MudancaDeBusca) {
    void navigate({
      to: '/lancamentos',
      search: (anterior) => aplicarNaBusca(anterior, mudanca),
      replace: true,
    })
  }

  function alternarEditor(id: string) {
    setEditando((atual) =>
      atual?.id === id && atual.busca === chaveDaBusca ? null : { id, busca: chaveDaBusca },
    )
  }

  function aoGravar(gravacao: GravacaoConcluida) {
    setEditando(null)
    const estado = queryClient.getQueryState(
      transactionsQueryKey({ mes, ...(contaId ? { contaId } : {}) }),
    )
    setFocoPendente({ ...gravacao, aPartirDe: estado?.dataUpdatedAt ?? 0 })
  }

  async function carregarMais() {
    const antes = visiveis.length
    const resultado = await fetchNextPage()
    const depois = linhasVisiveis(resultado.data?.pages ?? [], soSemCategoria)
    const novas = depois.length - antes
    const total = totalDoFiltro(resultado.data?.pages[0]?.summary, soSemCategoria)
    const quantas =
      novas === 1 ? 'Mais 1 lançamento carregado' : `Mais ${novas} lançamentos carregados`
    setAnuncio(`${quantas}. ${depois.length} de ${total}.`)
  }

  const colunas: readonly Column<Transaction>[] = [
    {
      key: 'conta',
      header: 'Conta',
      width: 'min',
      hideBelow: 'sm',
      render: (linha) => <span className={styles.conta}>{linha.accountName}</span>,
    },
    {
      key: 'categoria',
      header: 'Categoria',
      width: 'min',
      hideBelow: 'sm',
      render: (linha) => (
        <CelulaDeCategoria
          linha={linha}
          aberto={editandoId === linha.id}
          onAlternar={() => alternarEditor(linha.id)}
        />
      ),
    },
    {
      key: 'descricao',
      header: 'Descrição',
      render: (linha) => (
        <CelulaDeDescricao
          linha={linha}
          aberto={editandoId === linha.id}
          onAlternar={() => alternarEditor(linha.id)}
        />
      ),
    },
    {
      key: 'valor',
      header: 'Valor',
      align: 'end',
      width: 'min',
      render: (linha) => (
        <MoneyText
          cents={valorComSinal(linha.kind, linha.amountCents)}
          tone={ehTransferencia(linha.kind) ? 'neutral' : 'semantic'}
          sign="always"
        />
      ),
    },
    {
      key: 'acoes',
      header: 'Ações',
      headerHidden: true,
      align: 'end',
      width: 'min',
      render: (linha) => (
        <Button
          variant="quiet"
          size="sm"
          onClick={() => {
            setErroDaExclusao(undefined)
            setAlvoDaExclusao(linha)
          }}
          // Nunca só "Excluir": numa tabela de 50 linhas, cinquenta botões com
          // o mesmo nome deixam quem navega por lista de controles sem saber
          // qual é qual — e aqui errar a linha apaga dinheiro.
          aria-label={`Excluir ${rotuloDaLinha(linha)}`}
          iconStart={<TrashIcon size={16} />}
        >
          {''}
        </Button>
      ),
    },
  ]

  const grupos: readonly RowGroup<Transaction>[] = agruparPorDia(visiveis).map((dia) => ({
    key: dia.data,
    label: rotuloDoDia(dia),
    trailing: (
      <>
        <span className="sr-only">Subtotal do dia </span>
        <MoneyText cents={dia.subtotalCents} tone="semantic" sign="always" />
      </>
    ),
    rows: dia.linhas,
  }))

  const total = totalDoFiltro(resumo, soSemCategoria)
  const contaFiltrada = contaPorId(contas.data?.items ?? [], contaId)
  const procurandoNoResto =
    soSemCategoria && visiveis.length === 0 && (hasNextPage || isFetchingNextPage)

  return (
    <div className={styles.pagina}>
      <div className={styles.cabecalho}>
        <div>
          <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
            Lançamentos
          </h1>
          <p className={styles.apoio}>Tudo o que entrou e saiu em {nomeDoMesAtual}.</p>
        </div>
        <div className={styles.acoesDoTopo}>
          <Button variant="secondary" onClick={() => void navigate({ to: '/importar' })}>
            Importar extrato
          </Button>
        </div>
      </div>

      {/* Chegou de `/importar/{id}/resultado` com um pagamento de fatura que a
          pessoa deixou de fora. O aviso é a continuação daquele texto: enquanto
          a transferência não for registrada, a dívida do cartão não volta a
          zero. */}
      {transferenciaPendente ? (
        <Alert tone="warning" title="Falta registrar o pagamento da fatura">
          O pagamento de {formatarDinheiro(transferenciaPendente.valorCents)} de{' '}
          {dataCompleta(transferenciaPendente.data)}, na {transferenciaPendente.contaOrigemNome},
          ficou de fora da importação. Enquanto ele não for registrado como transferência para o
          cartão, a fatura continua com o valor cheio: o dinheiro já saiu da conta, mas a dívida do
          cartão não foi abatida.
        </Alert>
      ) : null}

      <FaixaSemCategoria
        pendentes={resumo?.uncategorizedCount ?? 0}
        carregando={isPending}
        filtroAtivo={soSemCategoria}
        mes={nomeDoMesAtual}
        onFiltrar={() => trocarBusca({ semCategoria: 1 })}
        onLimpar={() => trocarBusca({ semCategoria: undefined })}
        onCategorizar={() => setCategorizando(true)}
      />

      {lista.isError ? (
        <Alert
          tone="error"
          title="Não foi possível carregar os lançamentos."
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
            <Select
              label="Conta"
              density="compact"
              placeholder="Todas as contas"
              options={opcoesDeConta(contas.data?.items ?? [])}
              value={contaId ?? ''}
              onChange={(evento) =>
                trocarBusca({ conta: evento.target.value === '' ? undefined : evento.target.value })
              }
            />
            <Resumo
              carregando={isPending}
              entrouCents={resumo?.incomeCents ?? 0}
              saiuCents={resumo?.expenseCents ?? 0}
              resultadoCents={resumo?.netCents ?? 0}
              aportadoCents={resumo?.investedCents ?? 0}
              resgatadoCents={resumo?.redeemedCents ?? 0}
            />
          </div>

          <DataTable
            caption={`Lançamentos de ${nomeDoMesAtual}, agrupados por dia`}
            columns={colunas}
            groups={grupos}
            rowKey={(linha) => linha.id}
            loading={isPending}
            detail={(linha) =>
              linha.id === editandoId ? (
                <EditorDeCategoria
                  linha={linha}
                  mes={mes}
                  onCancelar={() => setEditando(null)}
                  onGravado={aoGravar}
                />
              ) : null
            }
            empty={
              procurandoNoResto ? (
                <p className={styles.procurando} role="status">
                  Procurando lançamentos sem categoria nas próximas páginas…
                </p>
              ) : (
                <VazioComOrientacao
                  filtroSemCategoria={soSemCategoria}
                  nomeDaConta={contaFiltrada?.name}
                  mes={nomeDoMesAtual}
                  onLimparCategoria={() => trocarBusca({ semCategoria: undefined })}
                  onLimparConta={() => trocarBusca({ conta: undefined })}
                  onImportar={() => void navigate({ to: '/importar' })}
                />
              )
            }
          />

          {visiveis.length > 0 ? (
            <div className={styles.rodape}>
              <p className={styles.contagem}>
                {hasNextPage
                  ? `Mostrando ${visiveis.length} de ${total} ${total === 1 ? 'lançamento' : 'lançamentos'}`
                  : `${total} ${total === 1 ? 'lançamento' : 'lançamentos'} — é tudo o que existe no filtro.`}
              </p>

              {/* Erro ao carregar mais NUNCA troca a tabela por um erro: as
                  linhas já lidas continuam lá e a falha fica onde estava o
                  botão. Perder 50 linhas carregadas por causa da 51ª é castigo
                  desproporcional. */}
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
                <div ref={carregarMaisRef}>
                  <Button
                    variant="secondary"
                    onClick={() => void carregarMais()}
                    loading={isFetchingNextPage}
                  >
                    Carregar mais {Math.min(POR_PAGINA, Math.max(total - visiveis.length, 1))}
                  </Button>
                </div>
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

      <ExcluirLancamentoDialog
        lancamento={alvoDaExclusao}
        {...(alvoDaExclusao ? { contraparte: acharContraparte(visiveis, alvoDaExclusao) } : {})}
        onCancel={() => {
          setAlvoDaExclusao(null)
          setErroDaExclusao(undefined)
        }}
        onConfirm={() => {
          if (alvoDaExclusao) excluir.mutate(alvoDaExclusao)
        }}
        excluindo={excluir.isPending}
        erro={erroDaExclusao}
      />

      <AutoCategorizeDialog
        open={categorizando}
        mes={mes}
        onClose={() => setCategorizando(false)}
      />
    </div>
  )
}

// -------------------------------------------------------------- pedaços

function FaixaSemCategoria({
  pendentes,
  carregando,
  filtroAtivo,
  mes,
  onFiltrar,
  onLimpar,
  onCategorizar,
}: {
  pendentes: number
  carregando: boolean
  filtroAtivo: boolean
  mes: string
  onFiltrar: () => void
  onLimpar: () => void
  onCategorizar: () => void
}) {
  // O botão de categorizar aparece sempre que HÁ pendência — inclusive quando a
  // casa ainda não cadastrou palavra-chave nenhuma: nesse caso é o diálogo,
  // com o estado vazio e o caminho para as categorias, que ensina.
  const temPendencia = !carregando && pendentes > 0
  const categorizar = (
    <Button variant="primary" size="sm" onClick={onCategorizar}>
      Categorizar automaticamente
    </Button>
  )

  if (filtroAtivo) {
    return (
      <Alert
        tone="info"
        action={
          <div className={styles.acoesDaFaixa}>
            {temPendencia ? categorizar : null}
            <Button size="sm" onClick={onLimpar}>
              Mostrar todos os lançamentos
            </Button>
          </div>
        }
      >
        Mostrando só os lançamentos sem categoria de {mes}.
      </Alert>
    )
  }

  // Carregando não renderiza: inventar contagem enquanto o número não chegou
  // seria pior do que não dizer nada. E com zero a faixa some sozinha — ela não
  // é dispensável por "X", porque dívida dispensada é dívida invisível.
  if (!temPendencia) return null

  const umSo = pendentes === 1
  return (
    <Alert
      tone="warning"
      action={
        <div className={styles.acoesDaFaixa}>
          {categorizar}
          <Button size="sm" onClick={onFiltrar}>
            {umSo ? 'Ver esse lançamento' : `Ver só esses ${pendentes}`}
          </Button>
        </div>
      }
    >
      {/* A frase antiga dizia que o lançamento sem categoria "não entra em
          nenhum relatório". Deixou de ser verdade com a E6a: ele entra, como
          "Sem categoria" — e é justamente por isso que a pendência precisa ser
          paga. O orçamento continua fora (E5). */}
      {umSo
        ? `1 lançamento de ${mes} está sem categoria. Ele não entra em nenhum orçamento, e nos relatórios aparece como "Sem categoria".`
        : `${pendentes} lançamentos de ${mes} estão sem categoria. Eles não entram em nenhum orçamento, e nos relatórios aparecem como "Sem categoria".`}
    </Alert>
  )
}

function Resumo({
  carregando,
  entrouCents,
  saiuCents,
  resultadoCents,
  aportadoCents,
  resgatadoCents,
}: {
  carregando: boolean
  entrouCents: number
  saiuCents: number
  resultadoCents: number
  /** Aportes do mês. Desde a spec 0006 §3.5.2 eles NÃO entram em `Saiu` nem em
   *  `Resultado` — e é a segunda linha da faixa que diz isso. */
  aportadoCents: number
  /** Resgates do mês, fora de `Entrou` e de `Resultado` pelo mesmo motivo. */
  resgatadoCents: number
}) {
  if (carregando) {
    return (
      <p className={styles.resumo}>
        <Skeleton width="4.5rem" height="1rem" />
        <Skeleton width="4.5rem" height="1rem" />
        <Skeleton width="4.5rem" height="1rem" />
      </p>
    )
  }

  return (
    <div className={styles.faixaDeNumeros}>
      <p className={styles.resumo}>
        {/* Entrou e Saiu são neutros e sem sinal: a palavra já diz a direção.
            Resultado é o único número ambíguo da barra — só ele leva tom e sinal. */}
        <span className={styles.resumoItem}>
          Entrou <MoneyText cents={entrouCents} />
        </span>
        <span className={styles.separador} aria-hidden="true">
          ·
        </span>
        <span className={styles.resumoItem}>
          Saiu <MoneyText cents={saiuCents} />
        </span>
        <span className={styles.separador} aria-hidden="true">
          ·
        </span>
        <span className={styles.resumoItem}>
          Resultado <MoneyText cents={resultadoCents} tone="semantic" sign="always" />
        </span>
      </p>
      <ResumoFora aportadoCents={aportadoCents} resgatadoCents={resgatadoCents} />
    </div>
  )
}

/** A segunda linha da faixa, subordinada, com o MOTIVO à frente dos números.
 *
 *  Aporte e resgate saíram de `Entrou`, `Saiu` e `Resultado` (spec 0006
 *  §3.5.2). Sem explicação o mês encolheria sozinho — mentira por omissão. Ela
 *  não entra como um quarto item depois de `Resultado`: ali seria o remendo que
 *  aparenta ser, e disputaria o fim da frase com o único número da faixa que
 *  leva tom e sinal.
 *
 *  Os valores são neutros, `plain`, sem sinal e sem tom: são os mesmos números
 *  de `/investimentos`, e lá eles são silenciosos. Sem link — Investimentos
 *  está no menu, a dois passos, e faixa não é lugar de atalho de navegação. */
function ResumoFora({
  aportadoCents,
  resgatadoCents,
}: {
  aportadoCents: number
  resgatadoCents: number
}) {
  // Só existe quando há o que explicar. Com um dos dois em zero, só o que
  // existe é citado — "0,00 em resgates" seria ruído sobre um mês sem resgate.
  if (aportadoCents + resgatadoCents <= 0) return null

  return (
    <p className={styles.resumoFora}>
      <span>Fora destes números:</span>
      {aportadoCents > 0 ? (
        <span className={styles.resumoItem}>
          <MoneyText cents={aportadoCents} /> em aportes
        </span>
      ) : null}
      {/* O `·` só existe entre DOIS itens da mesma linha: com um dos dois
          zerado ele seria pontuação órfã. */}
      {aportadoCents > 0 && resgatadoCents > 0 ? (
        <span className={styles.separador} aria-hidden="true">
          ·
        </span>
      ) : null}
      {resgatadoCents > 0 ? (
        <span className={styles.resumoItem}>
          <MoneyText cents={resgatadoCents} /> em resgates
        </span>
      ) : null}
    </p>
  )
}

type PropsDoAtalho = {
  linha: Transaction
  aberto: boolean
  onAlternar: () => void
}

function CelulaDeCategoria({ linha, aberto, onAlternar }: PropsDoAtalho) {
  // Transferência não tem categoria por desenho, e a etiqueta neutra diz isso
  // com uma palavra. Pintá-la gastaria --income/--expense em algo que não é
  // receita nem despesa.
  if (ehTransferencia(linha.kind)) return <Badge>Transferência</Badge>
  // A lacuna: um controle, não uma etiqueta — é aqui que a dívida se paga.
  if (podeCategorizar(linha)) {
    return <Atalho linha={linha} instancia="coluna" aberto={aberto} onAlternar={onAlternar} />
  }
  return <span>{linha.categoryName}</span>
}

function CelulaDeDescricao({ linha, aberto, onAlternar }: PropsDoAtalho) {
  const descricao = linha.description.trim()
  const classificacao = ehTransferencia(linha.kind)
    ? 'Transferência'
    : (linha.categoryName ?? 'Sem categoria')

  return (
    <>
      <span className={styles.descricao} title={descricao || undefined}>
        {descricao || <span className={styles.semDescricao}>Sem descrição</span>}
      </span>
      {/* Só aparece abaixo de 40rem, onde as colunas Conta e Categoria foram
          escondidas pelo DataTable. O dado não some — ele muda de lugar: e o
          controle também, por isso a segunda instância do botão mora aqui. */}
      <span className={styles.secundaria}>
        {linha.accountName} ·{' '}
        {podeCategorizar(linha) ? (
          <Atalho linha={linha} instancia="secundaria" aberto={aberto} onAlternar={onAlternar} />
        ) : (
          classificacao
        )}
      </span>
    </>
  )
}

function Atalho({
  linha,
  instancia,
  aberto,
  onAlternar,
}: PropsDoAtalho & { instancia: InstanciaDoAtalho }) {
  return (
    <BotaoSemCategoria linha={linha} instancia={instancia} aberto={aberto} onToggle={onAlternar} />
  )
}

function VazioComOrientacao({
  filtroSemCategoria,
  nomeDaConta,
  mes,
  onLimparCategoria,
  onLimparConta,
  onImportar,
}: {
  filtroSemCategoria: boolean
  nomeDaConta: string | undefined
  mes: string
  onLimparCategoria: () => void
  onLimparConta: () => void
  onImportar: () => void
}) {
  if (filtroSemCategoria) {
    return (
      <EmptyState
        title={`Tudo categorizado em ${mes}.`}
        description="Nenhum lançamento deste mês ficou sem categoria."
        action={
          <Button variant="primary" onClick={onLimparCategoria}>
            Mostrar todos os lançamentos
          </Button>
        }
      />
    )
  }

  if (nomeDaConta) {
    return (
      <EmptyState
        title={`Nenhum lançamento na ${nomeDaConta} em ${mes}.`}
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
      title={`Nenhum lançamento em ${mes}.`}
      description="Traga o extrato do banco — o app confere o que já existe antes de importar qualquer coisa."
      action={
        <Button variant="primary" onClick={onImportar}>
          Importar extrato
        </Button>
      }
    />
  )
}

// --------------------------------------------------------------- cálculo

type DiaDeLancamentos = {
  data: string
  linhas: Transaction[]
  /** Só receita e despesa. Transferência fica de fora — ver `rotuloDoDia`. */
  subtotalCents: number
  transferencias: number
}

/** Agrupa por data civil preservando a ordem em que o servidor mandou.
 *
 *  `Map` e não comparação com a linha anterior: se um dia aparecer em dois
 *  trechos da mesma página, as linhas caem no mesmo grupo em vez de produzir
 *  dois cabeçalhos com a mesma data e dois subtotais que não fecham. */
export function agruparPorDia(linhas: readonly Transaction[]): DiaDeLancamentos[] {
  const porDia = new Map<string, DiaDeLancamentos>()

  for (const linha of linhas) {
    let dia = porDia.get(linha.occurredOn)
    if (!dia) {
      dia = { data: linha.occurredOn, linhas: [], subtotalCents: 0, transferencias: 0 }
      porDia.set(linha.occurredOn, dia)
    }
    dia.linhas.push(linha)
    if (ehTransferencia(linha.kind)) {
      dia.transferencias += 1
      continue
    }
    dia.subtotalCents += valorComSinal(linha.kind, linha.amountCents)
  }

  return [...porDia.values()]
}

/** "segunda-feira, 31 de agosto · 1 transferência".
 *
 *  O trecho da transferência só aparece quando existe uma, e é ele que explica
 *  por que as linhas visíveis não somam o subtotal. Sem essa frase, o dia
 *  parece ter erro de conta. */
export function rotuloDoDia(dia: DiaDeLancamentos): string {
  const data = diaPorExtenso(dia.data)
  if (dia.transferencias === 0) return data
  if (dia.transferencias === 1) return `${data} · 1 transferência`
  return `${data} · ${dia.transferencias} transferências`
}

/** As linhas que a tabela mostra, já com o filtro de "sem categoria" aplicado.
 *
 *  O filtro é do CLIENTE porque o contrato da E2 só filtra por mês e conta. A
 *  regra copia a do servidor à risca — só receita e despesa contam como
 *  pendência; transferência não tem categoria por desenho e nunca poderia ser
 *  resolvida. Se as duas regras divergissem, a faixa diria "12" e a tela
 *  mostraria outro número. */
function linhasVisiveis(
  paginas: readonly TransactionList[],
  soSemCategoria: boolean,
): Transaction[] {
  const todas = paginas.flatMap((pagina) => pagina.items)
  if (!soSemCategoria) return todas
  return todas.filter((linha) => linha.categoryId === null && !ehTransferencia(linha.kind))
}

function totalDoFiltro(
  resumo: TransactionList['summary'] | undefined,
  soSemCategoria: boolean,
): number {
  if (!resumo) return 0
  return soSemCategoria ? resumo.uncategorizedCount : resumo.count
}

/** A outra perna da transferência, quando ela está entre as linhas carregadas.
 *
 *  Pode não estar — o par pode cair na próxima página, ou a outra conta pode
 *  estar fora do filtro. Por isso o diálogo sabe funcionar sem ela: o que não
 *  pode faltar é o AVISO de que as duas vão embora, e esse não depende de achar
 *  a contraparte. */
function acharContraparte(
  linhas: readonly Transaction[],
  alvo: Transaction,
): Transaction | undefined {
  if (!alvo.transferGroupId) return undefined
  return linhas.find(
    (linha) => linha.id !== alvo.id && linha.transferGroupId === alvo.transferGroupId,
  )
}
