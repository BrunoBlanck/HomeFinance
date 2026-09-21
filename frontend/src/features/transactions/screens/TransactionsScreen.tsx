import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import type { Transaction, TransactionList } from '@/api/types'
import {
  aplicarNaBusca,
  type MudancaDeBusca,
  type TipoDeLancamento,
  tipoValido,
  validarBusca,
} from '@/app/search'
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
import { categoriaPorId, categoriasQueryOptions } from '@/lib/categories'
import { dataCompleta, diaPorExtenso } from '@/lib/civil'
import { messageForError } from '@/lib/errors'
import { useFocoNoTitulo } from '@/lib/focus'
import { formatarDinheiro } from '@/lib/money'
import { FUSO_PADRAO, mesDaURL, nomeDoMes } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import {
  deleteTransaction,
  ehTransferencia,
  type FiltroDeLancamentos,
  POR_PAGINA,
  transactionsInfiniteQueryOptions,
  transactionsQueryKey,
  valorComSinal,
} from '../api/transactions'
import {
  BotaoDeCategoria,
  EditorDeCategoria,
  focarAtalho,
  type GravacaoConcluida,
  type InstanciaDoAtalho,
  podeEditarCategoria,
  proximaLacuna,
  proximoAtalho,
} from '../components/AtalhoDeCategoria'
import { AutoCategorizeDialog } from '../components/AutoCategorizeDialog'
import { ExcluirLancamentoDialog } from '../components/ExcluirLancamentoDialog'
import {
  fraseDaPendencia,
  fraseDoFiltroDePendencia,
  nenhumDoTipo,
  OPCOES_DE_TIPO,
  palavrasDoTipo,
  rotuloDaLinha,
  rotuloMostrarTodos,
  rotuloVerPendentes,
  tituloDoDocumento,
  tituloTudoCategorizado,
} from '../lexico'
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
 *  (spec 0005 §11): a linha se expande num editor — uma aberta por vez. Desde
 *  a §19, a célula da linha **já categorizada** é o mesmo controle no outro
 *  estado fechado, e abre o mesmo editor em modo *trocar*: a categoria se
 *  troca a qualquer momento, sem sair da lista. */
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
  const tipo = busca.tipo
  // `validarBusca` já garantiu que categoria e `semCategoria` não coexistem.
  const categoriaId = busca.categoria
  // `validarBusca` já descartou o `semCategoria` sob transferências e
  // investimentos (a combinação não tem resultado possível), então aqui ele é
  // só leitura.
  const soSemCategoria = busca.semCategoria === 1
  const palavras = palavrasDoTipo(tipo)

  // O filtro inteiro num objeto só: ele é a chave da query, e é a MESMA chave
  // que o `aoGravar` procura no cache. Duas montagens à mão divergiriam no dia
  // em que um filtro novo entrasse — e a que ficasse para trás leria o estado
  // de outra lista.
  const filtro: FiltroDeLancamentos = {
    mes,
    ...(contaId ? { contaId } : {}),
    ...(tipo ? { tipo } : {}),
    ...(categoriaId ? { categoriaId } : {}),
  }

  const contas = useQuery(contasQueryOptions(false))
  // A árvore é lida só para dar NOME ao filtro na faixa — o recorte é do
  // servidor, e a lista não espera por ela.
  const categorias = useQuery(categoriasQueryOptions(true))
  const categoriaFiltrada = categoriaPorId(categorias.data, categoriaId)
  const lista = useInfiniteQuery(transactionsInfiniteQueryOptions(filtro))

  const [alvoDaExclusao, setAlvoDaExclusao] = useState<Transaction | null>(null)
  const [erroDaExclusao, setErroDaExclusao] = useState<string | undefined>(undefined)
  const [categorizando, setCategorizando] = useState(false)
  const [anuncio, setAnuncio] = useState('')

  // A linha com o editor de categoria aberto — uma por vez. Guarda a busca em
  // que abriu: trocar mês, conta ou filtro fecha, sem efeito e sem tocar no
  // foco (o estado simplesmente deixa de valer para a busca nova).
  const chaveDaBusca = `${mes}|${contaId ?? ''}|${tipo ?? ''}|${categoriaId ?? ''}|${soSemCategoria ? 1 : 0}`
  const [editando, setEditando] = useState<{ id: string; busca: string } | null>(null)
  const editandoId = editando?.busca === chaveDaBusca ? editando.id : null

  // Depois de gravar, o foco tem um destino — mas só numa renderização que já
  // tenha o dado novo (`dataUpdatedAt`): as linhas que o reprocessamento
  // categorizou já perderam a lacuna, e focar uma que está prestes a sumir
  // jogaria o foco no <body>.
  const [focoPendente, setFocoPendente] = useState<
    (GravacaoConcluida & { aPartirDe: number }) | null
  >(null)

  // O tipo vai À FRENTE: `Despesas · Lançamentos · HomeFinance`. É o que faz o
  // estado do filtro sobreviver numa aba minimizada e num histórico.
  useEffect(() => {
    document.title = tituloDoDocumento(tipo)
  }, [tipo])

  const paginas = lista.data?.pages ?? []
  const resumo = paginas[0]?.summary
  const visiveis = linhasVisiveis(paginas, soSemCategoria)
  const nomeDoMesAtual = nomeDoMes(mes)

  const { hasNextPage, isFetchingNextPage, isPending, fetchNextPage } = lista
  const pendentes = resumo?.uncategorizedCount ?? 0

  // Com o filtro ligado, a tela pode receber uma página inteira sem nenhuma
  // linha sem categoria — o contrato da E2 filtra por mês, conta e tipo, não
  // por categoria. Buscar a próxima é o que honra a promessa do botão que ligou
  // o filtro ("Ver só esses 12"); o laço para sozinho quando `hasNextPage`
  // acaba.
  //
  // `pendentes > 0` é a GUARDA, e ela não é otimização: sem ela, uma URL colada
  // com `?semCategoria=1` num mês sem pendência varre o mês inteiro, 50 linhas
  // por requisição, para não achar nada — amplificação de requisição disparada
  // por um link. O servidor é quem diz quantas existem no filtro; se diz zero,
  // não há o que procurar na página seguinte.
  useEffect(() => {
    if (!soSemCategoria) return
    if (pendentes === 0) return
    if (visiveis.length > 0) return
    if (!hasNextPage || isFetchingNextPage || isPending) return
    void fetchNextPage()
  }, [
    soSemCategoria,
    pendentes,
    visiveis.length,
    hasNextPage,
    isFetchingNextPage,
    isPending,
    fetchNextPage,
  ])

  const { dataUpdatedAt } = lista
  useEffect(() => {
    if (!focoPendente || dataUpdatedAt < focoPendente.aPartirDe) return
    setFocoPendente(null)
    // Dois destinos, e a diferença é a pergunta que a pessoa está respondendo.
    // Quem PAGOU uma pendência está varrendo a lista: a próxima lacuna é o
    // próximo trabalho. Quem TROCOU uma categoria estava trabalhando nesta
    // linha: o foco volta para ela — e só se ela tiver saído da lista (filtro
    // `?tipo=`) vai ao vizinho da foto.
    if (focoPendente.eraLacuna) {
      const proxima = proximaLacuna(focoPendente.id, focoPendente.lacunasAntes)
      if (proxima) {
        proxima.focus()
        return
      }
    } else {
      if (focarAtalho(focoPendente.id)) return
      const vizinho = proximoAtalho(focoPendente.id, focoPendente.atalhosAntes)
      if (vizinho) {
        vizinho.focus()
        return
      }
    }
    // Sem destino à vista: o botão de carregar mais, se existir; senão o
    // título.
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
    const estado = queryClient.getQueryState(transactionsQueryKey(filtro))
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

  // `Movimento` só existe sob `tipo=investimentos`, e a derivação abaixo SÓ
  // vale ali: dentro deste recorte, `expense` é aporte e `income` é resgate,
  // porque o servidor obriga o pareamento natureza↔lado do dinheiro (spec 0006
  // §7.2). Fora do filtro a mesma conta seria falsa — uma despesa comum também
  // é `expense`.
  const colunaDeMovimento: Column<Transaction> = {
    key: 'movimento',
    header: 'Movimento',
    width: 'min',
    // Sem `hideBelow`: ela é a PORTADORA da distinção, e some no celular seria
    // deixar a coluna de valor neutra sem quem a explique.
    render: (linha) => (linha.kind === 'expense' ? 'Aporte' : 'Resgate'),
  }

  const colunaDeCategoria: Column<Transaction> = {
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
  }

  const colunas: readonly Column<Transaction>[] = [
    // Sob `investimentos` a palavra vem antes do resto: é ela que diz de que
    // lado o dinheiro andou, agora que o valor deixou de dizer.
    ...(tipo === 'investimentos' ? [colunaDeMovimento] : []),
    {
      key: 'conta',
      header: 'Conta',
      width: 'min',
      hideBelow: 'sm',
      render: (linha) => <span className={styles.conta}>{linha.accountName}</span>,
    },
    // Sob `transferencias` a coluna Categoria não existe: toda linha traria a
    // MESMA etiqueta `Transferência`, que é o ruído de escrever "Novo" 59 vezes
    // (spec 0004 §3.4). O que ela diria já está no título da tela.
    ...(tipo === 'transferencias' ? [] : [colunaDeCategoria]),
    {
      key: 'descricao',
      header: 'Descrição',
      render: (linha) => (
        <CelulaDeDescricao
          linha={linha}
          aberto={editandoId === linha.id}
          onAlternar={() => alternarEditor(linha.id)}
          // Sob transferências a classificação seria a mesma palavra em toda
          // linha, também na tela estreita: resta o nome da conta.
          mostrarClassificacao={tipo !== 'transferencias'}
        />
      ),
    },
    {
      key: 'valor',
      header: 'Valor',
      align: 'end',
      width: 'min',
      render: (linha) =>
        // Em `investimentos`, NEUTRO E SEM SINAL: a palavra `Aporte`/`Resgate`
        // está na primeira coluna, e cor e sinal só existem onde a palavra não
        // está. Pintar o aporte de vermelho ensinaria que poupar é prejuízo —
        // o erro que a E7 existe para corrigir.
        tipo === 'investimentos' ? (
          <MoneyText cents={linha.amountCents} />
        ) : (
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

  // O subtotal do dia responde "quanto este dia mudou o patrimônio da casa".
  // Transferência e investimento NÃO mudam o patrimônio: sob esses dois filtros
  // o cabeçalho fica só com a data. Nada de `0,00` repetido em doze dias — um
  // zero que nunca muda não é um total, é uma resposta falsa a uma pergunta que
  // este filtro não faz.
  const comSubtotal = tipo !== 'transferencias' && tipo !== 'investimentos'
  const grupos: readonly RowGroup<Transaction>[] = agruparPorDia(visiveis).map((dia) => ({
    key: dia.data,
    label: rotuloDoDia(dia, comSubtotal),
    ...(comSubtotal
      ? {
          trailing: (
            <>
              <span className="sr-only">Subtotal do dia </span>
              <MoneyText cents={dia.subtotalCents} tone="semantic" sign="always" />
            </>
          ),
        }
      : {}),
    rows: dia.linhas,
  }))

  const total = totalDoFiltro(resumo, soSemCategoria)
  const contaFiltrada = contaPorId(contas.data?.items ?? [], contaId)
  const procurandoNoResto =
    soSemCategoria && pendentes > 0 && visiveis.length === 0 && (hasNextPage || isFetchingNextPage)

  return (
    <div className={styles.pagina}>
      <div className={styles.cabecalho}>
        <div>
          <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
            Lançamentos
          </h1>
          {/* O <h1> não muda com o filtro — ele é o nome da rota. O estado
              vive no controle e nesta linha, que empresta a frase já ratificada
              da tela irmã de cada opção. */}
          <p className={styles.apoio}>{palavras.apoio(nomeDoMesAtual)}</p>
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
        pendentes={pendentes}
        carregando={isPending}
        filtroAtivo={soSemCategoria}
        tipo={tipo}
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
            <div className={styles.filtros}>
              <Select
                label="Conta"
                density="compact"
                placeholder="Todas as contas"
                options={opcoesDeConta(contas.data?.items ?? [])}
                value={contaId ?? ''}
                onChange={(evento) =>
                  trocarBusca({
                    conta: evento.target.value === '' ? undefined : evento.target.value,
                  })
                }
              />
              {/* O tipo vem da URL e não espera dado nenhum: o seletor fica
                  ativo inclusive durante a primeira carga. `Tudo` é o
                  placeholder — a ausência da chave —, e não uma opção a mais. */}
              <Select
                label="Tipo"
                density="compact"
                placeholder="Tudo"
                options={OPCOES_DE_TIPO}
                value={tipo ?? ''}
                onChange={(evento) => trocarBusca({ tipo: tipoValido(evento.target.value) })}
              />
              {/* O filtro de categoria não tem seletor: ele chega pelo atalho
                  do relatório. O que a faixa precisa é DIZER que ele está
                  ligado e dar a saída — um filtro invisível é uma lista que
                  parece faltar dado. O nome pode demorar um instante (a árvore
                  é outra query); até lá o rótulo genérico já mostra o estado,
                  em vez de a faixa aparecer atrasada. */}
              {categoriaId ? (
                <div className={styles.filtroDeCategoria}>
                  <span className={styles.rotuloDoFiltro}>Categoria</span>
                  <span className={styles.nomeDoFiltro}>
                    {categoriaFiltrada?.name ?? 'Categoria selecionada'}
                  </span>
                  <Button
                    size="sm"
                    variant="quiet"
                    onClick={() => trocarBusca({ categoria: undefined })}
                  >
                    Limpar
                  </Button>
                </div>
              ) : null}
            </div>
            <Resumo
              carregando={isPending}
              tipo={tipo}
              entrouCents={resumo?.incomeCents ?? 0}
              saiuCents={resumo?.expenseCents ?? 0}
              resultadoCents={resumo?.netCents ?? 0}
              aportadoCents={resumo?.investedCents ?? 0}
              resgatadoCents={resumo?.redeemedCents ?? 0}
            />
          </div>

          <DataTable
            caption={palavras.caption(nomeDoMesAtual)}
            columns={colunas}
            groups={grupos}
            rowKey={(linha) => linha.id}
            loading={isPending}
            detail={(linha) =>
              linha.id === editandoId ? (
                <EditorDeCategoria
                  linha={linha}
                  mes={mes}
                  tipo={tipo}
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
                  categoriaAtiva={categoriaId !== undefined}
                  nomeDaCategoria={categoriaFiltrada?.name}
                  tipo={tipo}
                  nomeDaConta={contaFiltrada?.name}
                  mes={nomeDoMesAtual}
                  onLimparCategoria={() => trocarBusca({ semCategoria: undefined })}
                  onLimparCategoriaFiltrada={() => trocarBusca({ categoria: undefined })}
                  onLimparTipo={() => trocarBusca({ tipo: undefined })}
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
  tipo,
  mes,
  onFiltrar,
  onLimpar,
  onCategorizar,
}: {
  pendentes: number
  carregando: boolean
  filtroAtivo: boolean
  /** O filtro de tipo ativo: a contagem é DELE, e a frase tem de nomeá-lo. */
  tipo: TipoDeLancamento | undefined
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
      {/* O diálogo é do MÊS (E2c (e)). Com um tipo ativo a frase ao lado passou
          a ser do filtro ("3 receitas"), e o rótulo antigo prometeria 3 e faria
          12 — a ação tem de dizer no próprio rótulo o que vai acontecer. */}
      {tipo ? 'Categorizar o mês automaticamente' : 'Categorizar automaticamente'}
    </Button>
  )

  if (filtroAtivo) {
    return (
      <Alert
        tone="info"
        action={
          <div className={styles.acoesDaFaixa}>
            {temPendencia ? categorizar : null}
            {/* Limpa SÓ o `semCategoria`: com um tipo ativo, prometer "todos os
                lançamentos" seria prometer o que o clique não faz. */}
            <Button size="sm" onClick={onLimpar}>
              {rotuloMostrarTodos(tipo)}
            </Button>
          </div>
        }
      >
        {fraseDoFiltroDePendencia(tipo, mes)}
      </Alert>
    )
  }

  // Carregando não renderiza: inventar contagem enquanto o número não chegou
  // seria pior do que não dizer nada. E com zero a faixa some sozinha — ela não
  // é dispensável por "X", porque dívida dispensada é dívida invisível.
  if (!temPendencia) return null

  return (
    <Alert
      tone="warning"
      action={
        <div className={styles.acoesDaFaixa}>
          {categorizar}
          {/* O botão liga o `semCategoria` PRESERVANDO o `?tipo=` — e o rótulo
              concorda com ele: "Ver só essas 3" para receitas e despesas. */}
          <Button size="sm" onClick={onFiltrar}>
            {rotuloVerPendentes(tipo, pendentes)}
          </Button>
        </div>
      }
    >
      {/* A frase antiga dizia que o lançamento sem categoria "não entra em
          nenhum relatório". Deixou de ser verdade com a E6a: ele entra, como
          "Sem categoria" — e é justamente por isso que a pendência precisa ser
          paga. O orçamento continua fora (E5). */}
      {fraseDaPendencia(tipo, pendentes, mes)}
    </Alert>
  )
}

function Resumo({
  carregando,
  tipo,
  entrouCents,
  saiuCents,
  resultadoCents,
  aportadoCents,
  resgatadoCents,
}: {
  carregando: boolean
  /** O recorte ativo. A faixa mostra o número que o filtro **sabe responder** e
   *  cala o que ele não sabe: número que nunca varia é ruído, e número repetido
   *  é pior do que número ausente. */
  tipo: TipoDeLancamento | undefined
  entrouCents: number
  saiuCents: number
  resultadoCents: number
  /** Aportes do mês. Desde a spec 0006 §3.5.2 eles NÃO entram em `Saiu` nem em
   *  `Resultado` — e é a segunda linha da faixa que diz isso. */
  aportadoCents: number
  /** Resgates do mês, fora de `Entrou` e de `Resultado` pelo mesmo motivo. */
  resgatadoCents: number
}) {
  // Transferência: uma FRASE, sem número. Não existe um "total movido"
  // honesto — cada transferência tem duas pernas na lista (somar as duas conta
  // o dinheiro duas vezes) e, com o filtro de conta ligado, só uma delas
  // aparece. Um número cujo significado muda conforme outro filtro é pior do
  // que nenhum número; a contagem já está no rodapé. Como a frase não depende
  // de dado nenhum, ela entra já na primeira renderização — sem skeleton.
  if (tipo === 'transferencias') {
    return (
      <div className={styles.faixaDeNumeros}>
        <p className={styles.resumo}>
          Transferência não é receita nem despesa — o dinheiro só mudou de conta dentro da casa.
        </p>
      </div>
    )
  }

  if (carregando) {
    // Quantos esqueletos: o mesmo número do que vai aparecer. Três placeholders
    // que viram um número só piscariam uma promessa que o filtro não cumpre.
    const quantos = tipo === undefined ? 3 : tipo === 'investimentos' ? 2 : 1
    return (
      <p className={styles.resumo}>
        <Skeleton width="4.5rem" height="1rem" />
        {quantos > 1 ? <Skeleton width="4.5rem" height="1rem" /> : null}
        {quantos > 2 ? <Skeleton width="4.5rem" height="1rem" /> : null}
      </p>
    )
  }

  // Investimentos: os dois números da E7, com os rótulos já ratificados em
  // `/investimentos`. Neutros, sem sinal e sem tom — lá eles são silenciosos, e
  // aqui são os mesmos números. Os DOIS aparecem sempre, inclusive `0,00`:
  // nesta faixa a ausência de resgate responde à pergunta que a pessoa está
  // fazendo. Sem líquido: o líquido do mês é do painel de `/investimentos`, e
  // o contrato de `/transactions` não entrega nenhum.
  if (tipo === 'investimentos') {
    return (
      <div className={styles.faixaDeNumeros}>
        <p className={styles.resumo}>
          <span className={styles.resumoItem}>
            Aportes <MoneyText cents={aportadoCents} />
          </span>
          <span className={styles.separador} aria-hidden="true">
            ·
          </span>
          <span className={styles.resumoItem}>
            Resgates <MoneyText cents={resgatadoCents} />
          </span>
        </p>
      </div>
    )
  }

  // Receitas e despesas: UM número. Sob receitas o `expenseCents` do contrato é
  // 0 e sob despesas o `incomeCents` é 0 — exibi-los seria escrever um zero que
  // nunca muda. E `Resultado` seria `Entrou` (ou `−Saiu`) outra vez, com tom e
  // sinal: o único número da faixa que carrega tom passaria a repetir o
  // vizinho. A 2ª linha SOBREVIVE, citando só o lado do dinheiro que o filtro
  // nomeia — aporte é dinheiro que saiu e não está em `Saiu`; resgate é
  // dinheiro que entrou e não está em `Entrou`.
  if (tipo === 'receitas') {
    return (
      <div className={styles.faixaDeNumeros}>
        <p className={styles.resumo}>
          <span className={styles.resumoItem}>
            Entrou <MoneyText cents={entrouCents} />
          </span>
        </p>
        <ResumoFora aportadoCents={0} resgatadoCents={resgatadoCents} />
      </div>
    )
  }

  if (tipo === 'despesas') {
    return (
      <div className={styles.faixaDeNumeros}>
        <p className={styles.resumo}>
          <span className={styles.resumoItem}>
            Saiu <MoneyText cents={saiuCents} />
          </span>
        </p>
        <ResumoFora aportadoCents={aportadoCents} resgatadoCents={0} />
      </div>
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
  if (!podeEditarCategoria(linha)) return <Badge>Transferência</Badge>
  // Nos dois estados é o MESMO controle, e não uma etiqueta: a lacuna é onde a
  // dívida se paga, e o nome é onde a categoria se troca a qualquer momento
  // (emenda §19). O <span> de antes não era alcançável pelo teclado.
  return <Atalho linha={linha} instancia="coluna" aberto={aberto} onAlternar={onAlternar} />
}

function CelulaDeDescricao({
  linha,
  aberto,
  onAlternar,
  mostrarClassificacao,
}: PropsDoAtalho & { mostrarClassificacao: boolean }) {
  const descricao = linha.description.trim()

  return (
    <>
      <span className={styles.descricao} title={descricao || undefined}>
        {descricao || <span className={styles.semDescricao}>Sem descrição</span>}
      </span>
      {/* Só aparece abaixo de 40rem, onde as colunas Conta e Categoria foram
          escondidas pelo DataTable. O dado não some — ele muda de lugar: e o
          controle também, por isso a segunda instância do botão mora aqui. */}
      <span className={styles.secundaria}>
        {mostrarClassificacao ? (
          <>
            {linha.accountName} ·{' '}
            {podeEditarCategoria(linha) ? (
              <Atalho
                linha={linha}
                instancia="secundaria"
                aberto={aberto}
                onAlternar={onAlternar}
              />
            ) : (
              // Só sobra a perna de transferência: ela não tem categoria por
              // desenho, e a palavra é o que a coluna escondida diria.
              'Transferência'
            )}
          </>
        ) : (
          linha.accountName
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
    <BotaoDeCategoria linha={linha} instancia={instancia} aberto={aberto} onToggle={onAlternar} />
  )
}

/** Os vazios, em ordem de precedência: `semCategoria` → `categoria` → `tipo` →
 *  `conta`.
 *
 *  E **cada botão diz a dimensão que limpa**. Com tipo e conta ativos são dois
 *  botões: adivinhar qual dos dois a pessoa quis desfazer é pior do que
 *  oferecer os dois. */
function VazioComOrientacao({
  filtroSemCategoria,
  categoriaAtiva,
  nomeDaCategoria,
  tipo,
  nomeDaConta,
  mes,
  onLimparCategoria,
  onLimparCategoriaFiltrada,
  onLimparTipo,
  onLimparConta,
  onImportar,
}: {
  filtroSemCategoria: boolean
  categoriaAtiva: boolean
  /** O nome, quando a árvore já chegou. O filtro pode estar ativo sem ele. */
  nomeDaCategoria: string | undefined
  tipo: TipoDeLancamento | undefined
  nomeDaConta: string | undefined
  mes: string
  onLimparCategoria: () => void
  onLimparCategoriaFiltrada: () => void
  onLimparTipo: () => void
  onLimparConta: () => void
  onImportar: () => void
}) {
  const palavras = palavrasDoTipo(tipo)

  // Vem ANTES do tipo: quem chegou pelo atalho do relatório quer desfazer a
  // categoria, que é a dimensão específica — o tipo veio de carona com ela.
  if (categoriaAtiva) {
    const onde = nomeDaCategoria ? `em ${nomeDaCategoria}` : 'na categoria escolhida'
    return (
      <EmptyState
        title={`Nenhum lançamento ${onde}${nomeDaConta ? ` na ${nomeDaConta}` : ''} em ${mes}.`}
        description="Esta combinação de filtros não tem lançamento no mês."
        action={
          <div className={styles.acoesDaFaixa}>
            <Button variant="primary" onClick={onLimparCategoriaFiltrada}>
              Mostrar todas as categorias
            </Button>
            {tipo ? (
              <Button variant="secondary" onClick={onLimparTipo}>
                Mostrar todos os tipos
              </Button>
            ) : null}
          </div>
        }
      />
    )
  }

  if (filtroSemCategoria) {
    return (
      <EmptyState
        title={tituloTudoCategorizado(tipo, mes)}
        description={`${nenhumDoTipo(tipo)} deste mês ficou sem categoria.`}
        action={
          <Button variant="primary" onClick={onLimparCategoria}>
            {rotuloMostrarTodos(tipo)}
          </Button>
        }
      />
    )
  }

  if (tipo) {
    return (
      <EmptyState
        title={`${nenhumDoTipo(tipo)}${nomeDaConta ? ` na ${nomeDaConta}` : ''} em ${mes}.`}
        description={
          nomeDaConta
            ? 'Troque o tipo, troque a conta, ou volte para a lista inteira.'
            : palavras.vazio
        }
        action={
          <div className={styles.acoesDaFaixa}>
            <Button variant="primary" onClick={onLimparTipo}>
              Mostrar todos os tipos
            </Button>
            {nomeDaConta ? (
              <Button variant="secondary" onClick={onLimparConta}>
                Mostrar todas as contas
              </Button>
            ) : null}
          </div>
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
 *  parece ter erro de conta.
 *
 *  Sem subtotal (`tipo=transferencias`), o sufixo também não existe: ele é a
 *  explicação de uma conta que não está mais na tela, e viraria rótulo repetido
 *  em todo cabeçalho. Nos outros três filtros não há linha de transferência,
 *  então o sufixo só aparece mesmo em **Tudo**. */
export function rotuloDoDia(dia: DiaDeLancamentos, comSubtotal = true): string {
  const data = diaPorExtenso(dia.data)
  if (!comSubtotal || dia.transferencias === 0) return data
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
