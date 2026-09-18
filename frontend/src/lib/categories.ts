import { queryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type { Category, CategoryKind, CategoryTree, UpdateCategoryInput } from '@/api/types'
import type { SelectOption } from '@/components/Select/Select'

/** Leitura da árvore de categorias, compartilhada entre telas.
 *
 *  Mesmo motivo de `lib/accounts.ts`: a revisão da importação oferece categoria
 *  por linha e não pode importar de `features/categories/`. Criar, renomear e
 *  arquivar categoria continuam lá.
 *
 *  **Editar** desce para cá pelo mesmo motivo que `updateAccount` desceu: a
 *  revisão da importação ensina palavras-chave à categoria ("da próxima vez,
 *  reconhecer por «mercado»"), e isso é um `PATCH /categories/{id}` disparado
 *  de dentro de `features/import/`. A tela de categorias continua com a sua
 *  cópia em `features/categories/api/` — a chamada é a mesma linha de código,
 *  e o contrato é um só. */

export type { Category, CategoryKind, CategoryTree, UpdateCategoryInput }

export const categoriasQueryKey = (incluirArquivadas: boolean) =>
  ['categories', { includeArchived: incluirArquivadas }] as const

export function categoriasQueryOptions(incluirArquivadas = false) {
  return queryOptions({
    queryKey: categoriasQueryKey(incluirArquivadas),
    queryFn: ({ signal }) =>
      apiRequest<CategoryTree>(`/categories?includeArchived=${incluirArquivadas}`, { signal }),
    retry: false,
  })
}

/** Edita uma categoria. `keywords` presente **substitui** a lista inteira —
 *  quem acrescenta uma palavra lê a lista atual antes e manda tudo de volta. */
export function updateCategory(id: string, input: UpdateCategoryInput) {
  return apiRequest<Category>(`/categories/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: input,
  })
}

/** O LADO DO DINHEIRO de um lançamento: `expense` é o que SAI da conta,
 *  `income` é o que ENTRA. É `TransactionKind` menos as duas pernas de
 *  transferência, que nunca têm categoria (ADR-016). */
export type LadoDoDinheiro = 'income' | 'expense'

/** As naturezas de categoria que cada lado do dinheiro aceita (ADR-029b).
 *
 *  Um aporte é dinheiro que **sai** da conta e um resgate é dinheiro que
 *  **entra** — por isso despesa aceita `expense` **ou** `investment`, e receita
 *  aceita `income` **ou** `redemption`. A ordem é a da tela: o que se mexe todo
 *  dia primeiro.
 *
 *  Esta tabela é a ÚNICA cópia da regra no frontend, pelo mesmo motivo que o
 *  backend a concentrou no pacote `category`: duas cópias de uma allowlist
 *  divergem, e a segunda é sempre a esquecida. */
export const NATUREZAS_DO_LADO: Record<LadoDoDinheiro, readonly CategoryKind[]> = {
  expense: ['expense', 'investment'],
  income: ['income', 'redemption'],
}

/** O lado do dinheiro de uma natureza de categoria — derivado da tabela
 *  acima, para que a regra continue tendo uma cópia só. */
export function ladoDaNatureza(natureza: CategoryKind): LadoDoDinheiro {
  return NATUREZAS_DO_LADO.income.includes(natureza) ? 'income' : 'expense'
}

/** `true` quando as duas naturezas ficam do MESMO lado do dinheiro.
 *
 *  É a fronteira do ADR-029c: dentro do mesmo lado, trocar a natureza é
 *  permitido **mesmo com a categoria em uso** (nenhum lançamento muda de sinal,
 *  de conta ou de saldo — a troca é de rótulo de intenção). Cruzar o lado
 *  transformaria despesa registrada em receita, e o servidor recusa. */
export function mesmoLadoDoDinheiro(a: CategoryKind, b: CategoryKind): boolean {
  return ladoDaNatureza(a) === ladoDaNatureza(b)
}

/** As quatro naturezas, na ordem em que a tela de categorias as desenha. */
export const NATUREZAS: readonly CategoryKind[] = ['expense', 'income', 'investment', 'redemption']

/** Os grupos de uma natureza.
 *
 *  Tolera o array ausente de propósito: `CategoryTree` ganhou `investment` e
 *  `redemption` na E7, e uma árvore em cache — ou um servidor mais velho que o
 *  app — pode não trazê-los. Quebrar a tela inteira por causa disso seria
 *  trocar uma natureza faltando por nenhuma categoria. */
function gruposDe(arvore: CategoryTree | undefined, natureza: CategoryKind): readonly Category[] {
  return arvore?.[natureza] ?? []
}

/** Opções do seletor de categoria de um LADO DO DINHEIRO, em `optgroup` por
 *  grupo.
 *
 *  Recebe o lado, e não a natureza, porque o pareamento é por lado (ADR-029b):
 *  uma despesa escolhe entre as categorias de despesa **e** as de investimento,
 *  e nunca vê uma de receita ou de resgate. Sem isto a pessoa veria a sugestão
 *  de investimento na revisão da importação e não conseguiria escolhê-la à mão.
 *
 *  Filtrar pelo lado continua sendo o ponto: oferecer "Salário" para uma
 *  despesa é oferecer um erro. A árvore tem dois níveis (ADR-017b) e já chega
 *  ordenada do servidor — reordenar aqui inventaria uma ordem própria.
 *
 *  Grupo sem subcategoria vira opção ele mesmo: numa casa que só criou grupos,
 *  um seletor vazio seria inexplicável. */
export function opcoesDeCategoria(
  arvore: CategoryTree | undefined,
  lado: LadoDoDinheiro,
): SelectOption[] {
  const opcoes: SelectOption[] = []

  for (const natureza of NATUREZAS_DO_LADO[lado]) {
    for (const grupo of gruposDe(arvore, natureza)) {
      if (grupo.children.length === 0) {
        opcoes.push({ value: grupo.id, label: grupo.name })
        continue
      }
      for (const filha of grupo.children) {
        opcoes.push({ value: filha.id, label: filha.name, group: grupo.name })
      }
    }
  }
  return opcoes
}

/** A categoria pelo id, em qualquer nível e em qualquer uma das QUATRO
 *  naturezas — para a tela escrever o NOME (e ler as palavras-chave) em vez de
 *  repetir o id.
 *
 *  Varre as quatro: uma categoria de investimento vinda de sugestão da
 *  importação tem de ser encontrada aqui, senão a linha secundária do celular
 *  diria "Categoria:" sem nome nenhum. */
export function categoriaPorId(
  arvore: CategoryTree | undefined,
  id: string | null | undefined,
): Category | undefined {
  if (!arvore || !id) return undefined
  for (const natureza of NATUREZAS) {
    for (const grupo of gruposDe(arvore, natureza)) {
      if (grupo.id === id) return grupo
      const filha = grupo.children.find((categoria) => categoria.id === id)
      if (filha) return filha
    }
  }
  return undefined
}
