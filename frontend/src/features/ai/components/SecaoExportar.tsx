import { useQuery } from '@tanstack/react-query'
import { useEffect, useId, useMemo, useState } from 'react'
import type { AiExportStats } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { CheckIcon } from '@/components/icons/CheckIcon'
import { Panel } from '@/components/Panel/Panel'
import { messageForError } from '@/lib/errors'
import { aiPromptQueryOptions } from '../api/aiPrompt'
import {
  contarLinhas,
  type JanelaDeTrabalho,
  nomeDoArquivoDoPrompt,
  rotuloDaJanela,
} from '../janela'
import styles from './SecaoExportar.module.css'

/** Quanto tempo o "Copiado." fica na tela: tempo de ler a confirmação sem que
 *  ela vire mobília permanente ao lado do botão. */
const SUMICO_DO_COPIADO = 6_000

/** O que o `<output>` está dizendo agora. `null` é o estado normal — nada
 *  aconteceu ainda, e um lugar reservado com texto vazio seria ruído. */
type AvisoDaCopia = 'copiado' | 'falhou'

/** Seção 1 — **Exportar o prompt** (spec 0010 §3).
 *
 *  A ordem dos blocos é normativa e é esta: apoio, **aviso**, estatísticas,
 *  ações, texto. O aviso vem antes de qualquer botão (aceite 50) porque este é
 *  o único ponto do produto em que dado financeiro sai da casa — e sai pela mão
 *  da pessoa, não pela rede.
 *
 *  **O aviso é prosa, não `Alert tone="warning"`.** Um banner amarelo com ícone
 *  é justamente o objeto que se aprende a pular, e `--warning` neste projeto já
 *  tem outro dono (pendência de categorização). O peso vem da tipografia e da
 *  posição: a primeira linha, em `--text-15`/`--ink` com dois trechos em
 *  `<strong>`, é a mais escura da seção e está acima de tudo.
 *
 *  **São três linhas, e a do meio é a que torna o consentimento informado**
 *  (achado médio do `revisor-seguranca`, 21/09/2026). A redação anterior dizia
 *  "não vão nomes", e isso é FALSO para o conteúdo que domina o export: o
 *  sanitizador da importação (`internal/importer/sanitize`) tira o que
 *  identifica terceiro por documento — CPF, CNPJ, agência, número de conta —,
 *  mas **mantém o nome da contraparte**, porque é ele que responde "quem eu
 *  paguei"; e o parser do Inter junta a mensagem do Pix DENTRO da descrição,
 *  texto que um terceiro escreveu. Na prática, `Pix enviado - Fulano de Tal`
 *  sai daqui. Negar a maior categoria de dado pessoal que de fato sai — e que é
 *  dado de TERCEIRO, não só da casa — transformaria o consentimento em
 *  consentimento desinformado, e a mitigação INTEIRA da única ameaça nova desta
 *  feature é o consentimento. A terceira linha lista o que realmente fica de
 *  fora: é ela que transforma susto em decisão.
 *
 *  **Copiar e Baixar entregam o MESMO texto que está no `<pre>`** (aceite 49):
 *  existe uma variável de texto, ela veio pronta do servidor, e ninguém a
 *  remonta no cliente. */
