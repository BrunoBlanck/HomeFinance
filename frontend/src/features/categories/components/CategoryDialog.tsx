import { type QueryClient, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { ApiError } from '@/api/client'
import type { Category, CategoryKind, CategoryTree } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { Dialog } from '@/components/Dialog/Dialog'
import { KeywordsField } from '@/components/KeywordsField/KeywordsField'
import { Select } from '@/components/Select/Select'
import { TextField } from '@/components/TextField/TextField'
import { useToast } from '@/components/Toast/Toast'
import { categoriaPorId, mesmoLadoDoDinheiro, NATUREZAS } from '@/lib/categories'
import { keywordTakenOf, messageForError } from '@/lib/errors'
import { indiceDaPalavra, normalizarPalavra } from '@/lib/keywords'
import { createCategory, ROTULO_DA_NATUREZA, updateCategory } from '../api/categories'
import styles from './CategoryDialog.module.css'

/** Dica fixa do campo de palavras-chave — tabela (g) de `docs/DESIGN.md`. */
const DICA_PALAVRAS_CHAVE =
  'Prefira o nome do estabelecimento — «padaria», «uber», «netflix». Enter ou vírgula adiciona.'

export type AlvoDoDialogo =
  /** Criar um grupo (nível 1). */
  | { modo: 'novo-grupo' }
  /** Criar uma subcategoria dentro de um grupo. */
  | { modo: 'nova-subcategoria'; grupo: Category }
  /** Renomear (e, num GRUPO, trocar a natureza — ADR-029c). */
  | { modo: 'editar'; categoria: Category }

type CategoryDialogProps = {
  open: boolean
  onClose: () => void
  alvo: AlvoDoDialogo
}

/** Erro do servidor sobre UMA palavra-chave. Guarda a forma normalizada, não o
 *  índice: o índice é derivado da lista atual a cada render, então remover a
 *  ficha marcada apaga o erro sozinho — e reordenar nunca marca a ficha errada. */
type ErroDePalavra = { normalizada: string; mensagem: string }

type Erros = {
  name?: string
  kind?: string
  keyword?: ErroDePalavra
  /** Erro do servidor sobre a LISTA (`fields.keywords`), não sobre uma ficha:
   *  o limite de 20, ou palavras num grupo com subcategorias (spec 0005 §12). */
  keywords?: string
  geral?: string
}

/** Emenda §12 da spec 0005: grupo com subcategoria ATIVA não recebe
 *  lançamento diretamente, então palavra-chave nele nunca sugeriria nada — o
 *  servidor responde 400 em `fields.keywords`. Grupo cujas filhas estão todas
 *  arquivadas volta a aceitar. */
function temSubcategoriasAtivas(categoria: Category): boolean {
  return categoria.parentId === null && categoria.children.some((filha) => !filha.archivedAt)
}

/** "3 palavras-chave sem efeito enquanto o grupo tiver subcategorias" — o caso
 *  residual: o grupo tinha palavras e DEPOIS ganhou uma subcategoria. Elas
 *  ficam inertes até serem movidas à mão; o campo continua visível para poder
 *  limpar. */
function notaDePalavrasInertes(quantas: number): string {
  if (quantas === 0) return 'Palavras-chave ficam nas subcategorias.'
  return quantas === 1
    ? '1 palavra-chave sem efeito enquanto o grupo tiver subcategorias.'
    : `${quantas} palavras-chave sem efeito enquanto o grupo tiver subcategorias.`
}

/** O que muda nos números quando a natureza troca DENTRO do mesmo lado do
 *  dinheiro, pela natureza de DESTINO (ADR-029c).
 *
 *  A palavra do movimento é *aporte* e *resgate* — léxico (a) da seção E7 de
 *  docs/DESIGN.md. "Investimento" é o nome da natureza e da seção do app, nunca
 *  o do lançamento. */
const EFEITO_DA_TROCA: Record<CategoryKind, string> = {
  investment: 'saem dos totais de despesa e passam a contar como aportes',
  redemption: 'saem dos totais de receita e passam a contar como resgates',
  expense: 'voltam para os totais de despesa e deixam de contar como aportes',
  income: 'voltam para os totais de receita e deixam de contar como resgates',
}

/** O aviso que PRECEDE a troca permitida (ADR-029c).
 *
 *  Dentro do mesmo lado do dinheiro a troca vale mesmo com a categoria em uso,
 *  e num grupo ela desce para todas as subcategorias. O preço está declarado no
 *  ADR: os números de receita e de despesa de meses já fechados mudam no
 *  instante do salvar. Quem confirma tem de saber disso ANTES — descobrir
 *  depois, pelo relatório, é descobrir do pior jeito.
 *
 *  Cruzar o lado (despesa ↔ receita) não ganha aviso nenhum: quem sabe se há
 *  lançamento pendurado é o servidor, e adivinhar aqui seria acertar às vezes —
 *  a recusa chega como 422 em `fields.kind` e cai no próprio campo. */
function efeitoDaTroca(de: CategoryKind, para: CategoryKind): string | undefined {
  if (de === para || !mesmoLadoDoDinheiro(de, para)) return undefined
  return EFEITO_DA_TROCA[para]
}

export function CategoryDialog({ open, onClose, alvo }: CategoryDialogProps) {
  const queryClient = useQueryClient()
  const toast = useToast()

  const [name, setName] = useState('')
  const [kind, setKind] = useState<CategoryKind>('expense')
  const [keywords, setKeywords] = useState<string[]>([])
  const [erros, setErros] = useState<Erros>({})

  useEffect(() => {
    if (!open) return
    setErros({})
    if (alvo.modo === 'editar') {
      setName(alvo.categoria.name)
      setKind(alvo.categoria.kind)
      setKeywords(alvo.categoria.keywords)
      return
    }
    setName('')
    setKind(alvo.modo === 'nova-subcategoria' ? alvo.grupo.kind : 'expense')
    setKeywords([])
  }, [open, alvo])

  // Grupo com subcategorias ativas: o campo de palavras-chave só aparece se o
  // grupo JÁ tiver palavras (para poder limpá-las); senão, só a nota.
  const grupoComFilhas = alvo.modo === 'editar' && temSubcategoriasAtivas(alvo.categoria)
  const mostraPalavras = !grupoComFilhas || alvo.categoria.keywords.length > 0

  // Quem troca a natureza é o GRUPO, em qualquer direção — inclusive com
  // subcategorias e com lançamentos pendurados (ADR-029c). A folha nunca: ela
  // herda do grupo (ADR-017b), e mudar só a folha criaria uma filha de natureza
  // diferente do pai.
  const editaGrupo = alvo.modo === 'editar' && alvo.categoria.parentId === null
  const efeito = alvo.modo === 'editar' ? efeitoDaTroca(alvo.categoria.kind, kind) : undefined

  const salvar = useMutation({
    mutationFn: async () => {
      // `keywords` vai SEMPRE, e inteira: o formulário é dono da lista e o
      // contrato é de substituição — `[]` limpa. Mandar só quando mudou
      // obrigaria a comparar listas para adivinhar a intenção.
      if (alvo.modo === 'editar') {
        // `kind` só viaja no GRUPO: mandá-lo numa folha seria pedir um
        // ErrKindLocked que o usuário não provocou — ela herda do grupo.
        return updateCategory(
          alvo.categoria.id,
          editaGrupo ? { name, kind, keywords } : { name, keywords },
        )
      }
      if (alvo.modo === 'nova-subcategoria') {
        // Sem `kind`: a subcategoria HERDA a natureza do grupo, e quem manda
        // nesse campo é o servidor (invariante 4 da spec 0003).
        return createCategory({ name, parentId: alvo.grupo.id, keywords })
      }
      return createCategory({ name, kind, keywords })
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['categories'] })
      toast.sucesso(alvo.modo === 'editar' ? 'Categoria atualizada.' : 'Categoria criada.')
      onClose()
    },
    onError: (error) => setErros(traduzir(error, keywords, queryClient, grupoComFilhas)),
  })

  // A ficha marcada é a que AINDA está na lista com a forma normalizada do
  // erro. Removida, o índice vira -1 e o erro some — sem efeito nem callback.
  const indiceMarcado = erros.keyword ? indiceDaPalavra(keywords, erros.keyword.normalizada) : -1

  function enviar(event: React.FormEvent) {
    event.preventDefault()
    if (salvar.isPending) return
    if (name.trim() === '') {
      setErros({ name: 'Informe um nome.' })
      return
    }
    setErros({})
    salvar.mutate()
  }

  const titulo = tituloDe(alvo)

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={titulo.texto}
      description={titulo.descricao}
      footer={
        <>
          <Button variant="quiet" onClick={onClose}>
            Cancelar
          </Button>
          <Button variant="primary" type="submit" form="form-categoria" loading={salvar.isPending}>
            {alvo.modo === 'editar' ? 'Salvar' : 'Criar'}
          </Button>
        </>
      }
    >
      <form id="form-categoria" onSubmit={enviar} noValidate className={styles.form}>
        {erros.geral ? <Alert tone="error">{erros.geral}</Alert> : null}

        <TextField
          label="Nome"
          value={name}
          onChange={(event) => setName(event.target.value)}
          maxLength={60}
          autoComplete="off"
          autoFocus
          error={erros.name}
        />

        {alvo.modo === 'novo-grupo' || editaGrupo ? (
          <Select
            label="Natureza"
            value={kind}
            onChange={(event) => setKind(event.target.value as CategoryKind)}
            options={NATUREZAS.map((natureza) => ({
              value: natureza,
              label: ROTULO_DA_NATUREZA[natureza],
            }))}
            error={erros.kind}
            hint={
              editaGrupo
                ? 'As subcategorias do grupo acompanham a troca.'
                : 'As subcategorias deste grupo herdam esta escolha.'
            }
          />
        ) : null}

        {/* `live="polite"`: é PRÉVIA de uma ação ainda não feita, e monta
            enquanto a pessoa ainda está no `<select>` — no Windows a seta já
            troca o valor de um select fechado, e um live region assertivo
            interromperia o anúncio da opção recém-escolhida (E7 (l)). */}
        {alvo.modo === 'editar' && efeito ? (
          <Alert tone="warning" live="polite" title="Isto muda os totais de meses já fechados">
            Os lançamentos de <strong>{alvo.categoria.name}</strong> continuam na lista e no saldo
            da conta, mas {efeito} — em todos os meses, não só neste.{' '}
            {alvo.categoria.children.length > 0
              ? 'As subcategorias, inclusive as arquivadas, mudam junto. '
              : ''}
            Dá para voltar atrás pelo mesmo caminho.
          </Alert>
        ) : null}

        {alvo.modo === 'nova-subcategoria' ? (
          <p className={styles.heranca}>
            {/* "de natureza X", e não "será uma X": com quatro naturezas o
                artigo deixa de concordar — "uma investimento" é o tipo de
                frase que só nasce quando a lista cresce e ninguém releu. */}
            Vai ficar dentro de <strong>{alvo.grupo.name}</strong> e herda do grupo a natureza{' '}
            <strong>{ROTULO_DA_NATUREZA[alvo.grupo.kind].toLowerCase()}</strong>.
          </p>
        ) : null}

        {alvo.modo === 'editar' && !editaGrupo ? (
          <p className={styles.heranca}>
            {/* Imperativo, não descrição: quem abre o diálogo da folha está
                procurando o campo que não está lá. */}
            Subcategoria acompanha o grupo: troque a natureza no grupo, e as filhas vão junto.
          </p>
        ) : null}

        {grupoComFilhas ? (
          <p className={styles.heranca}>{notaDePalavrasInertes(keywords.length)}</p>
        ) : null}

        {/* Último campo, em todos os modos: é o opcional e avançado — por
            último, não interrompe quem só quer dar um nome. */}
        {mostraPalavras ? (
          <KeywordsField
            value={keywords}
            onChange={setKeywords}
            hint={DICA_PALAVRAS_CHAVE}
            error={erros.keywords ?? (indiceMarcado >= 0 ? erros.keyword?.mensagem : undefined)}
            invalidIndex={indiceMarcado >= 0 ? indiceMarcado : undefined}
          />
        ) : null}
      </form>
    </Dialog>
  )
}

