import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { Category, CategoryTree } from '@/api/types'
import { Button } from '@/components/Button/Button'
import { CheckIcon } from '@/components/icons/CheckIcon'
import { PlusIcon } from '@/components/icons/PlusIcon'
import { useToast } from '@/components/Toast/Toast'
import { categoriaPorId, categoriasQueryKey, updateCategory } from '@/lib/categories'
import { keywordTakenOf, messageForError } from '@/lib/errors'
import { citarPalavra, palavrasParaAprender } from '@/lib/keywords'
import styles from './FichasDeAprender.module.css'

type Props = {
  descricao: string
  /** A categoria que a pessoa acabou de escolher para a linha. */
  categoria: Category
  /** Chamado depois que uma palavra foi gravada — a tela devolve o foco ao
   *  `<select>` de categoria da linha. */
  onAprendida: () => void
}

/** "Da próxima vez, reconhecer por" — as palavras da descrição como fichas.
 *
 *  Aparece numa linha SEM sugestão no momento em que a pessoa escolhe uma
 *  categoria à mão: é o instante em que ela acabou de fazer, manualmente, o
 *  trabalho que a palavra-chave faria sozinha — e é a hora certa de oferecer
 *  o atalho. Não aparece em linha com sugestão (mesmo trocada): a linha já
 *  ensina sozinha.
 *
 *  Cada ficha é o `Button quiet sm` do sistema, sem estilo inventado: nenhum
 *  chip arredondado, nenhum fundo colorido. Clicar lê a categoria do CACHE
 *  (a lista inteira de palavras) e manda `PATCH` com a lista mais a palavra —
 *  o `PATCH` substitui a lista, então mandar só a nova apagaria as outras.
 *
 *  Sucesso **não** re-analisa o lote: a análise já passou, e o efeito é da
 *  próxima importação em diante — o toast diz isso com todas as letras. */
export function FichasDeAprender({ descricao, categoria, onAprendida }: Props) {
  const queryClient = useQueryClient()
  const toast = useToast()
  // As palavras que ESTA ficha gravou. Vivem aqui, e não só no cache: depois da
  // invalidação a palavra passa a "já estar na categoria" e sairia da lista —
  // mas a ficha desta linha tem de continuar visível como texto estático,
  // dizendo que foi feito.
  const [aprendidas, setAprendidas] = useState<readonly string[]>([])
  const [emAndamento, setEmAndamento] = useState<string | null>(null)

  const aprender = useMutation({
    mutationFn: async (palavra: string) => {
      // A lista mais recente é a do cache no momento do clique, não a da
      // renderização: duas fichas seguidas na mesma categoria têm de somar.
      const arvore = queryClient.getQueryData<CategoryTree>(categoriasQueryKey(false))
      const atual = categoriaPorId(arvore, categoria.id) ?? categoria
      return updateCategory(categoria.id, { keywords: [...atual.keywords, palavra] })
    },
    onMutate: (palavra) => setEmAndamento(palavra),
    onSuccess: async (_categoria, palavra) => {
      setAprendidas((lista) => [...lista, palavra])
      toast.sucesso(
        `${citarPalavra(palavra)} adicionada a ${categoria.name}. Vale a partir da próxima importação.`,
      )
      onAprendida()
      // As outras linhas com o mesmo token perdem a ficha: a palavra já é da
      // categoria.
      await queryClient.invalidateQueries({ queryKey: ['categories'] })
    },
    onError: (erro, palavra) => {
      const conflito = keywordTakenOf(erro)
      if (conflito) {
        const arvore = queryClient.getQueryData<CategoryTree>(categoriasQueryKey(false))
        const dona = categoriaPorId(arvore, conflito.ownerId)
        toast.erro(
          dona
            ? `${citarPalavra(palavra)} já está em ${dona.name}.`
            : `${citarPalavra(palavra)} já está em outra categoria desta casa.`,
        )
        return
      }
      toast.erro(messageForError(erro))
    },
    onSettled: () => setEmAndamento(null),
  })

  const fichas = palavrasParaAprender(descricao, categoria, aprendidas)
  if (fichas.length === 0) return null

  return (
    <div className={styles.fichas}>
      <span className={styles.rotulo}>Da próxima vez, reconhecer por</span>
      {fichas.map((palavra) =>
        aprendidas.includes(palavra) ? (
          <span key={palavra} className={styles.aprendida}>
            <CheckIcon size={14} />
            {palavra}
          </span>
        ) : (
          <Button
            key={palavra}
            variant="quiet"
            size="sm"
            iconStart={<PlusIcon size={14} />}
            loading={emAndamento === palavra}
            aria-label={`Adicionar ${citarPalavra(palavra)} às palavras-chave de ${categoria.name}`}
            onClick={() => {
              if (emAndamento !== null) return
              aprender.mutate(palavra)
            }}
          >
            {palavra}
          </Button>
        ),
      )}
    </div>
  )
}
