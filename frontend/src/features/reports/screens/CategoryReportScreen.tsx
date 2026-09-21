import { useQuery } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import type { AccountGroup, CategoryKind, CategoryReportGroup } from '@/api/types'
import {
  aplicarNaBusca,
  type MudancaDeBusca,
  NATUREZAS,
  type Natureza,
  naturezaValida,
  type TipoDeLancamento,
  validarBusca,
} from '@/app/search'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable } from '@/components/DataTable/DataTable'
import {
  DonutChart,
  dobrarParaRosca,
  type GrupoDaRosca,
  type PapelDaFatia,
} from '@/components/DonutChart/DonutChart'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { ChevronDownIcon } from '@/components/icons/ChevronDownIcon'
import { MoneyText } from '@/components/MoneyText/MoneyText'
import { Panel } from '@/components/Panel/Panel'
import { Select } from '@/components/Select/Select'
import { Skeleton } from '@/components/Skeleton/Skeleton'
import { Swatch } from '@/components/Swatch/Swatch'
import { TextLink } from '@/components/TextLink/TextLink'
import { messageForError } from '@/lib/errors'
import { useFocoNoTitulo } from '@/lib/focus'
import { formatarParticipacao } from '@/lib/format'
import { FUSO_PADRAO, mesDaURL, mesPorExtenso, nomeDoMes } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import { categoryReportQueryOptions } from '../api/categoryReport'
import styles from './CategoryReportScreen.module.css'

/** A chave do balde "Sem categoria" (`categoryId: null` no contrato). É uma
 *  palavra, e não um uuid, então não colide com categoria nenhuma. */
const CHAVE_SEM_CATEGORIA = 'sem-categoria'

/** O conjunto vazio, uma vez só: um `new Set()` por renderização faria a
 *  comparação de identidade do estado de expansão nunca bater. */
const VAZIO: ReadonlySet<string> = new Set()

/** A copy de cada natureza (docs/DESIGN.md E6a (i)). `rotulo` é a opção do
 *  seletor; o resto acompanha a escolha. */
const PALAVRAS = {
  despesas: {
    rotulo: 'Despesas',
    titulo: 'Gastos por categoria',
    apoio: (mes: string) => `Para onde foi o dinheiro em ${mes}.`,
    vazio: (mes: string) => `Nenhuma despesa em ${mes}.`,
    caption: (mes: string) => `Gastos por categoria em ${mes}`,
  },
  'despesas-credito': {
    rotulo: 'Despesas no crédito',
    titulo: 'Gastos no crédito por categoria',
    apoio: (mes: string) => `Para onde foi o dinheiro do cartão de crédito em ${mes}.`,
    vazio: (mes: string) => `Nenhuma despesa no crédito em ${mes}.`,
    caption: (mes: string) => `Gastos no crédito por categoria em ${mes}`,
  },
  'despesas-debito': {
    rotulo: 'Despesas no débito',
    titulo: 'Gastos no débito por categoria',
    apoio: (mes: string) => `Para onde foi o dinheiro que saiu direto das contas em ${mes}.`,
    vazio: (mes: string) => `Nenhuma despesa no débito em ${mes}.`,
    caption: (mes: string) => `Gastos no débito por categoria em ${mes}`,
  },
  receitas: {
    rotulo: 'Receitas',
    titulo: 'Receitas por categoria',
    apoio: (mes: string) => `De onde veio o dinheiro em ${mes}.`,
    vazio: (mes: string) => `Nenhuma receita em ${mes}.`,
    caption: (mes: string) => `Receitas por categoria em ${mes}`,
  },
} as const satisfies Record<Natureza, unknown>

/** A tradução palavra da URL → par `{kind, accountGroup}` da API (ADR-032).
 *
 *  Existe **uma vez**, aqui. "Despesas no crédito" e "Despesas no débito" NÃO
 *  são naturezas da API: são recortes de conta da MESMA natureza `expense` —
 *  cartão de crédito de um lado, todas as outras contas do outro —, e a soma
 *  dos dois é o "Despesas" de todas as contas. `undefined` no `accountGroup`
 *  é "todas as contas", e vira a AUSÊNCIA da chave na query — a URL canônica.
 *  (`accountGroup=` vazio o servidor também aceita como "todas", pela
 *  convenção de valor vazio do contrato; a tela simplesmente não o escreve.)
 *
 *  `Record` exaustivo: uma palavra nova em `Natureza` é erro de compilação
 *  aqui, não uma requisição sem recorte com o seletor afirmando o contrário. */