function tituloDe(alvo: AlvoDoDialogo): { texto: string; descricao: string } {
  switch (alvo.modo) {
    case 'novo-grupo':
      return {
        texto: 'Novo grupo',
        descricao: 'Grupos organizam as subcategorias — Moradia, Transporte, Alimentação.',
      }
    case 'nova-subcategoria':
      return {
        texto: 'Nova subcategoria',
        descricao: 'Detalha um grupo — Energia dentro de Moradia, por exemplo.',
      }
    case 'editar':
      return {
        texto: 'Editar categoria',
        descricao: 'Renomear não afeta os lançamentos já registrados.',
      }
  }
}

/** O nome da categoria `id` em qualquer árvore que esteja no cache — com ou
 *  sem arquivadas. Só o nome: o `ownerId` nunca chega à tela cru. */
function nomeDaCategoriaNoCache(queryClient: QueryClient, id: string): string | undefined {
  for (const [, arvore] of queryClient.getQueriesData<CategoryTree>({ queryKey: ['categories'] })) {
    // Pelas QUATRO naturezas: a dona da palavra pode ser uma categoria de
    // investimento ou de resgate (ADR-029a).
    const dona = categoriaPorId(arvore, id)
    if (dona) return dona.name
  }
  return undefined
}

/** 409 `KEYWORD_TAKEN` → a ficha e a frase da tabela (g): "«padaria» já está
 *  em Alimentação." — ou, quando a dona não está no cache (corrida rara em que
 *  o índice único decidiu), "…já está em outra categoria desta casa." */