export function SecaoExportar({ janela }: { janela: JanelaDeTrabalho }) {
  const prompt = useQuery(aiPromptQueryOptions(janela))
  const tituloId = useId()
  const [aviso, setAviso] = useState<AvisoDaCopia | null>(null)
  const [textoAberto, setTextoAberto] = useState(false)

  const texto = prompt.data?.prompt ?? ''
  const periodo = rotuloDaJanela(janela)
  // Contado UMA vez: o mesmo número aparece na linha de estatísticas e no
  // `<summary>` porque é a mesma coisa — o tamanho do texto que vai ser colado
  // em outro lugar. Dois cálculos divergiriam no dia em que um deles mudasse.
  const linhas = useMemo(() => contarLinhas(texto), [texto])
  const pronto = prompt.isSuccess && texto !== ''

  // O "Copiado." some sozinho; o "não consegui copiar" **não** — ele pede uma
  // ação ("abra Ver o texto e copie à mão"), e sumir no meio da leitura
  // deixaria a pessoa sem saber por que o `<details>` abriu sozinho.
  useEffect(() => {
    if (aviso !== 'copiado') return
    const relogio = setTimeout(() => setAviso(null), SUMICO_DO_COPIADO)
    return () => clearTimeout(relogio)
  }, [aviso])

  // Nota de estado, e é por isso que a tela passa `key={fromMonth-toMonth}`
  // nesta seção: trocar a janela troca o texto, e um "Copiado." herdado da
  // janela anterior afirmaria que o texto NOVO está no clipboard — não está.
  // A remontagem pela `key` zera `aviso` e `textoAberto` de uma vez, sem um
  // efeito de limpeza que teria de lembrar de cada estado novo.

  async function copiar() {
    if (!pronto) return
    try {
      await navigator.clipboard.writeText(texto)
      setAviso('copiado')
    } catch {
      // Permissão negada, contexto inseguro, navegador antigo: a saída manual
      // existe e a tela a abre, em vez de deixar um botão que não fez nada.
      setTextoAberto(true)
      setAviso('falhou')
    }
  }

  function baixar() {
    if (!pronto) return
    // O MESMO texto, num Blob — nunca uma segunda requisição: ela poderia
    // devolver outro retrato, e o arquivo deixaria de ser o que está na tela.
    const url = URL.createObjectURL(new Blob([texto], { type: 'text/markdown;charset=utf-8' }))
    const ligacao = document.createElement('a')
    ligacao.href = url
    ligacao.download = nomeDoArquivoDoPrompt(janela)
    ligacao.click()
    // Sem o revoke o Blob fica preso na memória da aba até ela fechar, e aqui
    // ele tem o tamanho do extrato de três meses.
    URL.revokeObjectURL(url)
  }

  return (
    <Panel as="section" titleId={tituloId} padding="none">
      <div className={styles.secao}>
        <header className={styles.cabecalho}>
          {/* O número é TEXTO no `<h2>`, nunca bolinha numerada ligada por
              linha: as três seções não são um passo a passo (dá para importar
              sem ter exportado), e o `ImportStepper` não entra aqui. */}
          <h2 className={styles.titulo} id={tituloId}>
            1 · Exportar o prompt
          </h2>
          <p className={styles.apoio}>
            O texto que você cola numa IA de fora — ChatGPT, Claude, a que você usar — para ela
            propor palavras-chave e categorias.
          </p>
        </header>

        {/* ACIMA de qualquer botão. Aceite 50 da spec 0010, e tem teste. */}
        <div className={styles.aviso}>
          <p className={styles.avisoPrincipal}>
            Este texto leva <strong>as descrições e os valores das suas movimentações</strong> do
            período. O aplicativo não envia nada:{' '}
            <strong>quem copia e cola numa IA de fora é você</strong>.
          </p>
          <p className={styles.avisoSecundario}>
            As descrições vão como você as vê no app — e costumam trazer o nome de quem pagou ou
            recebeu, e o que a pessoa escreveu na mensagem do Pix.
          </p>
          <p className={styles.avisoSecundario}>
            Não vão: saldos, instituição, agência e número de conta, dias de fatura, seu nome e
            e-mail de cadastro, nem identificador de lançamento.
          </p>
        </div>

        {prompt.isError ? (
          <Alert
            tone="error"
            title="Não foi possível montar o prompt."
            action={
              <Button onClick={() => void prompt.refetch()} loading={prompt.isFetching}>
                Tentar de novo
              </Button>
            }
          >
            {messageForError(prompt.error)}
          </Alert>
        ) : (
          <>
            {/* Uma linha de texto, e não três cartões com número gigante: são
                contagens que dizem o TAMANHO do que vai ser colado, não
                indicadores para acompanhar mês a mês. */}
            <p className={styles.estatisticas}>
              {prompt.isPending ? (
                <span role="status">Montando o prompt de {periodo}…</span>
              ) : (
                estatisticas(linhas, prompt.data?.stats)
              )}
            </p>

            <div className={styles.acoes}>
              {/* NUNCA `disabled` nesta tela: um botão desabilitado sai da
                  navegação por teclado e perde contraste justo quando a pessoa
                  está esperando. `aria-disabled` anuncia o estado, mantém o
                  foco, e o clique morre no guarda de cada função. */}
              <Button
                variant="primary"
                onClick={() => void copiar()}
                aria-disabled={!pronto || undefined}
              >
                Copiar o prompt
              </Button>
              <Button variant="secondary" onClick={baixar} aria-disabled={!pronto || undefined}>
                Baixar .md
              </Button>
              {/* `<output>` é o elemento do RESULTADO de uma ação, e já é uma
                  live region `polite` por natureza. O rótulo do botão **não**
                  muda ao copiar: um botão que vira "Copiado!" some como botão
                  justo quando a pessoa quer copiar de novo. */}
              <output className={styles.resultado} aria-live="polite">
                {aviso === 'copiado' ? (
                  <span className={styles.copiado}>
                    <CheckIcon size={14} />
                    Copiado.
                  </span>
                ) : null}
                {aviso === 'falhou'
                  ? 'Não consegui copiar. Abra "Ver o texto" e copie à mão.'
                  : null}
              </output>
            </div>

            <details
              className={styles.texto}
              open={textoAberto}
              onToggle={(evento) => setTextoAberto(evento.currentTarget.open)}
            >
              <summary className={styles.resumoDoTexto}>
                {prompt.isPending ? 'Ver o texto' : `Ver o texto (${linhas} linhas)`}
              </summary>
              {/* Papel, não terminal: `--surface-sunken`, tinta `--ink`, zero
                  cor de sintaxe.
                  A caixa ROLA (22rem de teto), e conteúdo rolável precisa ser
                  alcançável pelo teclado — sem `tabIndex` quem não usa mouse
                  não chega ao fim do texto. É a técnica documentada da WCAG
                  2.1.1 para região rolável, com `<section>` + nome acessível
                  em vez de `role="region"` escrito à mão. */}
              {/* biome-ignore lint/a11y/noNoninteractiveTabindex: regiao rolavel precisa ser alcancavel pelo teclado (WCAG 2.1.1) */}
              <section className={styles.caixaDoTexto} tabIndex={0} aria-label="Texto do prompt">
                <pre className={styles.pre}>{texto}</pre>
              </section>
            </details>
          </>
        )}
      </div>
    </Panel>
  )
}

