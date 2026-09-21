import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { ApiError } from '@/api/client'
import type { Account } from '@/api/types'
import { AccountDialog } from '@/components/AccountDialog/AccountDialog'
import { Alert } from '@/components/Alert/Alert'
import { Badge } from '@/components/Badge/Badge'
import { Button } from '@/components/Button/Button'
import { type Column, DataTable } from '@/components/DataTable/DataTable'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { ArchiveIcon } from '@/components/icons/ArchiveIcon'
import { PencilIcon } from '@/components/icons/PencilIcon'
import { PlusIcon } from '@/components/icons/PlusIcon'
import { TrashIcon } from '@/components/icons/TrashIcon'
import { UnarchiveIcon } from '@/components/icons/UnarchiveIcon'
import { MoneyText } from '@/components/MoneyText/MoneyText'
import { Panel } from '@/components/Panel/Panel'
import { useToast } from '@/components/Toast/Toast'
import { ROTULO_DO_TIPO, rotuloVisivelDaInstituicao } from '@/lib/accounts'
import { messageForError } from '@/lib/errors'
import { useFocoNoTitulo } from '@/lib/focus'
import { FUSO_PADRAO, hojeNoFuso } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import {
  accountsQueryOptions,
  archiveAccount,
  deleteAccount,
  unarchiveAccount,
} from '../api/accounts'
import styles from './AccountsScreen.module.css'

/** Tela de contas.
 *
 *  Uma tabela densa, um total no rodapé e as ações por linha. Sem cartões
 *  grandes: quem abre esta tela quer comparar saldos, e comparar é uma leitura
 *  vertical de números alinhados (docs/DESIGN.md, princípio 4). */