const FILTRO_POR_NATUREZA: Record<
  Natureza,
  { kind: CategoryKind; accountGroup: AccountGroup | undefined }
> = {
  despesas: { kind: 'expense', accountGroup: undefined },
  'despesas-credito': { kind: 'expense', accountGroup: 'credit' },
  'despesas-debito': { kind: 'expense', accountGroup: 'debit' },
  receitas: { kind: 'income', accountGroup: undefined },
}

/** A tradução natureza do relatório → `?tipo=` de `/lancamentos`, para os
 *  atalhos desta tela.
 *
 *  É a regra que **nunca deixa as duas telas discordarem**: um atalho que
 *  abrisse `/lancamentos` sem `?tipo=` cairia em "Tudo" e misturaria receita
 *  com despesa embaixo de um número que só fala de uma das duas. O `tipo` é
 *  escrito SEMPRE, inclusive para despesas — em `/lancamentos` a ausência da
 *  chave significa "Tudo", e não "despesas".
 *
 *  Os três recortes de despesa caem no MESMO `despesas`: crédito e débito não
 *  são naturezas, são recortes de conta (ADR-032), e `/lancamentos` ainda não
 *  tem esse recorte. O atalho abre então no conjunto mais próximo que existe
 *  do outro lado — todas as despesas da categoria —, e nunca num conjunto de
 *  natureza trocada, que é o erro que importa.
 *
 *  `Record` exaustivo: uma natureza nova é erro de compilação aqui, não um
 *  atalho silencioso para a lista errada. */
const TIPO_POR_NATUREZA: Record<Natureza, TipoDeLancamento> = {
  despesas: 'despesas',
  'despesas-credito': 'despesas',
  'despesas-debito': 'despesas',
  receitas: 'receitas',
}

/** As opções do seletor, na ORDEM da allowlist — montadas dela, e não escritas
 *  de novo aqui, para que uma palavra nova em `search.ts` apareça no seletor
 *  sozinha (e cobre a copy dela em `PALAVRAS` pelo `Record`). */
const OPCOES_DE_NATUREZA = NATUREZAS.map((natureza) => ({
  value: natureza,
  label: PALAVRAS[natureza].rotulo,
}))

/** Tela `/relatorios/categorias` — o primeiro gráfico do produto (ADR-021,
 *  ADR-027; docs/DESIGN.md E6a).
 *
 *  Três decisões que esta tela sustenta:
 *
 *  1. **A tabela é a fonte, o anel é o resumo.** O SVG é `aria-hidden`; os
 *     números certos, todos eles, estão na `<table>` com `caption` e `<tfoot>`
 *     logo abaixo — inclusive as categorias que o anel dobrou em "Outras".
 *  2. **O cliente não calcula dinheiro nem percentual.** Total, contagem e
 *     `shareBp` vêm do servidor (ADR-027c). A única aritmética daqui é a soma
 *     **inteira** da dobra do anel.
 *  3. **Trocar mês ou natureza não pisca.** `keepPreviousData` mantém o quadro
 *     anterior a 0,6 de opacidade com `aria-busy` enquanto o novo chega;
 *     skeleton só na primeira carga. Navegar com as setas do mês é o uso
 *     comum desta tela. */