function erroDePalavraTomada(
  error: ApiError,
  keywords: readonly string[],
  queryClient: QueryClient,
): ErroDePalavra | undefined {
  const conflito = keywordTakenOf(error)
  if (!conflito) return undefined
  const normalizada = normalizarPalavra(conflito.keyword)
  // A palavra como a pessoa a digitou, quando ainda está na lista; senão a
  // que o servidor devolveu.
  const palavra = keywords[indiceDaPalavra(keywords, normalizada)] ?? conflito.keyword
  const dona = conflito.ownerId ? nomeDaCategoriaNoCache(queryClient, conflito.ownerId) : undefined
  return {
    normalizada,
    mensagem: dona
      ? `«${palavra}» já está em ${dona}.`
      : `«${palavra}» já está em outra categoria desta casa.`,
  }
}

/** 400 `fields.keywords[i]` → a ficha `i` com a frase da tabela (g). */
function erroDePalavraInvalida(
  campo: string,
  keywords: readonly string[],
): ErroDePalavra | undefined {
  const indice = /^keywords\[(\d+)\]$/.exec(campo)?.[1]
  if (indice === undefined) return undefined
  const palavra = keywords[Number(indice)]
  if (palavra === undefined) return undefined
  return {
    normalizada: normalizarPalavra(palavra),
    mensagem: `«${palavra}» não é uma palavra-chave válida: de 2 a 40 caracteres, só letras, números, espaço e & . - / '`,
  }
}

