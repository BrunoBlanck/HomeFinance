import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useId, useRef, useState } from 'react'
import type { KeywordImportEnvelope, KeywordImportPayload, KeywordImportReport } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { DataTable } from '@/components/DataTable/DataTable'
import { Panel } from '@/components/Panel/Panel'
import { TextArea } from '@/components/TextArea/TextArea'
import { contasQueryOptions } from '@/lib/accounts'
import { categoriasQueryOptions } from '@/lib/categories'
import { isConflict, messageForError } from '@/lib/errors'
import { aplicarImportacao, conferirImportacao } from '../api/aiImport'
import {
  categoriasACriar,
  contarAplicavel,
  corpoCabe,
  desmarcadasPeloServidor,
  erroDaConferencia,
  fraseDoResultado,
  fraseDosGruposNovos,
  fraseDosTotais,
  janelaGrandeDemais,
  lerJsonColado,
  MSG_GRANDE,
  MSG_JANELA_GRANDE,
  previaVazia,
  rotuloDoConfirmar,
} from '../importacao'
import type { JanelaDeTrabalho } from '../janela'
import { FRASE_JANELA_MUDOU, ID_DO_TITULO_REPROCESSAR } from '../secoes'
import { BlocosDaPrevia, COLUNAS_DE_CRIAR } from './BlocosDaPrevia'
import styles from './SecaoImportar.module.css'

/** A prévia viva: o relatório, o payload que o produziu (é ele que vai no
 *  confirm, byte a byte) e a janela em que o impacto foi medido. */
type Previa = {
  relatorio: KeywordImportReport
  payload: KeywordImportPayload
  /** `fromMonth-toMonth` — a chave de frescor. */
  chave: string
}

const FRASE_CONFERINDO = 'Conferindo o que a IA respondeu…'
const FRASE_PREVIA_VAZIA = 'Nada deste JSON pode ser aplicado.'

/** Seção 2 — **Importar o que a IA respondeu** (spec 0010 §4).
 *
 *  A ordem dentro da seção é normativa: campo de colar → ações → totais →
 *  bloco A (estrutura nova) → bloco B (palavras de conta, com impacto) →
 *  bloco C (palavras de categoria) → `<details>` do que fica de fora → barra
 *  de confirmar. Cada estado da seção mora num lugar fixo:
 *
 *  - **Vazio:** só o campo, a dica e o botão. Sem `EmptyState`, sem
 *    ilustração, sem "deixe a IA organizar".
 *  - **JSON inválido ou 400/413/429:** o erro mora **no campo** (`TextArea
 *    error`), e o foco volta ao `<textarea>`. Nunca toast, nunca `Alert` no
 *    topo, nunca eco do que foi colado, e `notes` nunca é renderizado (o
 *    servidor nem o devolve).
 *  - **Rede/500:** `Alert` dentro da seção, com "Tentar de novo".
 *  - **Sucesso:** o campo e a prévia dão lugar ao relatório do que DE FATO
 *    entrou, com os números do confirm — nunca os da prévia.
 *
 *  **Regra de frescor — agora existe objeto.** O impacto medido ("87 de
 *  212") é medido num período. Trocar o mês da casca ou o tamanho da janela
 *  descarta a prévia (o JSON colado fica) e a frase diz por quê: deixar o
 *  número na tela com outra janela seria mentira numérica. A detecção é por
 *  comparação de chave durante a renderização — o padrão do React para
 *  "reagir a uma prop que mudou" sem efeito —, e não uma `key` no pai, que
 *  levaria o texto colado junto.
 *
 *  **Orçamento de `aria-live`:** o `role="status"` dos totais (que ATUALIZA ao
 *  desmarcar — trade-off assumido: número parado seria mentira) e o
 *  `<output>` da barra. Nada mais anuncia sozinho.
 *
 *  **Nenhum `disabled`** nesta seção: o estado indisponível é `aria-disabled`,
 *  com o clique barrado no guarda da função — como em toda a tela. */