export function CategoryReportScreen() {
  const navigate = useNavigate()
  // Foco no <h1> na entrada da rota, igual às outras telas (docs/DESIGN.md).
  const tituloRef = useFocoNoTitulo()

  // Validada aqui, e não lida crua de `location.search`: é a fronteira entre a
  // URL (que a pessoa edita) e a query da API. `natureza` fora da allowlist
  // simplesmente some, e a tela abre em despesas.
  const buscaBruta = useRouterState({ select: (estado) => estado.location.search })
  const busca = validarBusca(buscaBruta as Record<string, unknown>)

  const session = useQuery(sessionQueryOptions)
  const fuso = session.data?.household.timezone ?? FUSO_PADRAO
  const mes = mesDaURL(busca.mes, fuso)
  const natureza: Natureza = busca.natureza ?? 'despesas'
  const palavras = PALAVRAS[natureza]

  const relatorio = useQuery(categoryReportQueryOptions({ mes, ...FILTRO_POR_NATUREZA[natureza] }))

  useEffect(() => {
    document.title = `${PALAVRAS[natureza].titulo} · HomeFinance`
  }, [natureza])

  function trocarBusca(mudanca: MudancaDeBusca) {
    void navigate({
      to: '/relatorios/categorias',
      search: (anterior) => aplicarNaBusca(anterior, mudanca),
      replace: true,
    })
  }

  const nomeCurtoDoMes = nomeDoMes(mes)
  const mesCompleto = mesPorExtenso(mes)
  const dados = relatorio.data
  const itens = dados?.items ?? []
  const { isPending } = relatorio
  // Refetch com dado na tela: o quadro fica, apagado e marcado como ocupado.
  const atualizando = relatorio.isFetching && !isPending

  const { fatias, papelPorChave } = dobrarParaRosca(itens.map(paraFatia))
  // A tabela nasce MINIMIZADA: só os grupos. Abrir tudo de uma vez faz da
  // lista de categorias uma lista de lançamentos — a pergunta desta tela é
  // "para onde foi o dinheiro", e ela se responde no nível do grupo; o
  // detalhe é a segunda pergunta, e quem a faz clica.
  //
  // O estado guarda a BUSCA em que abriu (mesmo padrão do editor de
  // `/lancamentos`): trocar de mês ou de natureza recolhe tudo sozinho, sem
  // efeito e sem guardar chave de um quadro que não está mais na tela.
  const chaveDaBusca = `${mes}|${natureza}`
  const [expansao, setExpansao] = useState<{ busca: string; abertos: ReadonlySet<string> }>({
    busca: chaveDaBusca,
    abertos: new Set(),
  })
  const abertos = expansao.busca === chaveDaBusca ? expansao.abertos : VAZIO

  function alternarGrupo(chave: string) {
    setExpansao((anterior) => {
      const base = anterior.busca === chaveDaBusca ? anterior.abertos : VAZIO
      const proximos = new Set(base)
      if (!proximos.delete(chave)) proximos.add(chave)
      return { busca: chaveDaBusca, abertos: proximos }
    })
  }

  // As filhas só entram na tabela quando o grupo está aberto. O `<tfoot>` e a
  // rosca NÃO mudam: eles somam os grupos, e minimizar é escolha de leitura,
  // não recorte de dado.
  const linhas = itens
    .flatMap((grupo) => linhasDoGrupo(grupo, papelPorChave))
    .filter((linha) => linha.tipo === 'grupo' || abertos.has(linha.chaveDoGrupo))

  // Vazio é o mês SEM NADA. Um mês só com "Sem categoria" não é vazio — é o
  // mês que mais precisa do link de categorizar.
  const vazio = !isPending && dados !== undefined && dados.totalCents === 0 && itens.length === 0

  const colunas: readonly Column<LinhaDoRelatorio>[] = [
    {
      key: 'categoria',
      header: 'Categoria',
      render: (linha) => (
        <CelulaDeCategoria
          linha={linha}
          mes={mes}
          nomeDoMes={nomeCurtoDoMes}
          tipo={TIPO_POR_NATUREZA[natureza]}
          aberto={abertos.has(linha.chaveDoGrupo)}
          onAlternar={() => alternarGrupo(linha.chaveDoGrupo)}
        />
      ),
    },
    {
      key: 'lancamentos',
      header: 'Lançamentos',
      align: 'end',
      width: 'min',
      // Abaixo de 40rem a coluna some e a contagem reaparece sob o nome:
      // esconder coluna só é honesto quando o dado reaparece.
      hideBelow: 'sm',
      render: (linha) => <span className={styles.contagem}>{linha.count}</span>,
    },
    {
      key: 'participacao',
      header: 'Participação',
      align: 'end',
      width: 'min',
      // Duas casas: com `shareBp` exato do servidor, as linhas fecham em
      // 100,00% e as subcategorias somam o grupo, sem nota de arredondamento.
      render: (linha) => (
        <span className={styles.participacao}>{formatarParticipacao(linha.shareBp, 2)}</span>
      ),
    },
    {
      key: 'valor',
      header: 'Valor',
      align: 'end',
      width: 'min',
      // Neutro e **plain**: o cabeçalho já diz que é dinheiro, e "R$" em toda
      // linha desalinharia os dígitos.
      render: (linha) => <MoneyText cents={linha.cents} />,
    },
  ]

  return (
    <div className={styles.pagina}>
      <div className={styles.cabecalho}>
        <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
          {palavras.titulo}
        </h1>
        <p className={styles.apoio}>{palavras.apoio(nomeCurtoDoMes)}</p>
      </div>

      {relatorio.isError ? (
        <Alert
          tone="error"
          title="Não foi possível carregar o relatório."
          action={
            <Button onClick={() => void relatorio.refetch()} loading={relatorio.isFetching}>
              Tentar de novo
            </Button>
          }
        >
          {messageForError(relatorio.error)}
        </Alert>
      ) : (
        <Panel padding="none">
          <div className={styles.faixa}>
            {/* A natureza vem da URL e não espera dado nenhum: o seletor fica
                ativo inclusive durante a primeira carga. O rótulo continua
                "Natureza" porque é como a pessoa o chama — mesmo que duas das
                quatro opções sejam recortes de conta, e não naturezas. */}
            <Select
              label="Natureza"
              density="compact"
              options={OPCOES_DE_NATUREZA}
              value={natureza}
              onChange={(evento) => {
                // A MESMA allowlist do portão da URL, não um `includes` local:
                // o que passa aqui é o que a próxima leitura aceita.
                const escolhida = naturezaValida(evento.target.value)
                trocarBusca({
                  // Despesas é o padrão: a URL canônica não escreve a chave.
                  natureza: escolhida === 'despesas' ? undefined : escolhida,
                })
              }}
            />
            {isPending ? (
              <Skeleton width="11rem" height="1rem" />
            ) : vazio || dados === undefined ? null : (
              <p className={styles.resumo}>
                <MoneyText cents={dados.totalCents} format="currency" />
                <span>
                  {' em '}
                  {dados.count === 1 ? '1 lançamento' : `${dados.count} lançamentos`}
                </span>
              </p>
            )}
          </div>

          {vazio ? (
            <EmptyState
              title={palavras.vazio(mesCompleto)}
              description="O relatório aparece assim que houver lançamentos no mês — registre um em Lançamentos ou importe o extrato."
              action={
                <Button variant="primary" onClick={() => void navigate({ to: '/importar' })}>
                  Importar extrato
                </Button>
              }
            />
          ) : (
            <div
              className={styles.quadro}
              data-atualizando={atualizando || undefined}
              aria-busy={atualizando || undefined}
            >
              <DonutChart
                fatias={fatias}
                totalCents={dados?.totalCents ?? 0}
                rotuloDoCentro={nomeCurtoDoMes}
                legenda="Distribuição por categoria — os valores estão na tabela abaixo."
                loading={isPending}
              />
              <DataTable
                caption={palavras.caption(mesCompleto)}
                columns={colunas}
                rows={linhas}
                rowKey={(linha) => linha.chave}
                loading={isPending}
                footer={
                  <tr>
                    <th scope="row">Total</th>
                    {/* O rodapé é ReactNode cru: ele não herda o `hideBelow` da
                        coluna, então a célula esconde a si mesma. */}
                    <td data-align="end" data-hide="sm">
                      <span className={styles.contagem}>{dados?.count ?? 0}</span>
                    </td>
                    <td data-align="end">
                      <span className={styles.participacao}>
                        {formatarParticipacao(dados && dados.totalCents > 0 ? 10_000 : 0, 2)}
                      </span>
                    </td>
                    <td data-align="end">
                      <MoneyText cents={dados?.totalCents ?? 0} emphasis="total" />
                    </td>
                  </tr>
                }
              />
            </div>
          )}
        </Panel>
      )}
    </div>
  )
}