export function AccountsScreen() {
  const queryClient = useQueryClient()
  const toast = useToast()
  // Foco no <h1> na entrada da rota (docs/DESIGN.md).
  const tituloRef = useFocoNoTitulo()

  const [mostrarArquivadas, setMostrarArquivadas] = useState(false)
  const [dialogoAberto, setDialogoAberto] = useState(false)
  const [emEdicao, setEmEdicao] = useState<Account | undefined>(undefined)
  const [avisoDeUso, setAvisoDeUso] = useState<string | undefined>(undefined)

  const session = useQuery(sessionQueryOptions)
  const contas = useQuery(accountsQueryOptions(mostrarArquivadas))

  const fuso = session.data?.household.timezone ?? FUSO_PADRAO
  const hoje = hojeNoFuso(fuso)

  useEffect(() => {
    document.title = 'Contas · HomeFinance'
  }, [])

  const arquivar = useMutation({
    mutationFn: (conta: Account) =>
      conta.archivedAt ? unarchiveAccount(conta.id) : archiveAccount(conta.id),
    onSuccess: async (_resultado, conta) => {
      await queryClient.invalidateQueries({ queryKey: ['accounts'] })
      toast.sucesso(conta.archivedAt ? 'Conta desarquivada.' : 'Conta arquivada.')
    },
    onError: (error) => toast.erro(messageForError(error)),
  })

  const excluir = useMutation({
    mutationFn: (conta: Account) => deleteAccount(conta.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['accounts'] })
      setAvisoDeUso(undefined)
      toast.sucesso('Conta excluída.')
    },
    onError: (error) => {
      // 422 RESOURCE_IN_USE não é "corrija o formulário": é "não dá para
      // excluir, mas dá para arquivar". Por isso vira um aviso permanente na
      // tela, com o caminho alternativo, e não um toast que some.
      if (error instanceof ApiError && error.code === 'RESOURCE_IN_USE') {
        setAvisoDeUso(
          'Esta conta tem lançamentos e não pode ser excluída. Arquive-a: ela sai dos seletores e o histórico continua intacto.',
        )
        return
      }
      toast.erro(messageForError(error))
    },
  })

  const colunas: readonly Column<Account>[] = [
    {
      key: 'nome',
      header: 'Conta',
      render: (conta) => {
        const detalhe = detalheDaConta(conta)
        return (
          <div className={styles.nome}>
            <div className={styles.nomeLinha}>
              <span className={styles.nomeTexto}>{conta.name}</span>
              {conta.archivedAt ? <Badge tone="muted">Arquivada</Badge> : null}
            </div>
            {detalhe ? <p className={styles.detalhe}>{detalhe}</p> : null}
          </div>
        )
      },
    },
    {
      key: 'tipo',
      header: 'Tipo',
      width: 'min',
      render: (conta) => <Badge>{ROTULO_DO_TIPO[conta.kind]}</Badge>,
    },
    {
      key: 'saldo',
      header: 'Saldo',
      align: 'end',
      width: 'min',
      render: (conta) => <MoneyText cents={conta.balanceCents} tone="semantic" />,
    },
    {
      key: 'acoes',
      header: 'Ações',
      headerHidden: true,
      align: 'end',
      width: 'min',
      render: (conta) => (
        <div className={styles.acoes}>
          <Button
            variant="quiet"
            size="sm"
            onClick={() => {
              setEmEdicao(conta)
              setDialogoAberto(true)
            }}
            aria-label={`Editar ${conta.name}`}
            iconStart={<PencilIcon size={16} />}
          >
            {''}
          </Button>
          <Button
            variant="quiet"
            size="sm"
            onClick={() => arquivar.mutate(conta)}
            aria-label={`${conta.archivedAt ? 'Desarquivar' : 'Arquivar'} ${conta.name}`}
            iconStart={conta.archivedAt ? <UnarchiveIcon size={16} /> : <ArchiveIcon size={16} />}
          >
            {''}
          </Button>
          <Button
            variant="quiet"
            size="sm"
            onClick={() => excluir.mutate(conta)}
            aria-label={`Excluir ${conta.name}`}
            iconStart={<TrashIcon size={16} />}
          >
            {''}
          </Button>
        </div>
      ),
    },
  ]

  const itens = contas.data?.items ?? []

  return (
    <div className={styles.pagina}>
      <div className={styles.cabecalho}>
        <div>
          <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
            Contas
          </h1>
          <p className={styles.apoio}>Onde o dinheiro da casa está.</p>
        </div>
        <Button
          variant="primary"
          onClick={() => {
            setEmEdicao(undefined)
            setDialogoAberto(true)
          }}
          iconStart={<PlusIcon size={18} />}
        >
          Nova conta
        </Button>
      </div>

      {avisoDeUso ? (
        <div className={styles.aviso}>
          <Alert tone="warning" title="Não dá para excluir esta conta">
            {avisoDeUso}
          </Alert>
        </div>
      ) : null}

      {contas.isError ? (
        <Alert
          tone="error"
          title="Não foi possível carregar as contas."
          action={
            <Button onClick={() => void contas.refetch()} loading={contas.isFetching}>
              Tentar de novo
            </Button>
          }
        >
          {messageForError(contas.error)}
        </Alert>
      ) : (
        <Panel padding="none">
          <div className={styles.filtros}>
            <label className={styles.alternador}>
              <input
                type="checkbox"
                checked={mostrarArquivadas}
                onChange={(event) => setMostrarArquivadas(event.target.checked)}
              />
              <span>Mostrar arquivadas</span>
            </label>
          </div>

          <DataTable
            caption="Contas da casa, com instituição, tipo e saldo"
            columns={colunas}
            rows={itens}
            rowKey={(conta) => conta.id}
            rowAttrs={(conta) => ({ 'data-archived': conta.archivedAt ? 'true' : undefined })}
            loading={contas.isPending}
            empty={
              <EmptyState
                title="Nenhuma conta ainda."
                description="Comece pela conta onde o dinheiro entra — normalmente a conta corrente. Depois some a carteira e o cartão."
                action={
                  <Button
                    variant="primary"
                    onClick={() => {
                      setEmEdicao(undefined)
                      setDialogoAberto(true)
                    }}
                    iconStart={<PlusIcon size={18} />}
                  >
                    Criar a primeira conta
                  </Button>
                }
              />
            }
            footer={
              itens.length > 0 ? (
                <tr>
                  <th scope="row" colSpan={2}>
                    Total
                  </th>
                  <td data-align="end">
                    <MoneyText
                      cents={contas.data?.totalBalanceCents ?? 0}
                      tone="semantic"
                      emphasis="total"
                    />
                  </td>
                  <td />
                </tr>
              ) : undefined
            }
          />
        </Panel>
      )}

      <AccountDialog
        open={dialogoAberto}
        onClose={() => setDialogoAberto(false)}
        account={emEdicao}
        hoje={hoje}
      />
    </div>
  )
}

/** A segunda linha da célula de conta: instituição e, no cartão, os dias da
 *  fatura.
 *
 *  Por que uma linha de apoio e não uma coluna: instituição é **contexto**, não
 *  um número para comparar na vertical, e uma coluna a mais empurraria o saldo
 *  — que é o que se vem ver aqui — para a beira da tela. Por que não uma
 *  `Badge`: etiqueta neste app marca estado (arquivada, a vencer), e gastar a
 *  forma de estado com uma classificação a esvaziaria.
 *
 *  `other` não aparece: é o default de quem não informou, e repetir "Outras"
 *  linha após linha é ruído (`rotuloVisivelDaInstituicao`). Os dias só saem no
 *  cartão porque só lá significam alguma coisa — e é vê-los aqui que diz à
 *  pessoa se a importação da fatura vai sugerir as datas ou adivinhá-las. */
function detalheDaConta(conta: Account): string | undefined {
  const partes: string[] = []
  const banco = rotuloVisivelDaInstituicao(conta.institution)
  if (banco) partes.push(banco)
  if (conta.kind === 'credit_card') {
    // `typeof` e não `!== null`: enquanto o campo não estiver na resposta de
    // todos os ambientes, `undefined` passaria pelo teste de nulo e escreveria
    // "fecha dia undefined" na tela.
    if (typeof conta.statementClosingDay === 'number') {
      partes.push(`fecha dia ${conta.statementClosingDay}`)
    }
    if (typeof conta.statementDueDay === 'number') {
      partes.push(`vence dia ${conta.statementDueDay}`)
    }
  }
  return partes.length > 0 ? partes.join(' · ') : undefined
}
