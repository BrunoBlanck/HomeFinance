import { type InfiniteData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { type KeyboardEvent, useEffect, useRef, useState } from 'react'
import type { Category, CategoryTree, Transaction, TransactionList } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { CheckIcon } from '@/components/icons/CheckIcon'
import { ChevronDownIcon } from '@/components/icons/ChevronDownIcon'
import { PlusIcon } from '@/components/icons/PlusIcon'
import { Select } from '@/components/Select/Select'
import { useToast } from '@/components/Toast/Toast'
import {
  categoriaPorId,
  categoriasQueryKey,
  categoriasQueryOptions,
  type LadoDoDinheiro,
  opcoesDeCategoria,
  updateCategory,
} from '@/lib/categories'
import { isCategoriaRecusada, keywordTakenOf, messageForError } from '@/lib/errors'
import { citarPalavra, MAX_PALAVRAS_CHAVE, palavrasParaAprender } from '@/lib/keywords'
import { ehTransferencia } from '@/lib/kind'
import { nomeDoMes } from '@/lib/month'
import { autoCategorize, updateTransactionCategory } from '../api/transactions'
import { rotuloDaLinha } from '../lexico'
import styles from './AtalhoDeCategoria.module.css'

/** Atalho de categorização em `/lancamentos` (spec 0005 §11; docs/DESIGN.md,
 *  E2c (h)).
 *
 *  Dois pedaços, ligados pela tela através do `editandoId`:
 *
 *  - `BotaoSemCategoria` — o controle da célula. Não é `Button`: é o quarto
 *    portador de estado, a **lacuna** a preencher — texto `Sem categoria`
 *    tracejado, peso 400, com o chevron. Padrão APG *disclosure*:
 *    `aria-expanded` e, só quando aberto, `aria-controls` para o editor.
 *  - `EditorDeCategoria` — o que abre **na própria linha**, como linha de
 *    detalhe da `DataTable`. Não é popover nem diálogo: uma camada flutuante
 *    sobre a tabela não é a linha, e o *light dismiss* jogaria fora uma escolha
 *    pela metade num clique distraído. Clique fora não fecha; `Escape`,
 *    `Cancelar` e o confirmar fecham.
 *
 *  Duas saídas, **um** botão de confirmar: a ficha pressionada É a escolha da
 *  segunda saída, e o rótulo do confirmar diz o que vai acontecer
 *  (`Categorizar` / `Categorizar e reconhecer por «mercado»`). Dois cliques
 *  para o caminho comum, três para o que altera outras linhas do mês. */

/** `true` para a linha que tem a lacuna: receita ou despesa sem categoria.
 *  Transferência não tem categoria por desenho; linha categorizada mostra o
 *  nome (recategorizar é E2b). */
export function podeCategorizar(linha: Transaction): boolean {
  return !ehTransferencia(linha.kind) && linha.categoryId === null
}

export function idDoEditorDeCategoria(id: string): string {
  return `editor-categoria-${id}`
}

/** Instância do botão: a da coluna Categoria (≥ 40rem) ou a da linha
 *  secundária da descrição (< 40rem). São duas no DOM e exatamente uma visível
 *  em qualquer largura — o mesmo mecanismo que já duplica o nome da conta. */
export type InstanciaDoAtalho = 'coluna' | 'secundaria'

type BotaoProps = {
  linha: Transaction
  aberto: boolean
  instancia: InstanciaDoAtalho
  onToggle: () => void
}

export function BotaoSemCategoria({ linha, aberto, instancia, onToggle }: BotaoProps) {
  return (
    <button
      type="button"
      id={`atalho-categoria-${linha.id}-${instancia}`}
      className={styles.lacuna}
      data-atalho={linha.id}
      aria-expanded={aberto}
      // Só quando aberto: o editor não existe no DOM fechado, e um
      // `aria-controls` para um id que não existe é uma promessa quebrada.
      aria-controls={aberto ? idDoEditorDeCategoria(linha.id) : undefined}
      // O texto visível está contido no nome (WCAG 2.5.3) e o nome diz de qual
      // linha é: dezoito botões "Sem categoria" iguais não dizem nada a quem
      // navega por lista de controles.
      aria-label={`Sem categoria. Categorizar ${rotuloDaLinha(linha)}`}
      onClick={onToggle}
    >
      Sem categoria
      <span className={styles.chevron}>
        <ChevronDownIcon size={14} />
      </span>
    </button>
  )
}

// ----------------------------------------------------------------- foco

/** As lacunas visíveis da tabela, em ordem do DOM, uma por linha.
 *
 *  Cada linha tem duas instâncias do botão (coluna e secundária); a visível é
 *  a que `checkVisibility()` aprova. Onde a API não existe (jsdom), a primeira
 *  do DOM responde pela linha. */
function lacunasVisiveis(): Map<string, HTMLButtonElement> {
  const mapa = new Map<string, HTMLButtonElement>()
  for (const botao of document.querySelectorAll<HTMLButtonElement>('button[data-atalho]')) {
    const id = botao.dataset.atalho
    if (!id || mapa.has(id) || !estaVisivel(botao)) continue
    mapa.set(id, botao)
  }
  return mapa
}

function estaVisivel(elemento: HTMLElement): boolean {
  return typeof elemento.checkVisibility === 'function' ? elemento.checkVisibility() : true
}

/** Os ids das lacunas visíveis agora, na ordem da tabela — a foto que o
 *  confirmar tira ANTES de gravar, para saber qual era "a próxima". */
export function idsDasLacunas(): string[] {
  return [...lacunasVisiveis().keys()]
}

/** Devolve o foco ao botão da célula da linha `id` — a instância visível. */
export function focarLacuna(id: string): boolean {
  const botao = lacunasVisiveis().get(id)
  if (!botao) return false
  botao.focus()
  return true
}

/** A próxima lacuna depois de resolver a linha `idResolvido`: a seguinte na
 *  ordem em que a tabela estava; não havendo, a anterior mais próxima; não
 *  havendo, qualquer uma que tenha sobrado (linhas que chegaram depois). `null`
 *  quando não há mais lacuna — a tela decide entre `Carregar mais` e o `<h1>`.
 *
 *  Compara com a foto de antes, e não com a posição no DOM, porque a linha
 *  resolvida pode já ter saído da lista (filtro `?semCategoria=1`) e as que o
 *  reprocessamento do mês categorizou já perderam a lacuna. */
export function proximaLacuna(
  idResolvido: string,
  antes: readonly string[],
): HTMLButtonElement | null {
  const agora = lacunasVisiveis()
  const indice = antes.indexOf(idResolvido)
  for (const id of antes.slice(indice + 1)) {
    const botao = agora.get(id)
    if (botao && id !== idResolvido) return botao
  }
  if (indice > 0) {
    for (const id of antes.slice(0, indice).reverse()) {
      const botao = agora.get(id)
      if (botao) return botao
    }
  }
  for (const [id, botao] of agora) {
    if (id !== idResolvido) return botao
  }
  return null
}

// --------------------------------------------------------------- editor

export type GravacaoConcluida = {
  /** A linha que acabou de ser categorizada. */
  id: string
  /** As lacunas visíveis no momento do confirmar, na ordem da tabela. */
  lacunasAntes: readonly string[]
}

type EditorProps = {
  linha: Transaction
  /** `AAAA-MM` — o mês da tela, que é o mês do reprocessamento. */
  mes: string
  /** Cancelar/Escape: a tela fecha o editor. O foco já foi devolvido ao botão
   *  da célula antes desta chamada. */
  onCancelar: () => void
  /** Gravou. Na saída "só este", chamado assim que o cache recebeu a linha
   *  (a célula muda no mesmo frame); na saída com palavra, depois que a lista
   *  refetchou — as linhas que o reprocessamento categorizou já perderam a
   *  lacuna, e é sobre esse estado que a tela escolhe a próxima. */
  onGravado: (gravacao: GravacaoConcluida) => void
}

type Pedido = {
  categoria: Category
  /** A ficha pressionada, ou `null` na saída "só este". */
  palavra: string | null
  /** As lacunas visíveis no clique, na ordem da tabela. */
  lacunasAntes: readonly string[]
}

/** O que o confirmar produziu. As falhas parciais da sequência com palavra
 *  são RESULTADO, não exceção: cada uma tem uma consequência diferente na
 *  tela (spec 0005 §11.3 e docs/DESIGN.md (h) §4), e `onError` só recebe o
 *  que não tem tratamento próprio. */
type Desfecho =
  | { tipo: 'so-este'; atualizado: Transaction }
  | { tipo: 'com-palavra'; palavra: string; atualizado: Transaction; categorizados: number }
  /** 409 em (a): a palavra já é de outra categoria. Nada mais é feito. */
  | { tipo: 'conflito'; palavra: string; donaId: string | undefined }
  /** (a) gravou, (b) falhou: a palavra já é da categoria; a linha não. */
  | { tipo: 'falha-b'; palavra: string }
  /** (a) e (b) gravaram, (c) falhou: a linha está resolvida; o mês não. */
  | { tipo: 'falha-c'; palavra: string; atualizado: Transaction }

export function EditorDeCategoria({ linha, mes, onCancelar, onGravado }: EditorProps) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const toast = useToast()
  const raiz = useRef<HTMLFieldSetElement>(null)
  const selectRef = useRef<HTMLSelectElement>(null)

  const categorias = useQuery(categoriasQueryOptions(false))

  const [categoriaId, setCategoriaId] = useState('')
  /** A ficha pressionada — no máximo uma. */
  const [palavra, setPalavra] = useState<string | null>(null)
  /** Palavras que levaram 409: saem da linha, oferecê-las de novo seria
   *  oferecer o mesmo 409. */
  const [recusadas, setRecusadas] = useState<readonly string[]>([])
  /** Palavras que (a) gravou e (b) não acompanhou: viram texto estático com o
   *  ícone de feito — a palavra já é da categoria. */
  const [gravadas, setGravadas] = useState<readonly string[]>([])
  /** O 422 da §13 mora JUNTO DO SELETOR, não num toast: o que ele pede é uma
   *  escolha nova no mesmo campo, e o toast some de onde a ação acontece. */
  const [erroDaCategoria, setErroDaCategoria] = useState<string | undefined>(undefined)

  const nomeDoMesAtual = nomeDoMes(mes)
  const rotulo = rotuloDaLinha(linha)
  // O editor só abre em receita/despesa (`podeCategorizar`). O seletor é do
  // LADO DO DINHEIRO da linha, não de uma natureza só (ADR-029b): uma despesa
  // escolhe entre categorias de despesa E de investimento, e nunca vê "Salário"
  // — oferecer o outro lado é oferecer um erro (e um 422).
  const lado: LadoDoDinheiro = linha.kind === 'income' ? 'income' : 'expense'
  const opcoes = opcoesDeCategoria(categorias.data, lado)
  const categoria = categoriaPorId(categorias.data, categoriaId)

  const semCategorias = categorias.isSuccess && opcoes.length === 0
  const semSelect = categorias.isError || semCategorias

  // Recebe o foco ao abrir: o `<select>` quando existe (inclusive carregando —
  // ele existe e não pula), senão o primeiro controle que houver. Roda de novo
  // se o select sair do DOM depois (erro ou nenhuma categoria): o foco não
  // pode cair no <body>.
  useEffect(() => {
    if (raiz.current?.contains(document.activeElement)) return
    const alvo = semSelect ? raiz.current?.querySelector<HTMLElement>('button') : selectRef.current
    alvo?.focus()
  }, [semSelect])

  // O `Button` do sistema não expõe `ref`; o confirmar é achado pelo atributo.
  function focarConfirmar() {
    raiz.current?.querySelector<HTMLElement>('[data-acao="confirmar"]')?.focus()
  }

  const gravar = useMutation({
    mutationFn: async (pedido: Pedido): Promise<Desfecho> => {
      const { palavra: escolhida } = pedido
      if (escolhida === null) {
        const atualizado = await updateTransactionCategory(linha.id, {
          categoryId: pedido.categoria.id,
        })
        return { tipo: 'so-este', atualizado }
      }

      // (a) A lista mais recente é a do cache no momento do clique, não a da
      // renderização: o PATCH substitui a lista inteira, e mandar só a nova
      // apagaria as outras.
      const arvore = queryClient.getQueryData<CategoryTree>(categoriasQueryKey(false))
      const atual = categoriaPorId(arvore, pedido.categoria.id) ?? pedido.categoria
      try {
        await updateCategory(pedido.categoria.id, { keywords: [...atual.keywords, escolhida] })
      } catch (erro) {
        const conflito = keywordTakenOf(erro)
        if (conflito) return { tipo: 'conflito', palavra: escolhida, donaId: conflito.ownerId }
        throw erro
      }
      void queryClient.invalidateQueries({ queryKey: ['categories'] })

      // (b)
      let atualizado: Transaction
      try {
        atualizado = await updateTransactionCategory(linha.id, { categoryId: pedido.categoria.id })
      } catch (erro) {
        // O 422 da §13 não é "falha-b": ali a orientação seria "tente de
        // novo", e tentar de novo na MESMA categoria falha de novo. Ele sobe
        // para o tratamento do editor, que limpa a escolha e recarrega as
        // opções.
        if (isCategoriaRecusada(erro)) throw erro
        return { tipo: 'falha-b', palavra: escolhida }
      }

      // (c) No mês da tela; o servidor recalcula e só escreve onde ainda não
      // há categoria — o número que volta é o dos OUTROS (este já foi por (b)).
      try {
        const resultado = await autoCategorize({ month: mes, dryRun: false })
        return {
          tipo: 'com-palavra',
          palavra: escolhida,
          atualizado,
          categorizados: resultado.categorized,
        }
      } catch {
        return { tipo: 'falha-c', palavra: escolhida, atualizado }
      }
    },
    onSuccess: async (desfecho, pedido) => {
      const nome = pedido.categoria.name
      const { lacunasAntes } = pedido
      switch (desfecho.tipo) {
        case 'so-este': {
          // A célula muda no mesmo frame; o `uncategorizedCount` da faixa vem
          // do servidor, nunca de conta local — daí a invalidação logo atrás.
          receberLinha(queryClient, desfecho.atualizado)
          toast.sucesso(`Lançamento categorizado como ${nome}.`)
          onGravado({ id: linha.id, lacunasAntes })
          await queryClient.invalidateQueries({ queryKey: ['transactions'] })
          return
        }
        case 'com-palavra': {
          toast.sucesso(
            `${citarPalavra(desfecho.palavra)} adicionada a ${nome} · ${fraseDosOutros(desfecho.categorizados, nomeDoMesAtual)}`,
          )
          // Uma invalidação só, depois de (c): a próxima lacuna é escolhida
          // sobre a lista já refetchada.
          await queryClient.invalidateQueries({ queryKey: ['transactions'] })
          onGravado({ id: linha.id, lacunasAntes })
          return
        }
        case 'conflito': {
          const arvore = queryClient.getQueryData<CategoryTree>(categoriasQueryKey(false))
          const dona = categoriaPorId(arvore, desfecho.donaId)
          toast.erro(
            dona
              ? `${citarPalavra(desfecho.palavra)} já está em ${dona.name}.`
              : `${citarPalavra(desfecho.palavra)} já está em outra categoria desta casa.`,
          )
          // Nada mais é feito: a ficha sai da linha, o rótulo volta a
          // `Categorizar` e a pessoa decide.
          setRecusadas((lista) => [...lista, desfecho.palavra])
          setPalavra(null)
          focarConfirmar()
          return
        }
        case 'falha-b': {
          toast.erro(
            `${citarPalavra(desfecho.palavra)} adicionada a ${nome}, mas o lançamento não foi categorizado. Tente de novo.`,
          )
          setGravadas((lista) => [...lista, desfecho.palavra])
          setPalavra(null)
          focarConfirmar()
          return
        }
        case 'falha-c': {
          receberLinha(queryClient, desfecho.atualizado)
          toast.erro(
            `${citarPalavra(desfecho.palavra)} adicionada e lançamento categorizado, mas os outros do mês não foram — use Categorizar automaticamente na faixa.`,
          )
          onGravado({ id: linha.id, lacunasAntes })
          await queryClient.invalidateQueries({ queryKey: ['transactions'] })
          return
        }
      }
    },
    onError: (erro) => {
      // 422 em `fields.categoryId` (spec 0005 §13): a categoria escolhida é um
      // grupo que ganhou subcategoria enquanto o editor estava aberto — o
      // seletor a ofereceu porque a árvore em cache é de antes. Nada foi
      // gravado, e a saída não é "tente de novo": é escolher outra. Por isso o
      // editor FICA aberto, a mensagem nasce junto do `<select>` (que recebe o
      // foco e a lê pelo `aria-describedby`), a escolha é limpa e as opções
      // são recarregadas — o grupo some da lista. Sem toast: seria a mesma
      // frase longe do campo que precisa ser usado de novo.
      if (isCategoriaRecusada(erro)) {
        setErroDaCategoria(messageForError(erro))
        setCategoriaId('')
        setPalavra(null)
        setGravadas([])
        selectRef.current?.focus()
        void queryClient.invalidateQueries({ queryKey: ['categories'] })
        return
      }
      toast.erro(messageForError(erro))
      focarConfirmar()
    },
  })

  function cancelar() {
    // O foco volta ANTES de a tela desmontar o editor: assim ele nunca cai no
    // `<body>` no meio do caminho.
    focarLacuna(linha.id)
    onCancelar()
  }

  function aoTeclar(evento: KeyboardEvent<HTMLFieldSetElement>) {
    // `Escape` com a lista nativa do `<select>` aberta é do navegador — ele
    // fecha só a lista e o evento não chega aqui.
    if (evento.key !== 'Escape' || evento.defaultPrevented) return
    evento.preventDefault()
    evento.stopPropagation()
    cancelar()
  }

  function confirmar() {
    if (gravar.isPending) return
    if (!categoria) {
      selectRef.current?.focus()
      return
    }
    // A foto das lacunas é tirada AGORA, antes de qualquer gravação: é sobre
    // ela que a tela escolhe a próxima depois.
    gravar.mutate({ categoria, palavra, lacunasAntes: idsDasLacunas() })
  }

  function trocarCategoria(id: string) {
    setCategoriaId(id)
    // A recusa era da categoria anterior: a nova escolha apaga a mensagem.
    setErroDaCategoria(undefined)
    // Trocar a categoria solta a ficha e zera o que era da outra: a palavra
    // pode já ser dela.
    setPalavra(null)
    setGravadas([])
  }

  // ------------------------------------------------------------ fichas

  const fichas = categoria
    ? palavrasParaAprender(
        linha.description,
        categoria,
        // A pressionada entra como "aprendida" para a ficha não sumir no meio
        // da sequência: depois de (a) a categoria já a tem, e a lista sem esta
        // exceção a descartaria enquanto (b) e (c) ainda rodam.
        palavra ? [...gravadas, palavra] : gravadas,
      ).filter((token) => !recusadas.includes(token))
    : []
  const lotada = categoria !== undefined && categoria.keywords.length >= MAX_PALAVRAS_CHAVE
  const dica =
    categoria && palavra
      ? `${citarPalavra(palavra)} vira palavra-chave de ${categoria.name} — vale para os outros lançamentos sem categoria de ${nomeDoMesAtual} e para as próximas importações.`
      : ''

  const rotuloDoConfirmar = !categoria
    ? 'Escolha uma categoria'
    : palavra
      ? `Categorizar e reconhecer por ${citarPalavra(palavra)}`
      : 'Categorizar'

  return (
    <fieldset
      ref={raiz}
      id={idDoEditorDeCategoria(linha.id)}
      className={styles.editor}
      onKeyDown={aoTeclar}
    >
      <legend className="sr-only">Categorizar {rotulo}</legend>

      {categorias.isError ? (
        <Alert
          tone="error"
          action={
            <Button
              size="sm"
              onClick={() => void categorias.refetch()}
              loading={categorias.isFetching}
            >
              Tentar de novo
            </Button>
          }
        >
          Não foi possível carregar as categorias.
        </Alert>
      ) : semCategorias ? (
        <p className={styles.vazio}>
          {/* A frase nomeia as DUAS naturezas do lado do dinheiro: o seletor
              oferece as duas, e ela só aparece quando as duas estão vazias. */}
          {lado === 'income'
            ? 'Nenhuma categoria de receita ou de resgate ainda.'
            : 'Nenhuma categoria de despesa ou de investimento ainda.'}{' '}
          <Button
            variant="secondary"
            size="sm"
            onClick={() =>
              void navigate({
                to: '/categorias',
                search: (anterior) => (anterior.mes ? { mes: anterior.mes } : {}),
              })
            }
          >
            Ir para categorias
          </Button>
        </p>
      ) : (
        <Select
          ref={selectRef}
          density="compact"
          labelHidden
          label="Categoria"
          aria-label={`Categoria de ${rotulo}`}
          placeholder={categorias.isPending ? 'Carregando categorias…' : 'Escolha a categoria'}
          options={opcoes}
          value={categoriaId}
          error={erroDaCategoria}
          onChange={(evento) => trocarCategoria(evento.target.value)}
        />
      )}

      {categoria && lotada ? (
        <p className={styles.lotada}>
          {categoria.name} já tem {MAX_PALAVRAS_CHAVE} palavras-chave. Remova uma em Categorias para
          incluir outra.
        </p>
      ) : null}

      {categoria && fichas.length > 0 ? (
        <div className={styles.fichas}>
          <span className={styles.rotulo}>Da próxima vez, reconhecer por</span>
          {fichas.map((token) =>
            gravadas.includes(token) ? (
              <span key={token} className={styles.gravada}>
                <CheckIcon size={14} />
                {token}
              </span>
            ) : (
              <Button
                key={token}
                variant="quiet"
                size="sm"
                aria-pressed={palavra === token}
                iconStart={palavra === token ? <CheckIcon size={14} /> : <PlusIcon size={14} />}
                // O nome não promete "adicionar": ainda não gravou.
                aria-label={`Reconhecer por ${citarPalavra(token)}`}
                onClick={() => {
                  if (gravar.isPending) return
                  setPalavra((atual) => (atual === token ? null : token))
                }}
              >
                {token}
              </Button>
            ),
          )}
        </div>
      ) : null}

      {/* A ÚNICA live region do editor: quem está na ficha não ouve o rótulo
          do botão mudar; ouve isto. Vazia, fica no DOM (como `sr-only`) para a
          região existir antes de o texto chegar — senão o anúncio se perde. */}
      <p className={dica ? styles.dica : 'sr-only'} role="status">
        {dica}
      </p>

      {semCategorias ? (
        <div className={styles.acoes}>
          <Button variant="quiet" size="sm" onClick={cancelar}>
            Cancelar
          </Button>
        </div>
      ) : (
        <div className={styles.acoes}>
          {/* Nunca `disabled`: o botão continua focável e o rótulo explica o
              que falta; o clique leva ao `<select>`. */}
          <Button
            data-acao="confirmar"
            variant="primary"
            size="sm"
            loading={gravar.isPending}
            aria-disabled={categoria ? undefined : true}
            onClick={confirmar}
          >
            {rotuloDoConfirmar}
          </Button>
          <Button variant="quiet" size="sm" onClick={cancelar}>
            Cancelar
          </Button>
        </div>
      )}
    </fieldset>
  )
}

// -------------------------------------------------------------- cache

/** Põe o `Transaction` devolvido pelo `PATCH` em TODAS as listas em cache
 *  (qualquer mês/conta): a célula muda no mesmo frame, antes do refetch. */
function receberLinha(
  queryClient: ReturnType<typeof useQueryClient>,
  atualizado: Transaction,
): void {
  queryClient.setQueriesData<InfiniteData<TransactionList>>(
    { queryKey: ['transactions'] },
    (dados) => {
      if (!dados) return dados
      return {
        ...dados,
        pages: dados.pages.map((pagina) => ({
          ...pagina,
          items: pagina.items.map((item) => (item.id === atualizado.id ? atualizado : item)),
        })),
      }
    },
  )
}

// ---------------------------------------------------------------- copy

/** O trecho do toast com o número REAL de (c) — só os outros lançamentos. */
export function fraseDosOutros(categorizados: number, mes: string): string {
  if (categorizados === 0) return `nenhum outro lançamento de ${mes} categorizado.`
  if (categorizados === 1) return `mais 1 lançamento de ${mes} categorizado.`
  return `mais ${categorizados} lançamentos de ${mes} categorizados.`
}