// -------------------------------------------------------------- pedaços

function CelulaDeCategoria({
  linha,
  mes,
  nomeDoMes,
  tipo,
  aberto,
  onAlternar,
}: {
  linha: LinhaDoRelatorio
  mes: string
  nomeDoMes: string
  /** O `?tipo=` que os atalhos escrevem — ver `TIPO_POR_NATUREZA`. */
  tipo: TipoDeLancamento
  aberto: boolean
  onAlternar: () => void
}) {
  const contagem = (
    // Só aparece abaixo de 40rem, onde a coluna Lançamentos foi escondida.
    <span className={styles.secundaria}>
      {linha.count === 1 ? '1 lançamento' : `${linha.count} lançamentos`}
    </span>
  )

  const nome = (
    <>
      {linha.nome}
      {linha.arquivada ? <span className={styles.arquivada}> (arquivada)</span> : null}
    </>
  )

  if (linha.tipo === 'grupo') {
    return (
      <>
        <span className={styles.nomeDoGrupo}>
          {/* Amostra e nome são um bloco só: numa coluna estreita (375 px) a
              quebra tem de cair DEPOIS do nome, nunca entre a amostra e ele —
              uma amostra sozinha numa linha não identifica coisa nenhuma.
              A amostra liga a linha à fatia, e as categorias dobradas levam a
              hachura de "Outras": o mapa "estes, juntos, são aquela fatia". */}
          <span className={styles.rotuloDoGrupo}>
            {linha.papel ? <Swatch papel={linha.papel} /> : null}
            {linha.temFilhas ? (
              // `<button aria-expanded>` de verdade, e não uma linha
              // clicável: é o padrão de disclosure do APG, e é o que faz o
              // leitor de tela anunciar "recolhido/expandido" e o teclado
              // alcançar a abertura sem mouse.
              <button
                type="button"
                className={styles.abrirGrupo}
                aria-expanded={aberto}
                onClick={onAlternar}
              >
                <span className={styles.seta} data-aberto={aberto || undefined} aria-hidden="true">
                  <ChevronDownIcon size={14} />
                </span>
                <span className={styles.grupo}>{nome}</span>
              </button>
            ) : (
              <span className={styles.grupo}>{nome}</span>
            )}
          </span>
          <span className={styles.acaoDoGrupo}>
            <span className={styles.separador} aria-hidden="true">
              ·
            </span>
            {linha.semCategoria ? (
              // O atalho do balde de pendência leva o `?tipo=` junto (ponto 2):
              // sem ele, "Categorizar" abriria a lista inteira do mês e a
              // pessoa veria receita no meio das despesas que estava olhando.
              <TextLink
                to="/lancamentos"
                search={{ mes, tipo, semCategoria: 1 }}
                aria-label={`Categorizar os lançamentos sem categoria de ${nomeDoMes}`}
              >
                Categorizar
              </TextLink>
            ) : (
              <AtalhoParaLancamentos linha={linha} mes={mes} nomeDoMes={nomeDoMes} tipo={tipo} />
            )}
          </span>
        </span>
        {contagem}
      </>
    )
  }

  return (
    <div className={styles.filha}>
      {/* O nome do grupo viaja junto para quem lê linha a linha: sem ele,
          "Mercado" sozinho não diz de que grupo é. */}
      <span className="sr-only">em {linha.grupo}: </span>
      <span className={linha.tipo === 'direta' ? styles.semSubcategoria : undefined}>{nome}</span>
      {/* A linha "Sem subcategoria" não ganha atalho: ela é o que sobrou do
          grupo depois das filhas, e `?categoria=` do grupo traria as filhas
          junto — um atalho que abre num conjunto MAIOR do que o número em que
          se clicou é pior do que atalho nenhum. */}
      {linha.tipo === 'filha' ? (
        <span className={styles.acaoDaFilha}>
          <span className={styles.separador} aria-hidden="true">
            ·
          </span>
          <AtalhoParaLancamentos linha={linha} mes={mes} nomeDoMes={nomeDoMes} tipo={tipo} />
        </span>
      ) : null}
      {contagem}
    </div>
  )
}

