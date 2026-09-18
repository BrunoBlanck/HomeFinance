import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { ApiError } from '@/api/client'
import type { Category, CategoryKind } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Badge } from '@/components/Badge/Badge'
import { Button } from '@/components/Button/Button'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { ArchiveIcon } from '@/components/icons/ArchiveIcon'
import { PencilIcon } from '@/components/icons/PencilIcon'
import { PlusIcon } from '@/components/icons/PlusIcon'
import { TrashIcon } from '@/components/icons/TrashIcon'
import { UnarchiveIcon } from '@/components/icons/UnarchiveIcon'
import { Panel } from '@/components/Panel/Panel'
import { Skeleton } from '@/components/Skeleton/Skeleton'
import { useToast } from '@/components/Toast/Toast'
import { NATUREZAS } from '@/lib/categories'
import { messageForError } from '@/lib/errors'
import { useFocoNoTitulo } from '@/lib/focus'
import {
  archiveCategory,
  categoriesQueryOptions,
  deleteCategory,
  ROTULO_DA_NATUREZA,
  ROTULO_PLURAL_DA_NATUREZA,
  SUBTITULO_DA_NATUREZA,
  unarchiveCategory,
} from '../api/categories'
import { type AlvoDoDialogo, CategoryDialog } from '../components/CategoryDialog'
import styles from './CategoriesScreen.module.css'

/** Tela de categorias.
 *
 *  A árvore de dois níveis é desenhada como QUATRO listas — despesas, receitas,
 *  investimentos e resgates (ADR-029a) — e não como uma tabela: categoria não
 *  tem colunas de dado para comparar, tem hierarquia para enxergar. Uma `<ul>`
 *  aninhada diz "isto está dentro daquilo" para o leitor de tela de graça; uma
 *  tabela com coluna "pai" não diria.
 *
 *  As naturezas novas não pediram seção de código nova: é a mesma estrutura,
 *  quatro vezes, com o mesmo diálogo e o mesmo `KeywordsField`. A grade de duas
 *  colunas põe o lado que SAI da conta à esquerda (Despesas, Investimentos) e o
 *  que ENTRA à direita (Receitas, Resgates). */
export function CategoriesScreen() {
  const queryClient = useQueryClient()
  const toast = useToast()
  // Foco no <h1> na entrada da rota (docs/DESIGN.md).
  const tituloRef = useFocoNoTitulo()

  const [mostrarArquivadas, setMostrarArquivadas] = useState(false)
  const [alvo, setAlvo] = useState<AlvoDoDialogo | null>(null)
  const [avisoDeUso, setAvisoDeUso] = useState<string | undefined>(undefined)

  const arvore = useQuery(categoriesQueryOptions(mostrarArquivadas))

  useEffect(() => {
    document.title = 'Categorias · HomeFinance'
  }, [])

  const arquivar = useMutation({
    mutationFn: (categoria: Category) =>
      categoria.archivedAt ? unarchiveCategory(categoria.id) : archiveCategory(categoria.id),
    onSuccess: async (_resultado, categoria) => {
      await queryClient.invalidateQueries({ queryKey: ['categories'] })
      toast.sucesso(categoria.archivedAt ? 'Categoria desarquivada.' : 'Categoria arquivada.')
    },
    onError: (error) => {
      if (error instanceof ApiError && error.invalidFields.includes('parentId')) {
        toast.erro('Desarquive o grupo antes de desarquivar esta subcategoria.')
        return
      }
      toast.erro(messageForError(error))
    },
  })

  const excluir = useMutation({
    mutationFn: (categoria: Category) => deleteCategory(categoria.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['categories'] })
      setAvisoDeUso(undefined)
      toast.sucesso('Categoria excluída.')
    },
    onError: (error, categoria) => {
      if (error instanceof ApiError && error.code === 'RESOURCE_IN_USE') {
        setAvisoDeUso(
          categoria.children.length > 0
            ? `"${categoria.name}" tem subcategorias. Exclua ou arquive as subcategorias primeiro — ou arquive o grupo inteiro, que leva as filhas junto.`
            : `"${categoria.name}" está em uso e não pode ser excluída. Arquive-a: ela sai dos seletores e o histórico continua intacto.`,
        )
        return
      }
      toast.erro(messageForError(error))
    },
  })

  const acoes = { arquivar, excluir, setAlvo }
  // Vazio de verdade é vazio nas QUATRO naturezas: uma casa que só tem o grupo
  // "Investimentos" da semente não é uma casa sem categorias.
  const semNada =
    !arvore.isPending && NATUREZAS.every((natureza) => (arvore.data?.[natureza]?.length ?? 0) === 0)

  return (
    <div className={styles.pagina}>
      <div className={styles.cabecalho}>
        <div>
          <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
            Categorias
          </h1>
          <p className={styles.apoio}>
            Dois níveis: um grupo e, dentro dele, as subcategorias. Arquivar preserva o histórico;
            excluir só é possível quando nunca houve uso.
          </p>
        </div>
        <Button
          variant="primary"
          onClick={() => setAlvo({ modo: 'novo-grupo' })}
          iconStart={<PlusIcon size={18} />}
        >
          Novo grupo
        </Button>
      </div>

      {avisoDeUso ? (
        <Alert tone="warning" title="Não dá para excluir esta categoria">
          {avisoDeUso}
        </Alert>
      ) : null}

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

      {arvore.isError ? (
        <Alert
          tone="error"
          title="Não foi possível carregar as categorias."
          action={
            <Button onClick={() => void arvore.refetch()} loading={arvore.isFetching}>
              Tentar de novo
            </Button>
          }
        >
          {messageForError(arvore.error)}
        </Alert>
      ) : arvore.isPending ? (
        <Panel padding="lg">
          <span className="sr-only">Carregando categorias</span>
          <div className={styles.esqueleto} aria-hidden="true">
            <Skeleton width="12rem" height="1.25rem" />
            <Skeleton width="18rem" height="1rem" />
            <Skeleton width="16rem" height="1rem" />
          </div>
        </Panel>
      ) : semNada ? (
        <Panel padding="none">
          <EmptyState
            title="Nenhuma categoria."
            description="Toda casa nova nasce com um conjunto em português — se ele sumiu, crie os grupos que fizerem sentido para vocês."
            action={
              <Button
                variant="primary"
                onClick={() => setAlvo({ modo: 'novo-grupo' })}
                iconStart={<PlusIcon size={18} />}
              >
                Criar o primeiro grupo
              </Button>
            }
          />
        </Panel>
      ) : (
        <div className={styles.colunas}>
          {NATUREZAS.map((natureza) => (
            <Bloco
              key={natureza}
              kind={natureza}
              grupos={arvore.data?.[natureza] ?? []}
              {...acoes}
            />
          ))}
        </div>
      )}

      {alvo ? (
        <CategoryDialog open={alvo !== null} onClose={() => setAlvo(null)} alvo={alvo} />
      ) : null}
    </div>
  )
}

