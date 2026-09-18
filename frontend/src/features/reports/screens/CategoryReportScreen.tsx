import { useQuery } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect } from 'react'
import type { CategoryKind, CategoryReportGroup } from '@/api/types'
import { aplicarNaBusca, type MudancaDeBusca, type Natureza, validarBusca } from '@/app/search'
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

const PALAVRAS = {
  despesas: {
    titulo: 'Gastos por categoria',
    apoio: (mes: string) => `Para onde foi o dinheiro em ${mes}.`,
    vazio: (mes: string) => `Nenhuma despesa em ${mes}.`,
    caption: (mes: string) => `Gastos por categoria em ${mes}`,
  },
  receitas: {
    titulo: 'Receitas por categoria',
    apoio: (mes: string) => `De onde veio o dinheiro em ${mes}.`,
    vazio: (mes: string) => `Nenhuma receita em ${mes}.`,
    caption: (mes: string) => `Receitas por categoria em ${mes}`,
  },
} as const satisfies Record<Natureza, unknown>

const KIND_POR_NATUREZA: Record<Natureza, CategoryKind> = {
  despesas: 'expense',
  receitas: 'income',
}

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

  const relatorio = useQuery(categoryReportQueryOptions({ mes, kind: KIND_POR_NATUREZA[natureza] }))

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
  const linhas = itens.flatMap((grupo) => linhasDoGrupo(grupo, papelPorChave))

  // Vazio é o mês SEM NADA. Um mês só com "Sem categoria" não é vazio — é o
  // mês que mais precisa do link de categorizar.
  const vazio = !isPending && dados !== undefined && dados.totalCents === 0 && itens.length === 0

  const colunas: readonly Column<LinhaDoRelatorio>[] = [
    {
      key: 'categoria',
      header: 'Categoria',
      render: (linha) => <CelulaDeCategoria linha={linha} mes={mes} nomeDoMes={nomeCurtoDoMes} />,
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
                ativo inclusive durante a primeira carga. */}
            <Select
              label="Natureza"
              density="compact"
              options={[
                { value: 'despesas', label: 'Despesas' },
                { value: 'receitas', label: 'Receitas' },
              ]}
              value={natureza}
              onChange={(evento) =>
                trocarBusca({
                  // Despesas é o padrão: a URL canônica não escreve a chave.
                  natureza: evento.target.value === 'receitas' ? 'receitas' : undefined,
                })
              }
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
}: {
  linha: LinhaDoRelatorio
  mes: string
  nomeDoMes: string
}) {
  const contagem = (
    // Só aparece abaixo de 40rem, onde a coluna Lançamentos foi escondida.
    <span className={styles.secundaria}>
      {linha.count === 1 ? '1 lançamento' : `${linha.count} lançamentos`}
    </span>
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
            <span className={styles.grupo}>
              {linha.nome}
              {linha.arquivada ? <span className={styles.arquivada}> (arquivada)</span> : null}
            </span>
          </span>
          {linha.semCategoria ? (
            <span className={styles.acaoDoGrupo}>
              <span className={styles.separador} aria-hidden="true">
                ·
              </span>
              <TextLink
                to="/lancamentos"
                search={{ mes, semCategoria: 1 }}
                aria-label={`Categorizar os lançamentos sem categoria de ${nomeDoMes}`}
              >
                Categorizar
              </TextLink>
            </span>
          ) : null}
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
      <span className={linha.tipo === 'direta' ? styles.semSubcategoria : undefined}>
        {linha.nome}
        {linha.arquivada ? <span className={styles.arquivada}> (arquivada)</span> : null}
      </span>
      {contagem}
    </div>
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

  const linhas: LinhaDoRelatorio[] = [
    {
      chave: `g:${chave}`,
      tipo: 'grupo',
      nome,
      grupo: nome,
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
      arquivada: filha.archivedAt !== null,
      cents: filha.totalCents,
      count: filha.count,
      shareBp: filha.shareBp,
    })
  }

  // "Sem subcategoria" só quando o grupo TEM filhas e há lançamento direto
  // nele: num grupo folha a linha repetiria o próprio grupo.
  if (grupo.children.length > 0 && grupo.directCount > 0) {
    linhas.push({
      chave: `d:${chave}`,
      tipo: 'direta',
      nome: 'Sem subcategoria',
      grupo: nome,
      arquivada: false,
      cents: grupo.directCents,
      count: grupo.directCount,
      shareBp: grupo.directShareBp,
    })
  }

  return linhas
}