/** "Ver lançamentos" — o atalho desta tela para `/lancamentos`, já filtrado.
 *
 *  Leva as TRÊS dimensões do que está na tela: o mês, o tipo (para nunca
 *  misturar receita com despesa) e a categoria. Grupo traz as subcategorias
 *  junto, e quem expande é o servidor — é o mesmo conjunto que a linha soma
 *  aqui, então o total lá bate com o número em que a pessoa clicou.
 *
 *  É link, e não botão: navegar é trabalho de link, e assim "abrir em nova
 *  aba" continua funcionando. */
function AtalhoParaLancamentos({
  linha,
  mes,
  nomeDoMes,
  tipo,
}: {
  linha: LinhaDoRelatorio
  mes: string
  nomeDoMes: string
  tipo: TipoDeLancamento
}) {
  if (linha.categoryId === null) return null
  return (
    <TextLink
      to="/lancamentos"
      search={{ mes, tipo, categoria: linha.categoryId }}
      aria-label={`Ver os lançamentos de ${linha.nome} em ${nomeDoMes}`}
    >
      Ver lançamentos
    </TextLink>
  )
}

// --------------------------------------------------------------- dados

/** Uma linha PLANA da tabela: grupo, subcategoria ou o "Sem subcategoria" do
 *  grupo. A hierarquia é de dois níveis e a indentação já a mostra — um
 *  `<tbody>` por grupo repetiria o nome como cabeçalho e depois como linha. */