function traduzir(
  error: unknown,
  keywords: readonly string[],
  queryClient: QueryClient,
  grupoComFilhas: boolean,
): Erros {
  if (!(error instanceof ApiError)) return { geral: messageForError(error) }

  const tomada = erroDePalavraTomada(error, keywords, queryClient)
  if (tomada) return { keyword: tomada }

  if (error.invalidFields.length > 0) {
    const erros: Erros = {}
    for (const campo of error.invalidFields) {
      const palavraInvalida = erroDePalavraInvalida(campo, keywords)
      if (palavraInvalida) {
        // Uma ficha marcada por vez: a primeira que o servidor apontou.
        erros.keyword ??= palavraInvalida
        continue
      }
      switch (campo) {
        case 'name':
          erros.name = 'Já existe uma categoria com este nome aqui, ou o nome é inválido.'
          break
        case 'kind':
          // A única troca que o servidor ainda recusa é CRUZAR o lado do
          // dinheiro (ADR-029c). A mensagem precisa dizer qual troca continua
          // valendo, senão ensina o oposto da regra nova.
          erros.kind =
            'Não dá para trocar entre receita e despesa numa categoria em uso ou com subcategorias. Entre despesa e investimento, ou entre receita e resgate, a troca vale.'
          break
        case 'parentId':
          erros.geral = 'O grupo escolhido não pode receber esta categoria.'
          break
        case 'keywords':
          // O campo é a LISTA. Qual das duas recusas foi, o diálogo sabe
          // sozinho: num grupo com subcategorias ativas é a §12; no resto, o
          // limite. A frase é nossa — o servidor manda o campo, não a prosa.
          erros.keywords = grupoComFilhas
            ? 'Palavras-chave ficam nas subcategorias.'
            : 'Limite de 20 palavras-chave. Remova uma para incluir outra.'
          break
        case 'limit':
          erros.geral = 'Você atingiu o limite de categorias desta casa.'
          break
        default:
          erros.geral = 'Confira os dados informados e tente de novo.'
      }
    }
    return erros
  }

  return { geral: messageForError(error) }
}
