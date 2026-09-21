import { type InfiniteData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { type KeyboardEvent, useEffect, useRef, useState } from 'react'
import type { Category, CategoryTree, Transaction, TransactionList } from '@/api/types'
import type { TipoDeLancamento } from '@/app/search'
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
import {
  autoCategorize,
  transactionsQueryKey,
  updateTransactionCategory,
} from '../api/transactions'
import { fraseDaLinhaQueSaiu, rotuloDaLinha } from '../lexico'
import styles from './AtalhoDeCategoria.module.css'

/** Atalho de categorização em `/lancamentos` (spec 0005 §11 e §19;
 *  docs/DESIGN.md, E2c (h)).
 *
 *  Dois pedaços, ligados pela tela através do `editandoId`:
 *
 *  - `BotaoDeCategoria` — o controle da célula, com **dois estados fechados e
 *    um aberto**. Fechado, a **lacuna** (texto `Sem categoria` tracejado, peso
 *    400) e a linha **categorizada** (o nome na tinta do lugar) diferem: uma é
 *    pendência, a outra é dado. Aberto, são idênticos — a pessoa está editando
 *    a categoria da linha, e isso é uma coisa só. Padrão APG *disclosure*:
 *    `aria-expanded` e, só quando aberto, `aria-controls` para o editor.
 *  - `EditorDeCategoria` — o que abre **na própria linha**, como linha de
 *    detalhe da `DataTable`. Não é popover nem diálogo: uma camada flutuante
 *    sobre a tabela não é a linha, e o *light dismiss* jogaria fora uma escolha
 *    pela metade num clique distraído. Clique fora não fecha; `Escape`,
 *    `Cancelar` e o confirmar fecham.
 *
 *  Duas saídas, **um** botão de confirmar: a ficha pressionada É a escolha da
 *  segunda saída, e o rótulo do confirmar diz o que vai acontecer
 *  (`Categorizar` / `Trocar categoria` / `… e reconhecer por «mercado»`). Dois
 *  cliques para o caminho comum, três para o que altera outras linhas do mês.
 *
 *  **Modo trocar** (emenda §19 da spec 0005, 18/09/2026 — pedido do usuário:
 *  "a qualquer momento eu manualmente alterar um lançamento de categoria"): a
 *  linha já categorizada abre o MESMO editor, com o `<select>` já na categoria
 *  atual. O backend não muda — `PATCH /transactions/{id}` com `categoryId`
 *  substitui, não só preenche. */

/** `true` para a linha que tem controle de categoria na célula — ou seja,
 *  qualquer uma que não seja perna de transferência (transferência não tem
 *  categoria por desenho, ADR-016).
 *
 *  Desde a emenda §19 isto **não** é mais "está sem categoria": a linha
 *  categorizada tem o outro estado fechado do mesmo controle, e é por ele que
 *  a pessoa troca a categoria a qualquer momento. */
export function podeEditarCategoria(linha: Transaction): boolean {
  return !ehTransferencia(linha.kind)
}

/** `true` só na **lacuna**: receita ou despesa ainda sem categoria — a dívida
 *  que a faixa de pendência conta e que o foco do modo categorizar persegue de
 *  linha em linha. */
export function ehLacuna(linha: Transaction): boolean {
  return podeEditarCategoria(linha) && linha.categoryId === null
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

export function BotaoDeCategoria({ linha, aberto, instancia, onToggle }: BotaoProps) {
  const lacuna = ehLacuna(linha)
  // O nome ausente com `categoryId` presente é o caso que o contrato permite e
  // o servidor não produz. A tela escolhe a mesma palavra que a linha
  // secundária já usava nesse buraco, em vez de um botão sem nome acessível.
  const texto = lacuna ? 'Sem categoria' : (linha.categoryName ?? 'Sem categoria')

  return (
    <button
      type="button"
      id={`atalho-categoria-${linha.id}-${instancia}`}
      className={`${styles.atalho} ${lacuna ? styles.lacuna : styles.categorizada}`}
      // Nos DOIS estados: é por ele que o foco volta à linha, em qualquer
      // estado, e é ele que a foto do modo trocar fotografa.
      data-atalho={linha.id}
      data-instancia={instancia}
      // SÓ na lacuna: é por ele que "a próxima lacuna" é procurada. Usar
      // `data-atalho` ali trataria a linha categorizada vizinha como pendência.
      data-lacuna={lacuna ? '' : undefined}
      aria-expanded={aberto}
      // Só quando aberto: o editor não existe no DOM fechado, e um
      // `aria-controls` para um id que não existe é uma promessa quebrada.
      aria-controls={aberto ? idDoEditorDeCategoria(linha.id) : undefined}
      // O texto visível está contido no nome (WCAG 2.5.3) e o nome diz de qual
      // linha é: dezoito botões "Sem categoria" iguais não dizem nada a quem
      // navega por lista de controles.
      aria-label={`${texto}. ${lacuna ? 'Categorizar' : 'Trocar categoria de'} ${rotuloDaLinha(linha)}`}
      onClick={onToggle}
    >
      {texto}
      <span className={styles.chevron}>
        <ChevronDownIcon size={14} />
      </span>
    </button>
  )
}

// ----------------------------------------------------------------- foco

/** Os controles de categoria visíveis da tabela, em ordem do DOM, um por
 *  linha — `seletor` escolhe QUAIS.
 *
 *  Cada linha tem duas instâncias do botão (coluna e secundária); a visível é
 *  a que `checkVisibility()` aprova. Onde a API não existe (jsdom), a primeira
 *  do DOM responde pela linha. */
function visiveisPor(seletor: string): Map<string, HTMLButtonElement> {
  const mapa = new Map<string, HTMLButtonElement>()
  for (const botao of document.querySelectorAll<HTMLButtonElement>(seletor)) {
    const id = botao.dataset.atalho
    if (!id || mapa.has(id) || !estaVisivel(botao)) continue
    mapa.set(id, botao)
  }
  return mapa
}

/** Só as lacunas — `[data-lacuna]`, nunca `[data-atalho]`, que desde a emenda
 *  §19 também está nas linhas categorizadas. Sem esta distinção, a linha
 *  categorizada vizinha seria tratada como "a próxima pendência". */
function lacunasVisiveis(): Map<string, HTMLButtonElement> {
  return visiveisPor('button[data-lacuna]')
}

/** Todo controle de categoria, em qualquer estado — a foto do modo trocar. */
function atalhosVisiveis(): Map<string, HTMLButtonElement> {
  return visiveisPor('button[data-atalho]')
}

function estaVisivel(elemento: HTMLElement): boolean {
  return typeof elemento.checkVisibility === 'function' ? elemento.checkVisibility() : true
}

/** Os ids das lacunas visíveis agora, na ordem da tabela — a foto que o
 *  confirmar tira ANTES de gravar, para saber qual era "a próxima". */
export function idsDasLacunas(): string[] {
  return [...lacunasVisiveis().keys()]
}

/** Os ids de TODOS os controles de categoria visíveis agora, na ordem da
 *  tabela. A foto do modo trocar: quando a linha sai da lista por contradizer
 *  o filtro `?tipo=`, é sobre ela que o vizinho é escolhido. Linha de
 *  transferência não está na foto (não tem controle) e é pulada naturalmente. */
export function idsDosAtalhos(): string[] {
  return [...atalhosVisiveis().keys()]
}

/** Devolve o foco ao controle de categoria da linha `id` — a instância
 *  visível, em **qualquer** estado. É por isso que a busca é por
 *  `[data-atalho]`: depois de trocar, o botão que estava ali continua ali, só
 *  que com o nome novo. */
export function focarAtalho(id: string): boolean {
  const botao = atalhosVisiveis().get(id)
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

/** O vizinho de `idResolvido` entre os controles de categoria, para quando a
 *  linha **saiu da lista** depois de trocar de categoria (filtro `?tipo=`):
 *  o seguinte na foto de antes; não havendo, o anterior mais próximo. `null`
 *  quando não sobrou nenhum — a tela decide entre `Carregar mais` e o `<h1>`.
 *
 *  **Sem** o terceiro passo de `proximaLacuna` ("qualquer uma que sobrou"):
 *  ali a pessoa está varrendo pendências e qualquer lacuna é trabalho; aqui
 *  ela trabalhava NESTA linha, e pular para uma linha aleatória da tabela não
 *  é devolver o foco. */
export function proximoAtalho(
  idResolvido: string,
  antes: readonly string[],
): HTMLButtonElement | null {
  const agora = atalhosVisiveis()
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
  return null
}

// --------------------------------------------------------------- editor

export type GravacaoConcluida = {
  /** A linha que acabou de ser categorizada. */
  id: string
  /** `true` quando a linha estava **sem categoria**: é o que decide para onde
   *  o foco vai. Quem paga uma pendência está varrendo a lista, e a próxima
   *  lacuna é o próximo trabalho; quem troca uma categoria está trabalhando
   *  NESTA linha, e o foco volta para ela (emenda §19). */
  eraLacuna: boolean
  /** As lacunas visíveis no momento do confirmar, na ordem da tabela. */
  lacunasAntes: readonly string[]
  /** Os controles de categoria visíveis no momento do confirmar, em qualquer
   *  estado e na ordem da tabela — a foto do modo trocar. */
  atalhosAntes: readonly string[]
}

type EditorProps = {
  linha: Transaction
  /** `AAAA-MM` — o mês da tela, que é o mês do reprocessamento. */
  mes: string
  /** O filtro de tipo ativo na tela (E2d).
   *
   *  Serve a UMA coisa: a segunda frase do toast, quando a categoria escolhida
   *  tira a linha da lista. Com `tipo=despesas`, categorizar como investimento
   *  faz a linha sumir na hora — e sumir em silêncio ensinaria, pelo susto, uma
   *  regra que a pessoa não viu. */
  tipo?: TipoDeLancamento | undefined
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
  /** O nome da categoria de ONDE a linha saiu, fotografado no clique — é o
   *  `de Transporte` do toast. `null` quando a linha não tinha categoria (modo
   *  categorizar) ou quando o servidor mandou `categoryId` sem `categoryName`.
   *  Fotografado, e não lido no `onSuccess`, porque lá o cache já recebeu a
   *  linha nova: ler dali diria "de Lazer para Lazer". */
  nomeAnterior: string | null
  /** As lacunas visíveis no clique, na ordem da tabela. */
  lacunasAntes: readonly string[]
  /** Todos os controles de categoria visíveis no clique, na ordem da tabela. */
  atalhosAntes: readonly string[]
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

export function EditorDeCategoria({ linha, mes, tipo, onCancelar, onGravado }: EditorProps) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const toast = useToast()
  const raiz = useRef<HTMLFieldSetElement>(null)
  const selectRef = useRef<HTMLSelectElement>(null)

  const categorias = useQuery(categoriasQueryOptions(false))

  /** A categoria escolhida NO CAMPO, ou `null` enquanto a pessoa não mexeu —
   *  e é essa distinção que o modo trocar precisa: `null` cai na pré-seleção
   *  (a categoria atual, quando ela ainda é escolhível), `''` é o placeholder
   *  escolhido de propósito, que é onde o 422 deixa o campo. */
  const [escolha, setEscolha] = useState<string | null>(null)
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

  // Modo TROCAR (emenda §19): a linha já tem categoria. Um editor só, dois
  // modos — o que muda é a legenda, o `<select>` já na categoria atual, o
  // verbo do confirmar e o toast.
  const trocando = !ehLacuna(linha)
  const nomeDoMesAtual = nomeDoMes(mes)
  const rotulo = rotuloDaLinha(linha)
  // O editor só abre em receita/despesa (`podeEditarCategoria`). O seletor é do
  // LADO DO DINHEIRO da linha, não de uma natureza só (ADR-029b): uma despesa
  // escolhe entre categorias de despesa E de investimento, e nunca vê "Salário"
  // — oferecer o outro lado é oferecer um erro (e um 422).
  const lado: LadoDoDinheiro = linha.kind === 'income' ? 'income' : 'expense'
  const opcoes = opcoesDeCategoria(categorias.data, lado)

  // O `<select>` abre JÁ na categoria atual — a pessoa vê de onde está saindo
  // antes de escolher para onde vai, e o `Escape` não tem o que desfazer. Só
  // se ela ainda for escolhível: a árvore não traz arquivadas, e um grupo que
  // ganhou subcategorias deixou de receber lançamento (§13). Enquanto as
  // categorias carregam a lista está vazia, e o campo fica no placeholder —
  // não é defeito, é o estado real.
  const atualEscolhivel =
    trocando &&
    linha.categoryId !== null &&
    opcoes.some((opcao) => opcao.value === linha.categoryId)
  const categoriaId = escolha ?? (atualEscolhivel ? (linha.categoryId ?? '') : '')
  const categoria = categoriaPorId(categorias.data, categoriaId)

  const semCategorias = categorias.isSuccess && opcoes.length === 0
  const semSelect = categorias.isError || semCategorias

  const escolhaEhAtual = trocando && categoriaId !== '' && categoriaId === linha.categoryId

  // A categoria atual que o `<select>` NÃO pode oferecer de novo, e o porquê —
  // a nota sob o campo. Sem nome resolvível não há frase: nomear a categoria é
  // o que faz a nota explicar alguma coisa.
  const notaDaAtual = notaDaCategoriaAtual(linha, categorias.data, atualEscolhivel, trocando)
  // A nota descreve o CAMPO, não a linha: sem este id ela existiria só para
  // quem enxerga — o `<select>` anunciaria rótulo e placeholder, e nada
  // explicaria por que a categoria atual não está na lista (docs/DESIGN.md (h)
  // §2). O `Select` soma este id ao slot de mensagem dele.
  const notaId = `nota-categoria-${linha.id}`

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
      const concluida: GravacaoConcluida = {
        id: linha.id,
        eraLacuna: !trocando,
        lacunasAntes: pedido.lacunasAntes,
        atalhosAntes: pedido.atalhosAntes,
      }
      switch (desfecho.tipo) {
        case 'so-este': {
          // A célula muda no mesmo frame; o `uncategorizedCount` da faixa vem
          // do servidor, nunca de conta local — daí a invalidação logo atrás.
          receberLinha(queryClient, desfecho.atualizado)
          // O toast continua DE SUCESSO: nada falhou, ela fez o que quis. A
          // segunda frase só existe quando a natureza escolhida contradiz o
          // filtro ativo — em Tudo, e com categoria do mesmo lado, ele é o de
          // hoje, sem acréscimo.
          toast.sucesso(
            `${trocando ? fraseDaTroca(pedido.nomeAnterior, nome) : `Lançamento categorizado como ${nome}`}.${fraseDaLinhaQueSaiu(tipo, pedido.categoria.kind, 'Ele')}`,
          )
          onGravado(concluida)
          await queryClient.invalidateQueries({ queryKey: ['transactions'] })
          return
        }
        case 'com-palavra': {
          // Em modo trocar a troca DESTA linha vem primeiro: é o que a pessoa
          // fez, e é o que a palavra não implica (em `categorizar`, "«mercado»
          // adicionada a Alimentação" já implica esta linha; aqui não).
          const daLinha = trocando ? `${fraseDaTroca(pedido.nomeAnterior, nome)} · ` : ''
          // A frase da linha que saiu entra DEPOIS do texto de hoje: o sujeito
          // muda para "Este lançamento" porque a frase anterior já falou de
          // outros lançamentos do mês.
          toast.sucesso(
            `${daLinha}${citarPalavra(desfecho.palavra)} adicionada a ${nome} · ${fraseDosOutros(desfecho.categorizados, nomeDoMesAtual)}${fraseDaLinhaQueSaiu(tipo, pedido.categoria.kind, 'Este lançamento')}`,
          )
          // Uma invalidação só, depois de (c): o destino do foco é escolhido
          // sobre a lista já refetchada.
          await queryClient.invalidateQueries({ queryKey: ['transactions'] })
          onGravado(concluida)
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
          // Em modo trocar a frase diz ONDE a linha ficou — "continua em
          // Transporte" —, que é a informação que falta a quem acabou de
          // mandar tirá-la de lá.
          const ondeFicou = pedido.nomeAnterior
            ? `o lançamento continua em ${pedido.nomeAnterior}`
            : 'a categoria não foi trocada'
          toast.erro(
            `${citarPalavra(desfecho.palavra)} adicionada a ${nome}, mas ${trocando ? ondeFicou : 'o lançamento não foi categorizado'}. Tente de novo.`,
          )
          setGravadas((lista) => [...lista, desfecho.palavra])
          setPalavra(null)
          focarConfirmar()
          return
        }
        case 'falha-c': {
          receberLinha(queryClient, desfecho.atualizado)
          // A linha também sai da lista aqui — (b) gravou. Nenhuma linha some
          // em silêncio, nem no caminho de falha parcial.
          const oQueFoi = trocando
            ? `adicionada e categoria trocada para ${nome}, mas os outros do mês não foram categorizados`
            : 'adicionada e lançamento categorizado, mas os outros do mês não foram'
          toast.erro(
            `${citarPalavra(desfecho.palavra)} ${oQueFoi} — use Categorizar automaticamente na faixa.${fraseDaLinhaQueSaiu(tipo, pedido.categoria.kind, 'Este lançamento')}`,
          )
          onGravado(concluida)
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
        // Em modo trocar a escolha vai para o PLACEHOLDER, e não de volta à
        // categoria atual: a mensagem pede outra, e é isso que o placeholder
        // diz. `''` é escolha explícita — a pré-seleção não a desfaz.
        setEscolha('')
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
    // `<body>` no meio do caminho. Em qualquer modo é o controle da própria
    // linha — o `<select>` tinha a categoria atual, e nada mudou.
    focarAtalho(linha.id)
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
    // Sem escolha, ou com a categoria ATUAL escolhida: o clique leva ao campo
    // e **nenhuma requisição sai**. Não existe "trocar para a mesma" — o
    // servidor responderia 200 sem escrever, e um toast de troca que não
    // trocou nada seria mentira.
    if (!categoria || escolhaEhAtual) {
      selectRef.current?.focus()
      return
    }
    // As duas fotos são tiradas AGORA, antes de qualquer gravação: é sobre
    // elas que a tela escolhe o destino do foco depois.
    gravar.mutate({
      categoria,
      palavra,
      nomeAnterior: trocando ? linha.categoryName : null,
      lacunasAntes: idsDasLacunas(),
      atalhosAntes: idsDosAtalhos(),
    })
  }

  function trocarCategoria(id: string) {
    setEscolha(id)
    // A recusa era da categoria anterior: a nova escolha apaga a mensagem.
    setErroDaCategoria(undefined)
    // Trocar a categoria solta a ficha e zera o que era da outra: a palavra
    // pode já ser dela.
    setPalavra(null)
    setGravadas([])
  }

  // ------------------------------------------------------------ fichas

  // Só com escolha DIFERENTE da atual: com a atual selecionada não há troca, e
  // "ensinar a categoria de hoje a reconhecer esta descrição" é outra ação,
  // que já tem casa em Categorias e na revisão da importação — aqui viraria um
  // terceiro modo com um PATCH que não muda nada.
  const fichas =
    categoria && !escolhaEhAtual
      ? palavrasParaAprender(
          linha.description,
          categoria,
          // A pressionada entra como "aprendida" para a ficha não sumir no
          // meio da sequência: depois de (a) a categoria já a tem, e a lista
          // sem esta exceção a descartaria enquanto (b) e (c) ainda rodam.
          palavra ? [...gravadas, palavra] : gravadas,
        ).filter((token) => !recusadas.includes(token))
      : []
  const lotada =
    categoria !== undefined && !escolhaEhAtual && categoria.keywords.length >= MAX_PALAVRAS_CHAVE
  const dica =
    categoria && palavra
      ? `${citarPalavra(palavra)} vira palavra-chave de ${categoria.name} — vale para os outros lançamentos sem categoria de ${nomeDoMesAtual} e para as próximas importações.`
      : ''

  // Um confirmar só, e o rótulo diz a saída — inclusive quando a saída é
  // nenhuma. O verbo do modo trocar é **trocar**, do botão ao toast.
  const rotuloDoConfirmar = !categoria
    ? 'Escolha uma categoria'
    : escolhaEhAtual
      ? 'Escolha outra categoria'
      : palavra
        ? `${trocando ? 'Trocar' : 'Categorizar'} e reconhecer por ${citarPalavra(palavra)}`
        : trocando
          ? 'Trocar categoria'
          : 'Categorizar'
  const confirmarInerte = !categoria || escolhaEhAtual

  return (
    <fieldset
      ref={raiz}
      id={idDoEditorDeCategoria(linha.id)}
      className={styles.editor}
      onKeyDown={aoTeclar}
    >
      <legend className="sr-only">
        {trocando ? 'Trocar categoria de' : 'Categorizar'} {rotulo}
      </legend>

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
          aria-describedby={notaDaAtual ? notaId : undefined}
          onChange={(evento) => trocarCategoria(evento.target.value)}
        />
      )}

      {/* A categoria atual que não pode ser escolhida de novo — o campo abriu
          no placeholder e a nota diz por quê. Sem ela, o modo trocar abriria
          vazio sem explicação, e a pessoa concluiria que a linha perdeu a
          categoria. */}
      {notaDaAtual ? (
        <p id={notaId} className={styles.nota}>
          {notaDaAtual}
        </p>
      ) : null}

      {categoria && lotada ? (
        <p className={styles.nota}>
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
            aria-disabled={confirmarInerte ? true : undefined}
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

/** Põe o `Transaction` devolvido pelo `PATCH` em todas as listas de lançamento
 *  em cache (qualquer mês/conta/filtro): a célula muda no mesmo frame, antes do
 *  refetch.
 *
 *  O filtro discrimina a **identidade da query**, não a presença de `pages`.
 *  `['transactions']` é um PREFIXO, e sob ele moram QUATRO formas — três delas
 *  penduradas ali de propósito, para serem invalidadas junto (ADR-027):
 *
 *  1. `['transactions', {mes,contaId,tipo}]` — a lista de `/lancamentos`,
 *     `InfiniteData<TransactionList>`. **É o alvo, e o único.**
 *  2. `['transactions','dashboard',mes]` — o painel, um `DashboardSummary`.
 *  3. `['transactions','reports','by-category',…]` — o relatório por categoria.
 *  4. `['transactions','investments',mes]` — a visão de `/investimentos`
 *     (`features/investments/api/investments.ts`), que TAMBÉM é
 *     `InfiniteData<…>` e também tem `pages[].items[]`.
 *
 *  A quarta é a razão de este filtro existir. Enquanto só a linha SEM categoria
 *  era editável, ela nunca estava no cache de `/investimentos` (`InvestmentItem`
 *  exige `categoryId`); com a emenda §19, a linha já marcada como aporte ou
 *  resgate virou editável — e está lá. Um filtro por forma trocaria o
 *  `InvestmentItem` por um `Transaction`: o item perderia o `flow`, a coluna
 *  "Movimento" ficaria em branco, a linha continuaria listada numa tela cuja
 *  premissa é "só aportes e resgates" exibindo categoria de despesa, e o
 *  cabeçalho seguiria contando o valor em `monthly` — número e lista
 *  discordando no mesmo painel. E o lixo FICA: o `invalidateQueries` seguinte é
 *  `refetchType: 'active'`, e `/investimentos` está desmontada.
 *
 *  A guarda de formato continua logo abaixo como segunda linha de defesa — mas
 *  ela é o cinto, não o critério. Sem o filtro, o `.map` no painel lançaria
 *  `TypeError` **dentro do `onSuccess`**: a mutação cairia no `onError` e
 *  mostraria toast de erro com o `PATCH` já gravado — a pessoa refaz uma escrita
 *  que deu certo. */
function receberLinha(
  queryClient: ReturnType<typeof useQueryClient>,
  atualizado: Transaction,
): void {
  queryClient.setQueriesData<InfiniteData<TransactionList>>(
    { queryKey: ['transactions'], predicate: (query) => ehChaveDeLista(query.queryKey) },
    (dados) => {
      if (!dados || !Array.isArray(dados.pages)) return dados
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

/** O molde da chave de lista, tirado da PRÓPRIA fábrica em vez de reescrito
 *  aqui: se `transactionsQueryKey` ganhar um segmento ou trocar o filtro de
 *  lugar, o molde muda junto e o predicado continua verdadeiro. O `mes` é
 *  irrelevante — só a FORMA é lida. */
const MOLDE_DA_LISTA = transactionsQueryKey({ mes: '' })

/** `true` só para a chave de uma lista de `/lancamentos`.
 *
 *  O critério é a forma da chave: dois segmentos, e o segundo é o **objeto** de
 *  filtro. É ele que distingue das outras três — `'dashboard'`, `'investments'`
 *  e `'reports'` são strings, e nenhuma query nova sob este prefixo vira alvo
 *  por acidente sem antes imitar a fábrica.
 *
 *  Array é recusado explicitamente porque `typeof [] === 'object'`: nenhuma
 *  fábrica do projeto põe array nessa posição, e deixar a exceção implícita
 *  faria o predicado prometer "é o objeto de filtro" e aceitar outra coisa. */
function ehChaveDeLista(chave: readonly unknown[]): boolean {
  return (
    chave.length === MOLDE_DA_LISTA.length &&
    chave[0] === MOLDE_DA_LISTA[0] &&
    typeof chave[1] === 'object' &&
    chave[1] !== null &&
    !Array.isArray(chave[1])
  )
}

// ---------------------------------------------------------------- copy

/** A frase da troca: **de onde para onde, em palavras** — nunca só o destino,
 *  e nunca por cor.
 *
 *  Sem a anterior resolvível (`categoryId` presente e `categoryName` nulo), a
 *  frase encolhe em vez de inventar um nome. */
function fraseDaTroca(nomeAnterior: string | null, nome: string): string {
  if (!nomeAnterior) return `Categoria trocada para ${nome}`
  return `Categoria trocada de ${nomeAnterior} para ${nome}`
}

/** Por que a categoria ATUAL não está entre as opções — a nota sob o campo.
 *
 *  Dois motivos possíveis, e a frase diz qual: a categoria foi arquivada
 *  (marcação existente sobrevive ao arquivamento; atribuição nova, não) ou é
 *  grupo com subcategorias, que não recebe lançamento (§13). Só com nome
 *  resolvível: nomear a categoria é o que faz a nota explicar alguma coisa.
 *
 *  As duas frases dizem o substantivo (`A categoria`, `O grupo`) em vez de
 *  começar pelo nome, porque **nome injetado em frase nunca governa
 *  concordância** (precedente: docs/DESIGN.md §E7 (l)): com a semente de
 *  fábrica, 8 dos 15 grupos têm nome masculino, e a frase elíptica escreveria
 *  `Salário está arquivada e não pode ser escolhida`. Com o substantivo à vista
 *  a concordância é legítima em qualquer nome, inclusive nos que a casa
 *  inventar. A frase do grupo termina em instrução e perdeu o `agora`: 14 dos
 *  15 grupos **nascem** com subcategorias, e a frase precisa ser verdadeira nos
 *  dois mundos. Texto ratificado pelo designer em docs/DESIGN.md (h) §2. */
function notaDaCategoriaAtual(
  linha: Transaction,
  arvore: CategoryTree | undefined,
  atualEscolhivel: boolean,
  trocando: boolean,
): string | undefined {
  if (!trocando || atualEscolhivel || !arvore) return undefined
  const atual = categoriaPorId(arvore, linha.categoryId)
  const nome = atual?.name ?? linha.categoryName
  if (!nome) return undefined
  if (atual && atual.children.length > 0) {
    return `O grupo ${nome} tem subcategorias e não recebe lançamento. Escolha uma delas.`
  }
  return `A categoria ${nome} está arquivada e não pode ser escolhida de novo.`
}

/** O trecho do toast com o número REAL de (c) — só os outros lançamentos. */
export function fraseDosOutros(categorizados: number, mes: string): string {
  if (categorizados === 0) return `nenhum outro lançamento de ${mes} categorizado.`
  if (categorizados === 1) return `mais 1 lançamento de ${mes} categorizado.`
  return `mais ${categorizados} lançamentos de ${mes} categorizados.`
}
