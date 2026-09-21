import { queryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type {
  Account,
  AccountKind,
  AccountList,
  CreateAccountInput,
  Institution,
  UpdateAccountInput,
} from '@/api/types'
import type { SelectOption } from '@/components/Select/Select'

/** Conta compartilhada entre telas: leitura, criação e edição.
 *
 *  Mora em `lib/` pelo mesmo motivo de `lib/session.ts`: **conta é referência de
 *  todo mundo**, não patrimônio de uma feature. Lançamentos precisa dela para o
 *  filtro, importação para o destino do arquivo, e import entre features é
 *  proibido (`AGENTS.md`). Se morasse em `features/accounts/`, as outras telas
 *  teriam de importar de lá.
 *
 *  Leitura sempre morou aqui; a partir da recuperação da importação, CRIAR e
 *  EDITAR também: quando a fatura vai para a conta errada, a tela de importação
 *  oferece criar a conta de cartão ali mesmo, sem sair para `/contas`. Como o
 *  `AccountDialog` virou componente compartilhado (`components/AccountDialog/`),
 *  a capacidade que ele usa desce para `lib/` junto — um componente base não pode
 *  depender de uma feature. Arquivar e excluir continuam em `features/accounts/`,
 *  onde só a tela de contas as usa. A chave de cache é a mesma, então as telas
 *  compartilham a resposta em vez de buscá-la duas vezes. */

export type {
  Account,
  AccountKind,
  AccountList,
  CreateAccountInput,
  Institution,
  UpdateAccountInput,
}

export const contasQueryKey = (incluirArquivadas: boolean) =>
  ['accounts', { includeArchived: incluirArquivadas }] as const

export function contasQueryOptions(incluirArquivadas = false) {
  return queryOptions({
    queryKey: contasQueryKey(incluirArquivadas),
    queryFn: ({ signal }) =>
      apiRequest<AccountList>(`/accounts?includeArchived=${incluirArquivadas}`, { signal }),
    retry: false,
  })
}

/** Cria uma conta e devolve a conta criada. Compartilhado porque a importação
 *  também cria conta (a de cartão, na recuperação de fatura na conta errada). */
export function createAccount(input: CreateAccountInput) {
  return apiRequest<Account>('/accounts', { method: 'POST', body: input })
}

/** Edita uma conta. Compartilhado junto de `createAccount`: é o outro lado do
 *  mesmo `AccountDialog`. */
export function updateAccount(id: string, input: UpdateAccountInput) {
  return apiRequest<Account>(`/accounts/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: input,
  })
}

/** Rótulos em português dos tipos de conta.
 *
 *  `Record<AccountKind, string>` e não um objeto solto: se o backend ganhar um
 *  tipo novo, o `tsc` cobra o rótulo aqui em vez de a tela mostrar
 *  "credit_card" cru para o usuário. */
export const ROTULO_DO_TIPO: Record<AccountKind, string> = {
  cash: 'Carteira',
  checking: 'Conta corrente',
  savings: 'Poupança',
  credit_card: 'Cartão de crédito',
  other: 'Outra',
}

/** Ordem de exibição no seletor — do mais comum para o menos. */
export const TIPOS_DE_CONTA: readonly AccountKind[] = [
  'checking',
  'cash',
  'savings',
  'credit_card',
  'other',
]

/** Nome das instituições da allowlist do contrato.
 *
 *  `Record<Institution, string>` e não um objeto solto: instituição nova no
 *  backend vira erro de compilação aqui, em vez de um `optgroup` chamado "c6"
 *  aparecendo para o usuário. */
export const ROTULO_DA_INSTITUICAO: Record<Institution, string> = {
  c6: 'C6',
  inter: 'Inter',
  nubank: 'Nubank',
  other: 'Outras',
}

/** A allowlist de instituições em forma de lista, na ordem em que o formulário
 *  as oferece.
 *
 *  Derivada das chaves do rótulo **de propósito**: o `Record<Institution, …>`
 *  acima já é exaustivo por construção, então uma instituição nova no contrato
 *  chega ao `<select>` e ao schema de validação sozinha — não há um segundo
 *  lugar para alguém esquecer de atualizar. */
export const INSTITUICOES = Object.keys(ROTULO_DA_INSTITUICAO) as readonly Institution[]

/** O nome da instituição para MOSTRAR ao lado de uma conta — ou nada.
 *
 *  `other` é o default de quem não informou, e escrever "Outras" embaixo de cada
 *  conta seria ruído repetido em toda a tabela. A ausência já diz o que há para
 *  dizer. */
export function rotuloVisivelDaInstituicao(instituicao: Institution): string | undefined {
  return instituicao === 'other' ? undefined : ROTULO_DA_INSTITUICAO[instituicao]
}

/** Opções do seletor de conta, agrupadas por instituição.
 *
 *  O agrupamento não é enfeite: quem tem conta corrente e cartão no mesmo banco
 *  escolhe errado com frequência, e mandar a fatura do cartão para a conta
 *  corrente é justamente o erro que o backend recusa com
 *  `IMPORT_TARGET_MISMATCH`. Ver "Nubank › Cartão" evita a ida e volta. */
export function opcoesDeConta(contas: readonly Account[]): SelectOption[] {
  return contas.map((conta) => ({
    value: conta.id,
    label: conta.name,
    group: ROTULO_DA_INSTITUICAO[conta.institution],
  }))
}

/** A conta pelo id, para a tela escrever o NOME em vez de repetir o id. */
export function contaPorId(
  contas: readonly Account[],
  id: string | undefined,
): Account | undefined {
  if (!id) return undefined
  return contas.find((conta) => conta.id === id)
}