export function SecaoImportar({ janela }: { janela: JanelaDeTrabalho }) {
  const queryClient = useQueryClient()
  const tituloId = useId()
  const campoRef = useRef<HTMLTextAreaElement>(null)

  const [texto, setTexto] = useState('')
  const [previa, setPrevia] = useState<Previa | null>(null)
  const [desmarcadas, setDesmarcadas] = useState<ReadonlySet<string>>(() => new Set())
  const [erroDoCampo, setErroDoCampo] = useState<string | undefined>(undefined)
  const [erroDaSecao, setErroDaSecao] = useState<string | undefined>(undefined)
  const [erroDaJanela, setErroDaJanela] = useState<string | undefined>(undefined)
  const [erroDoConfirm, setErroDoConfirm] = useState<unknown>(null)
  const [janelaMudou, setJanelaMudou] = useState(false)
  const [resultado, setResultado] = useState<KeywordImportReport | null>(null)
  // "Importar outro JSON" pede o foco no campo, que só volta ao DOM na
  // renderização seguinte: a bandeira atravessa a renderização e o efeito
  // cumpre o pedido quando o campo existe.
  const querFocoNoCampo = useRef(false)

  useEffect(() => {
    if (resultado === null && querFocoNoCampo.current) {
      querFocoNoCampo.current = false
      campoRef.current?.focus()
    }
  })

  const chave = `${janela.fromMonth}-${janela.toMonth}`

  // A regra de frescor, em três linhas e sem efeito: a prévia foi medida
  // noutra janela, então ela morre AGORA, antes de qualquer número dela chegar
  // à tela — e a frase avisa. O React reexecuta esta renderização com o
  // estado novo antes de pintar nada.
  if (previa !== null && previa.chave !== chave) {
    setPrevia(null)
    setDesmarcadas(new Set())
    setErroDoConfirm(null)
    setJanelaMudou(true)
  }
  // O 422 pedia para trocar a janela; trocada, o aviso já cumpriu o papel e
  // sai. A chave em que ele nasceu é o que evita apagá-lo em qualquer outra
  // renderização.
  const [chaveDoErroDaJanela, setChaveDoErroDaJanela] = useState(chave)
  if (erroDaJanela !== undefined && chaveDoErroDaJanela !== chave) {
    setErroDaJanela(undefined)
    setChaveDoErroDaJanela(chave)
  }

  // Só para escrever o NOME do dono de uma palavra recusada por
  // `keyword_taken` — e só quando há prévia: sem ela não há id a resolver, e
  // a seção abriria com dois pedidos de rede a mais por nada.
  const arvore = useQuery({ ...categoriasQueryOptions(true), enabled: previa !== null })
  const contas = useQuery({ ...contasQueryOptions(true), enabled: previa !== null })

  const conferir = useMutation({
    mutationFn: (envelope: KeywordImportEnvelope) => conferirImportacao(envelope),
    onSuccess: (relatorio, envelope) => {
      setPrevia({
        relatorio,
        payload: envelope.payload,
        chave: `${envelope.fromMonth}-${envelope.toMonth}`,
      })
      setDesmarcadas(new Set())
      setJanelaMudou(false)
    },
    onError: (error) => {
      const erro = erroDaConferencia(error)
      if (erro.onde === 'campo') {
        setErroDoCampo(erro.mensagem)
        // O erro mora no campo, e é para lá que o foco volta: quem usa leitor
        // de tela ouve a mensagem pelo `aria-describedby`, quem enxerga vê a
        // borda e o ícone no lugar em que vai corrigir.
        campoRef.current?.focus()
        return
      }
      if (erro.onde === 'janela') {
        setErroDaJanela(erro.mensagem)
        setChaveDoErroDaJanela(chave)
        return
      }
      setErroDaSecao(erro.mensagem)
    },
  })

  const aplicar = useMutation({
    mutationFn: (envelope: KeywordImportEnvelope) => aplicarImportacao(envelope),
    onSuccess: async (relatorio) => {
      setResultado(relatorio)
      setPrevia(null)
      setDesmarcadas(new Set())
      setTexto('')
      // Categorias acabaram de existir e itens ganharam palavras: as telas
      // que servem o cache de `/categories` e `/accounts` precisam parar de
      // servir o retrato anterior.
      await queryClient.invalidateQueries({ queryKey: ['categories'] })
      await queryClient.invalidateQueries({ queryKey: ['accounts'] })
    },
    onError: (error) => setErroDoConfirm(error),
  })

  const temTexto = texto.trim() !== ''

  function submeterConferencia() {
    if (!temTexto || conferir.isPending) return
    setErroDoCampo(undefined)
    setErroDaSecao(undefined)
    setErroDaJanela(undefined)
    setErroDoConfirm(null)
    setJanelaMudou(false)

    const lido = lerJsonColado(texto)
    if (!lido.ok) {
      setErroDoCampo(lido.mensagem)
      campoRef.current?.focus()
      return
    }
    const envelope: KeywordImportEnvelope = {
      payload: lido.payload,
      fromMonth: janela.fromMonth,
      toMonth: janela.toMonth,
    }
    // O corpo inteiro, medido em bytes UTF-8 contra o teto do servidor, ANTES
    // do pedido (achado B2 do QA): pelo proxy, um corpo grande vira 502 em vez
    // de 413, e 502 cairia em "tentar de novo" para sempre.
    if (!corpoCabe(envelope)) {
      setErroDoCampo(MSG_GRANDE)
      campoRef.current?.focus()
      return
    }
    // A prévia anterior morre antes da nova chegar: o que fica na tela
    // enquanto o servidor responde é o esqueleto, nunca um número velho.
    setPrevia(null)
    setDesmarcadas(new Set())
    conferir.mutate(envelope)
  }

  function limpar() {
    setTexto('')
    setPrevia(null)
    setDesmarcadas(new Set())
    setErroDoCampo(undefined)
    setErroDaSecao(undefined)
    setErroDaJanela(undefined)
    setErroDoConfirm(null)
    setJanelaMudou(false)
    campoRef.current?.focus()
  }

  function alternar(ref: string, marcada: boolean) {
    setDesmarcadas((anteriores) => {
      const proximas = new Set(anteriores)
      if (marcada) proximas.delete(ref)
      else proximas.add(ref)
      return proximas
    })
  }

  function marcarTodas(marcar: boolean) {
    if (!previa) return
    setDesmarcadas(
      marcar ? new Set() : new Set(categoriasACriar(previa.relatorio).map((e) => e.ref)),
    )
  }

  const aplicavel = previa ? contarAplicavel(previa.relatorio, desmarcadas) : null
  const nadaParaAplicar =
    aplicavel === null || (aplicavel.categorias === 0 && aplicavel.palavras === 0)

  function submeterConfirmacao() {
    if (!previa || nadaParaAplicar || aplicar.isPending) return
    setErroDoConfirm(null)
    // O MESMO payload da prévia, mais os `ref` das desmarcadas — que vieram
    // do servidor no relatório, nunca foram montados aqui.
    const envelope: KeywordImportEnvelope = {
      payload: previa.payload,
      fromMonth: janela.fromMonth,
      toMonth: janela.toMonth,
      skipNewCategories: [...desmarcadas],
    }
    // Os `ref` somam ao corpo: uma prévia que coube pode virar um confirm que
    // não cabe. A mesma pré-checagem, no campo, sem pedido.
    if (!corpoCabe(envelope)) {
      setErroDoCampo(MSG_GRANDE)
      campoRef.current?.focus()
      return
    }
    aplicar.mutate(envelope)
  }

  function importarOutro() {
    // O campo volta a existir na próxima renderização; o foco vai para ele
    // então (no efeito de `querFocoNoCampo`, no alto do componente), e não
    // agora, que ele ainda não está no DOM.
    querFocoNoCampo.current = true
    setResultado(null)
  }

  function irParaReprocessar() {
    // Foco em elemento com `tabIndex={-1}`: o navegador rola até ele sem
    // `scrollTo`, e sem animação para quem pediu menos movimento.
    document.getElementById(ID_DO_TITULO_REPROCESSAR)?.focus()
  }

  /** O `Alert` da falha do confirm. Três casos, três ações: 409 (o estado
   *  mudou, nada foi gravado) → conferir de novo; 422 em `toMonth` (a janela
   *  não cabe) → sem ação, a saída é o seletor do topo; o resto → tentar de
   *  novo. Função chamada, e não componente, para não nascer um tipo novo a
   *  cada render. */
  function erroDoConfirmRenderizado(error: unknown) {
    if (janelaGrandeDemais(error)) {
      return (
        <Alert tone="error" title="A janela é grande demais para aplicar.">
          {MSG_JANELA_GRANDE}
        </Alert>
      )
    }
    if (isConflict(error)) {
      return (
        <Alert
          tone="error"
          title="O estado mudou desde a conferência."
          action={
            <Button onClick={submeterConferencia} loading={conferir.isPending}>
              Conferir de novo
            </Button>
          }
        >
          {messageForError(error)}
        </Alert>
      )
    }
    return (
      <Alert
        tone="error"
        title="Não foi possível aplicar."
        action={
          <Button onClick={submeterConfirmacao} loading={aplicar.isPending}>
            Tentar de novo
          </Button>
        }
      >
        {messageForError(error)}
      </Alert>
    )
  }

  // A frase do `role="status"`: um nó, uma frase por estado, cada estado
  // anunciado uma vez.
  let fraseDeEstado = ''
  if (conferir.isPending) fraseDeEstado = FRASE_CONFERINDO
  else if (janelaMudou) fraseDeEstado = FRASE_JANELA_MUDOU
  else if (previa && aplicavel) {
    fraseDeEstado = previaVazia(previa.relatorio.totals)
      ? FRASE_PREVIA_VAZIA
      : fraseDosTotais(previa.relatorio.totals, aplicavel)
  }

  return (
    <Panel as="section" titleId={tituloId} padding="none">
      <div className={styles.secao}>
        <header className={styles.cabecalho}>
          <h2 className={styles.titulo} id={tituloId}>
            2 · Importar o que a IA respondeu
          </h2>
          <p className={styles.apoio}>
            Cole o JSON que a IA devolveu e confira, item a item, o que entra. Nada é gravado até
            você confirmar.
          </p>
        </header>

        {resultado ? (
          <RelatorioDoImport
            relatorio={resultado}
            onImportarOutro={importarOutro}
            onIrParaReprocessar={irParaReprocessar}
          />
        ) : (
          <>
            <TextArea
              ref={campoRef}
              label="Cole aqui o JSON que a IA respondeu"
              hint="Só o JSON — do primeiro { ao último }. Se a IA escreveu texto antes ou depois, apague."
              error={erroDoCampo}
              value={texto}
              onChange={(evento) => {
                setTexto(evento.target.value)
                // Editar o texto é começar a corrigir: o erro antigo sai do
                // caminho, e volta se a conferência nova falhar.
                if (erroDoCampo) setErroDoCampo(undefined)
              }}
            />

            <div className={styles.acoes}>
              {/* NUNCA `disabled`: com o campo vazio o botão diz o que falta
                  e continua focável; o clique morre no guarda da função. */}
              <Button
                variant="primary"
                onClick={submeterConferencia}
                loading={conferir.isPending}
                aria-disabled={temTexto ? undefined : true}
              >
                {temTexto ? 'Conferir' : 'Cole o JSON para conferir'}
              </Button>
              {temTexto || previa ? (
                <Button variant="quiet" onClick={limpar}>
                  Limpar
                </Button>
              ) : null}
            </div>

            {erroDaSecao ? (
              <Alert
                tone="error"
                title="Não foi possível conferir o JSON."
                action={
                  <Button onClick={submeterConferencia} loading={conferir.isPending}>
                    Tentar de novo
                  </Button>
                }
              >
                {erroDaSecao}
              </Alert>
            ) : null}

            {/* 422 em `fields.toMonth`: SEM "Tentar de novo" — repetir daria o
                mesmo 422. A ação é o seletor do topo, e o aviso some sozinho
                quando a janela muda. */}
            {erroDaJanela ? (
              <Alert tone="error" title="A janela é grande demais para conferir.">
                {erroDaJanela}
              </Alert>
            ) : null}

            {fraseDeEstado ? (
              <p className={styles.totais} role="status">
                {fraseDeEstado}
              </p>
            ) : null}

            {conferir.isPending ? (
              <div className={styles.esqueleto}>
                <DataTable
                  caption="Categorias a criar"
                  columns={COLUNAS_DE_CRIAR}
                  rows={[]}
                  rowKey={(entrada) => entrada.ref}
                  loading
                />
              </div>
            ) : null}

            {previa ? (
              <BlocosDaPrevia
                relatorio={previa.relatorio}
                desmarcadas={desmarcadas}
                onAlternar={alternar}
                onMarcarTodas={marcarTodas}
                arvore={arvore.data}
                contas={contas.data?.items ?? []}
              />
            ) : null}

            {erroDoConfirm !== null ? erroDoConfirmRenderizado(erroDoConfirm) : null}
          </>
        )}
      </div>

      {previa && aplicavel && !resultado ? (
        // Grudada no rodapé por BORDA, nunca por sombra — a mesma barra da
        // revisão da importação. Fica acima da barra de navegação do celular.
        <div className={styles.barra}>
          <output className={styles.nuance} aria-live="polite">
            {fraseDosGruposNovos(aplicavel)}
          </output>
          {/* O rótulo diz a SAÍDA e recalcula ao desmarcar: desmarcar uma
              categoria de 2 palavras faz o número cair 2 — é a prova, no
              próprio botão, de que desmarcar leva as palavras junto. */}
          <Button
            variant="primary"
            onClick={submeterConfirmacao}
            loading={aplicar.isPending}
            aria-disabled={nadaParaAplicar ? true : undefined}
          >
            {rotuloDoConfirmar(aplicavel)}
          </Button>
        </div>
      ) : null}
    </Panel>
  )
}