export type LinhaDoRelatorio = {
  chave: string
  tipo: 'grupo' | 'filha' | 'direta'
  nome: string
  /** O nome do grupo, para o prefixo `sr-only` das linhas recuadas. */
  grupo: string
  /** A chave do GRUPO a que a linha pertence — a própria, no caso do grupo.
   *
   *  É por ela que a tabela decide o que aparece com o grupo aberto. Chave, e
   *  não nome: dois grupos podem se chamar igual depois de um renomear, e o
   *  estado de expansão não pode confundi-los. */
  chaveDoGrupo: string
  /** O id da categoria desta linha, ou `null` quando ela não tem uma para
   *  filtrar: o balde "Sem categoria" e a linha "Sem subcategoria". É o que
   *  o atalho "Ver lançamentos" manda para `?categoria=`. */
  categoryId: string | null
  /** Só no grupo: ele tem linhas embaixo para abrir? Grupo folha não vira
   *  disclosure — um botão que não abre nada é um botão que mente. */
  temFilhas?: boolean | undefined
  arquivada: boolean
  cents: number
  count: number
  shareBp: number
  papel?: PapelDaFatia | undefined
  semCategoria?: boolean | undefined
}

function paraFatia(grupo: CategoryReportGroup): GrupoDaRosca {
  return {
    key: chaveDoGrupo(grupo),
    label: nomeDoGrupo(grupo),
    cents: grupo.totalCents,
    shareBp: grupo.shareBp,
    ...(grupo.categoryId === null ? { pendente: true } : {}),
  }
}

function chaveDoGrupo(grupo: CategoryReportGroup): string {
  return grupo.categoryId ?? CHAVE_SEM_CATEGORIA
}

function nomeDoGrupo(grupo: CategoryReportGroup): string {
  return grupo.name ?? 'Sem categoria'
}

/** As linhas de um grupo, na ordem em que aparecem na tabela. */
export function linhasDoGrupo(
  grupo: CategoryReportGroup,
  papelPorChave: ReadonlyMap<string, PapelDaFatia>,
): LinhaDoRelatorio[] {
  const chave = chaveDoGrupo(grupo)
  const nome = nomeDoGrupo(grupo)
  const papel = papelPorChave.get(chave)

  // O grupo só é "abrível" quando tem linha embaixo: as filhas, mais a linha
  // "Sem subcategoria" quando ela existe. É a MESMA conjunção que decide as
  // linhas logo abaixo — uma segunda regra aqui divergiria no dia em que a de
  // baixo mudasse.
  const temDireta = grupo.children.length > 0 && grupo.directCount > 0
  const temFilhas = grupo.children.length > 0 || temDireta

  const linhas: LinhaDoRelatorio[] = [
    {
      chave: `g:${chave}`,
      tipo: 'grupo',
      nome,
      grupo: nome,
      chaveDoGrupo: chave,
      categoryId: grupo.categoryId,
      ...(temFilhas ? { temFilhas: true } : {}),
      arquivada: grupo.archivedAt !== null,
      cents: grupo.totalCents,
      count: grupo.count,
      shareBp: grupo.shareBp,
      ...(papel ? { papel } : {}),
      ...(grupo.categoryId === null ? { semCategoria: true } : {}),
    },
  ]

  for (const filha of grupo.children) {
    linhas.push({
      chave: `f:${filha.categoryId}`,
      tipo: 'filha',
      nome: filha.name,
      grupo: nome,
      chaveDoGrupo: chave,
      categoryId: filha.categoryId,
      arquivada: filha.archivedAt !== null,
      cents: filha.totalCents,
      count: filha.count,
      shareBp: filha.shareBp,
    })
  }

  // "Sem subcategoria" só quando o grupo TEM filhas e há lançamento direto
  // nele: num grupo folha a linha repetiria o próprio grupo.
  if (temDireta) {
    linhas.push({
      chave: `d:${chave}`,
      tipo: 'direta',
      nome: 'Sem subcategoria',
      grupo: nome,
      chaveDoGrupo: chave,
      categoryId: null,
      arquivada: false,
      cents: grupo.directCents,
      count: grupo.directCount,
      shareBp: grupo.directShareBp,
    })
  }

  return linhas
}
