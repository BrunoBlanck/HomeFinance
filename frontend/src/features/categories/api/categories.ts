import { queryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type {
  Category,
  CategoryKind,
  CategoryTree,
  CreateCategoryInput,
  UpdateCategoryInput,
} from '@/api/types'

export type { Category, CategoryKind, CategoryTree, CreateCategoryInput, UpdateCategoryInput }

export const categoriesQueryKey = (includeArchived: boolean) =>
  ['categories', { includeArchived }] as const

export function categoriesQueryOptions(includeArchived: boolean) {
  return queryOptions({
    queryKey: categoriesQueryKey(includeArchived),
    queryFn: ({ signal }) =>
      apiRequest<CategoryTree>(`/categories?includeArchived=${includeArchived}`, { signal }),
    retry: false,
  })
}

export function createCategory(input: CreateCategoryInput) {
  return apiRequest<Category>('/categories', { method: 'POST', body: input })
}

export function updateCategory(id: string, input: UpdateCategoryInput) {
  return apiRequest<Category>(`/categories/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: input,
  })
}

export function archiveCategory(id: string) {
  return apiRequest<Category>(`/categories/${encodeURIComponent(id)}/archive`, { method: 'POST' })
}

export function unarchiveCategory(id: string) {
  return apiRequest<Category>(`/categories/${encodeURIComponent(id)}/unarchive`, {
    method: 'POST',
  })
}

export function deleteCategory(id: string) {
  return apiRequest<void>(`/categories/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/** O nome de cada natureza, no singular — o rótulo do seletor e o miolo das
 *  frases do diálogo ("será uma despesa, como o grupo").
 *
 *  `investment` e `redemption` são **Investimento** e **Resgate**: é a natureza
 *  da CATEGORIA. O movimento correspondente chama-se *aporte* e *resgate*
 *  (léxico (a) da seção E7 de docs/DESIGN.md), e quem o nomeia é a tela de
 *  investimentos — não este seletor. */
export const ROTULO_DA_NATUREZA: Record<CategoryKind, string> = {
  expense: 'Despesa',
  income: 'Receita',
  investment: 'Investimento',
  redemption: 'Resgate',
}

/** O mesmo nome no plural — o título de cada seção da tela. */
export const ROTULO_PLURAL_DA_NATUREZA: Record<CategoryKind, string> = {
  expense: 'Despesas',
  income: 'Receitas',
  investment: 'Investimentos',
  redemption: 'Resgates',
}

/** O `subtitle` de cada painel — quatro linhas em construção paralela que
 *  definem as duas naturezas novas **por contraste** com as duas que a pessoa
 *  já conhece (docs/DESIGN.md, E7 (l)).
 *
 *  É onde a casa aprende o modelo mental inteiro da E7: aporte SAI da conta sem
 *  ser gasto, resgate ENTRA sem ser ganho. A lição mora junto de cada bloco, e
 *  não empilhada no apoio do `<h1>` — que continua falando de dois níveis,
 *  arquivar e excluir. */
export const SUBTITULO_DA_NATUREZA: Record<CategoryKind, string> = {
  expense: 'Sai da conta e é gasto.',
  income: 'Entra na conta e é ganho.',
  investment: 'Sai da conta, mas não é gasto.',
  redemption: 'Entra na conta, mas não é ganho.',
}
