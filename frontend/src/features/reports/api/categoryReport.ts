import { keepPreviousData, queryOptions } from '@tanstack/react-query'
import { apiRequest, EchoMismatchError } from '@/api/client'
import type {
  AccountGroup,
  CategoryKind,
  CategoryReport,
  CategoryReportChild,
  CategoryReportGroup,
} from '@/api/types'

/** Relatório por categoria (ADR-027). Nenhum tipo de payload é escrito aqui:
 *  todos derivam do contrato OpenAPI (ADR-015). */

export type { AccountGroup, CategoryReport, CategoryReportChild, CategoryReportGroup }

export type FiltroDoRelatorio = {
  /** `AAAA-MM` — competência, como no resto do app (ADR-023c). */
  mes: string
  /** `expense` (padrão) ou `income`. Traduzido da `?natureza=` da URL. */
  kind: CategoryKind
  /** Recorte de contas (ADR-032): `credit`, `debit` ou `undefined` = todas.
   *
   *  Chave OBRIGATÓRIA, valor opcional — de propósito. Um `accountGroup?`
   *  deixaria a tela esquecer de traduzi-lo e cair em "todas as contas" sem
   *  erro de compilação, com o seletor afirmando "Despesas no crédito". Aqui
   *  quem monta o filtro tem de escrever o `undefined` de caso pensado. */
  accountGroup: AccountGroup | undefined
}

/** A chave mora sob o prefixo `['transactions', …]` **de propósito** (ADR-027).
 *
 *  Toda mutação que mexe em lançamento já invalida `['transactions']` — a
 *  importação, o atalho de categoria, a categorização automática, o
 *  reprocessamento de transferências e a exclusão. Pendurando o relatório
 *  ali, ele se atualiza junto, sem que nenhuma dessas features precise saber
 *  que este relatório existe. Import entre features é proibido (AGENTS.md), e
 *  a alternativa — um prefixo próprio invalidado em seis lugares — viraria
 *  dívida no primeiro esquecimento.
 *
 *  `accountGroup` entra na chave SEMPRE, inclusive como `undefined`: o recorte
 *  é do servidor, e sem ele na chave trocar "Despesas" por "Despesas no
 *  crédito" serviria o quadro de todas as contas a partir do cache. O
 *  `undefined` é estável no hash da TanStack Query e distingue as quatro
 *  opções da tela sem colisão — `{expense, undefined}`, `{expense, credit}`,
 *  `{expense, debit}` e `{income, undefined}` são quatro chaves. */
export const categoryReportQueryKey = (filtro: FiltroDoRelatorio) =>
  [
    'transactions',
    'reports',
    'by-category',
    { mes: filtro.mes, kind: filtro.kind, accountGroup: filtro.accountGroup },
  ] as const

/** A query da tela `/relatorios/categorias`.
 *
 *  `keepPreviousData` é o que faz trocar de mês ou de natureza **não** piscar:
 *  com dado na tela, o quadro anterior fica visível (a 0,6 de opacidade e com
 *  `aria-busy`) enquanto o novo chega. Skeleton só na primeira carga
 *  (`isPending`) — é o anti-padrão "skeleton flash on refetch", e esta tela é
 *  justamente a que se navega com as setas do mês.
 *
 *  `retry: false` como no resto do app: 400, 401 e um eco divergente não
 *  melhoram na segunda tentativa, e o erro tem uma ação própria na tela. Vale
 *  para o erro lançado AQUI dentro tanto quanto para o que veio do HTTP — a
 *  TanStack Query não distingue os dois.
 *
 *  Os dois juntos têm uma consequência que a tela sustenta: com
 *  `keepPreviousData`, o `data` do fetch anterior continua vivo durante o erro,
 *  e é `isError` ser a PRIMEIRA condição do render que faz o quadro sair
 *  inteiro (`CategoryReportScreen.tsx`). */
export function categoryReportQueryOptions(filtro: FiltroDoRelatorio) {
  return queryOptions({
    queryKey: categoryReportQueryKey(filtro),
    queryFn: async ({ signal }) => {
      // `URLSearchParams`, nunca concatenação: mês e natureza vêm da URL, que
      // a pessoa edita. O `search.ts` já os validou por allowlist — isto é a
      // segunda camada, e ela custa nada.
      const busca = new URLSearchParams({ month: filtro.mes, kind: filtro.kind })
      // Ausente = todas as contas, e é a URL canônica: `set` (nunca `append`,
      // que faria a chave repetida — essa, sim, é 400), e só quando há recorte.
      // `accountGroup=` vazio não é erro no servidor (vale o mesmo que a
      // ausência, pela convenção de valor vazio do contrato), mas escrever a
      // chave vazia diria "recorte" a quem lê a URL e "sem recorte" ao
      // servidor — é justamente a ambiguidade que não se produz de propósito.
      if (filtro.accountGroup !== undefined) busca.set('accountGroup', filtro.accountGroup)
      const dados = await apiRequest<CategoryReport>(`/reports/by-category?${busca.toString()}`, {
        signal,
      })
      // O eco é CONFERIDO, não presumido: o contrato põe `month`, `kind` e
      // `accountGroup` em `required` na resposta justamente para a tela não ter
      // de adivinhar sob que rótulo está o número — e os três estão no `<h1>` e
      // no subtítulo desta tela. Divergiu, é erro da query: o quadro não é
      // publicado sob um rótulo que não é dele (o defeito que o ADR-030 nomeia).
      // Uma conferência só, com os três campos — o critério de QUANDO se
      // confere está no doc-comment do `EchoMismatchError`.
      if (
        dados.month !== filtro.mes ||
        dados.kind !== filtro.kind ||
        // `null` na resposta é "todas as contas"; no filtro, "todas" é
        // `undefined` (a ausência da chave na query). São o mesmo recorte
        // escrito em dois lugares — comparar cru acusaria divergência sempre.
        (dados.accountGroup ?? undefined) !== filtro.accountGroup
      ) {
        throw new EchoMismatchError()
      }
      return dados
    },
    placeholderData: keepPreviousData,
    retry: false,
  })
}