type AcoesDeLinha = {
  arquivar: { mutate: (categoria: Category) => void }
  excluir: { mutate: (categoria: Category) => void }
  setAlvo: (alvo: AlvoDoDialogo) => void
}

function Bloco({
  kind,
  grupos,
  ...acoes
}: { kind: CategoryKind; grupos: readonly Category[] } & AcoesDeLinha) {
  return (
    <Panel
      as="section"
      title={ROTULO_PLURAL_DA_NATUREZA[kind]}
      subtitle={SUBTITULO_DA_NATUREZA[kind]}
      padding="none"
    >
      {grupos.length === 0 ? (
        <p className={styles.vazioBloco}>
          Nenhum grupo de {ROTULO_DA_NATUREZA[kind].toLowerCase()} ainda.
        </p>
      ) : (
        // biome-ignore lint/a11y/noRedundantRoles: o reset zera marcador e recuo justamente por [role=list]; sem ele o Safari descarta a semantica de lista
        <ul className={styles.grupos} role="list">
          {grupos.map((grupo) => (
            <li key={grupo.id} className={styles.grupo}>
              <Linha categoria={grupo} nivel="grupo" {...acoes} />
              {grupo.children.length > 0 ? (
                // biome-ignore lint/a11y/noRedundantRoles: idem
                <ul className={styles.filhas} role="list">
                  {grupo.children.map((filha) => (
                    <li key={filha.id}>
                      <Linha categoria={filha} nivel="filha" {...acoes} />
                    </li>
                  ))}
                </ul>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}

function Linha({
  categoria,
  nivel,
  arquivar,
  excluir,
  setAlvo,
}: { categoria: Category; nivel: 'grupo' | 'filha' } & AcoesDeLinha) {
  const ehGrupo = nivel === 'grupo'

  return (
    <div className={styles.linha} data-archived={categoria.archivedAt ? 'true' : undefined}>
      <span className={styles.nome}>{categoria.name}</span>
      {categoria.archivedAt ? <Badge tone="muted">Arquivada</Badge> : null}

      <div className={styles.acoes}>
        {ehGrupo && !categoria.archivedAt ? (
          <Button
            variant="quiet"
            size="sm"
            onClick={() => setAlvo({ modo: 'nova-subcategoria', grupo: categoria })}
            aria-label={`Nova subcategoria em ${categoria.name}`}
            iconStart={<PlusIcon size={16} />}
          >
            {''}
          </Button>
        ) : null}
        <Button
          variant="quiet"
          size="sm"
          onClick={() => setAlvo({ modo: 'editar', categoria })}
          aria-label={`Editar ${categoria.name}`}
          iconStart={<PencilIcon size={16} />}
        >
          {''}
        </Button>
        <Button
          variant="quiet"
          size="sm"
          onClick={() => arquivar.mutate(categoria)}
          aria-label={`${categoria.archivedAt ? 'Desarquivar' : 'Arquivar'} ${categoria.name}`}
          iconStart={categoria.archivedAt ? <UnarchiveIcon size={16} /> : <ArchiveIcon size={16} />}
        >
          {''}
        </Button>
        <Button
          variant="quiet"
          size="sm"
          onClick={() => excluir.mutate(categoria)}
          aria-label={`Excluir ${categoria.name}`}
          iconStart={<TrashIcon size={16} />}
        >
          {''}
        </Button>
      </div>
    </div>
  )
}