/** `540 linhas · 32 descrições distintas · 4 contas · 41 categorias`
 *
 *  `linhas` é o tamanho do TEXTO — o mesmo número do `<summary>`, porque é a
 *  mesma pergunta: quanto você está prestes a colar em outro lugar.
 *
 *  Período sem movimentação troca "N descrições distintas" por "nenhuma
 *  movimentação no período": escrever "0 descrições distintas" e a frase do
 *  vazio lado a lado diria a mesma coisa duas vezes. O prompt continua válido —
 *  contas e categorias bastam (spec 0010 §3.6). */
function estatisticas(linhas: number, stats: AiExportStats | undefined): string {
  if (!stats) return ''
  const partes = [plural(linhas, 'linha', 'linhas')]

  if (stats.transactions === 0) {
    partes.push(plural(stats.accounts, 'conta', 'contas'))
    partes.push(plural(stats.categories, 'categoria', 'categorias'))
    partes.push('nenhuma movimentação no período')
    return partes.join(' · ')
  }

  partes.push(plural(stats.descriptions, 'descrição distinta', 'descrições distintas'))
  partes.push(plural(stats.accounts, 'conta', 'contas'))
  partes.push(plural(stats.categories, 'categoria', 'categorias'))
  // O corte é DECLARADO, aqui e no próprio texto do prompt: corte silencioso
  // faria a IA responder com confiança sobre um extrato que não é o da pessoa.
  if (stats.truncatedDescriptions > 0) {
    const quantas = stats.truncatedDescriptions
    partes.push(
      `${quantas} ${quantas === 1 ? 'descrição ficou' : 'descrições ficaram'} de fora (as menos frequentes)`,
    )
  }
  return partes.join(' · ')
}

function plural(quantidade: number, um: string, varios: string): string {
  return `${quantidade} ${quantidade === 1 ? um : varios}`
}