/** O relatório do que **de fato** entrou — com os números do confirm.
 *
 *  Uma `<dl>` de até cinco linhas (as zeradas não aparecem), a frase em
 *  `role="status"` e duas saídas: seguir para Reprocessar (o próximo passo
 *  recomendado — palavra nova só vale depois de reprocessar) ou colar outro
 *  JSON. **Sem toast:** o resultado é o conteúdo da seção, não um aviso que
 *  some. */
function RelatorioDoImport({
  relatorio,
  onImportarOutro,
  onIrParaReprocessar,
}: {
  relatorio: KeywordImportReport
  onImportarOutro: () => void
  onIrParaReprocessar: () => void
}) {
  const { totals } = relatorio
  const desmarcadas = desmarcadasPeloServidor(relatorio)
  const linhas: Array<[string, number]> = [
    ['Categorias criadas', totals.categoriesCreated],
    ['Palavras gravadas', totals.added],
    ['Já estavam lá', totals.skipped],
    ['Recusadas', totals.rejected],
    ['Desmarcadas por você', desmarcadas],
  ]

  return (
    <div className={styles.relatorio}>
      <p className={styles.totais} role="status">
        {fraseDoResultado(totals)}
      </p>
      <dl className={styles.lista}>
        {linhas
          .filter(([, quantidade]) => quantidade > 0)
          .map(([rotulo, quantidade]) => (
            <div className={styles.linha} key={rotulo}>
              <dt>{rotulo}</dt>
              <dd>{quantidade}</dd>
            </div>
          ))}
      </dl>
      <p className={styles.apoio}>
        Palavra-chave nova só passa a valer quando você reprocessa o período — nada mudou nos
        lançamentos ainda.
      </p>
      <div className={styles.acoes}>
        <Button variant="primary" onClick={onIrParaReprocessar}>
          Ir para Reprocessar
        </Button>
        <Button variant="secondary" onClick={onImportarOutro}>
          Importar outro JSON
        </Button>
      </div>
    </div>
  )
}
