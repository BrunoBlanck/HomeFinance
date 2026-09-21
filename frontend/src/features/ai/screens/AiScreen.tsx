import { useQuery } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useId } from 'react'
import {
  aplicarNaBusca,
  type MudancaDeBusca,
  type TamanhoDaJanela,
  tamanhoDaJanelaValido,
  validarBusca,
} from '@/app/search'
import { Select } from '@/components/Select/Select'
import { useFocoNoTitulo } from '@/lib/focus'
import { FUSO_PADRAO, mesDaURL } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import { SecaoExportar } from '../components/SecaoExportar'
import { SecaoImportar } from '../components/SecaoImportar'
import { SecaoReprocessar } from '../components/SecaoReprocessar'
import { janelaDeTrabalho, rotuloDaJanela } from '../janela'
import styles from './AiScreen.module.css'

/** O tamanho padrão da janela: o mês corrente e os dois anteriores (spec 0010
 *  §2.1). É o padrão, então a **URL canônica não escreve a chave** — a mesma
 *  regra de `natureza` e de `tipo` em `app/search.ts`. */
const PADRAO: TamanhoDaJanela = 3

/** As opções do seletor, do maior período para o menor: o padrão primeiro. */
const TAMANHOS: readonly TamanhoDaJanela[] = [3, 2, 1]

/** Tela `/ia` — o menu IA (spec 0010, entrega E9).
 *
 *  **A ideia da tela em uma frase:** o app escreve o pedido, a pessoa leva a uma
 *  IA de fora e traz a resposta de volta. Nada sai daqui sozinho — não há
 *  cliente de IA, não há chave de API, não há rota que fale com provedor
 *  nenhum (spec 0010 §2.2). O transporte é a pessoa, e é por isso que o aviso
 *  da seção 1 vem antes de qualquer botão.
 *
 *  **Três seções empilhadas, sempre abertas, numeradas no próprio `<h2>`** — e
 *  não abas, acordeão ou wizard. O fluxo **não é linear**: dá para importar sem
 *  ter exportado nesta sessão, e dá para reprocessar sem ter importado nada. Os
 *  números dizem a ordem RECOMENDADA, não uma sequência obrigatória, por isso
 *  são texto e não bolinhas ligadas por linha.
 *
 *  **Esta tela não tem dinheiro.** Nenhum centavo aparece aqui, então
 *  `--income`, `--expense` e `--chart-*` não entram em lugar nenhum: só
 *  `--accent` (ação, link, foco, o "Copiado.") e `--danger` (erro). O teste de
 *  aceite é `filter: grayscale(1)` na tela inteira sem perder informação.
 *
 *  **As três fatias (E9a, E9b, E9c — spec 0010 §10.2) estão no ar:** as
 *  seções 1 e 2 funcionam ponta a ponta, e a 3 orquestra as duas rotas de
 *  reprocessamento que já existiam — sem backend novo. */
export function AiScreen() {
  const navigate = useNavigate()
  const tituloRef = useFocoNoTitulo()
  const rotuloDoPeriodoId = useId()

  // Validada aqui, e não lida crua de `location.search`: é a fronteira entre a
  // URL (que a pessoa edita) e a query da API. `meses` fora de 1–3 simplesmente
  // some, e a tela abre na janela padrão — o servidor recusaria uma janela
  // maior com 400, e um link truncado no WhatsApp tem de abrir o app.
  const buscaBruta = useRouterState({ select: (estado) => estado.location.search })
  const busca = validarBusca(buscaBruta as Record<string, unknown>)

  const session = useQuery(sessionQueryOptions)
  const fuso = session.data?.household.timezone ?? FUSO_PADRAO
  const mes = mesDaURL(busca.mes, fuso)
  const tamanho = busca.meses ?? PADRAO

  // **Uma função só** deriva a janela, e é esta chamada. Nenhuma seção calcula
  // mês por conta própria (spec 0010 §2.1) — três derivações seriam três
  // janelas se contradizendo no dia em que uma delas esquecesse a virada de
  // ano.
  const janela = janelaDeTrabalho(mes, tamanho)

  useEffect(() => {
    document.title = 'IA · HomeFinance'
  }, [])

  function trocarBusca(mudanca: MudancaDeBusca) {
    void navigate({
      to: '/ia',
      search: (anterior) => aplicarNaBusca(anterior, mudanca),
      replace: true,
    })
  }

  return (
    <div className={styles.pagina}>
      <div className={styles.cabecalho}>
        <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
          IA
        </h1>
        <p className={styles.apoio}>
          O app escreve o pedido, você leva a uma IA de fora e traz a resposta de volta. Nada sai
          daqui sozinho.
        </p>
      </div>

      {/* A janela de trabalho: UMA, no topo, valendo para as três seções
          (aceite 47). Ela termina no mês da casca e anda para trás — em meses
          de competência, nunca em datas, porque as rotas de reprocessamento só
          falam mês (emenda §10, achado A2). */}
      <div className={styles.faixa}>
        <Select
          label="Período"
          density="compact"
          options={TAMANHOS.map((quantos) => ({
            value: String(quantos),
            // O rótulo diz os meses RESOLVIDOS ("3 meses · julho a setembro"):
            // "últimos 3 meses" obrigaria a pessoa a contar de cabeça qual é o
            // período, e é justamente o que ela está prestes a exportar.
            label: `${quantos} ${quantos === 1 ? 'mês' : 'meses'} · ${rotuloDaJanela(
              janelaDeTrabalho(mes, quantos),
            )}`,
          }))}
          value={String(tamanho)}
          aria-describedby={rotuloDoPeriodoId}
          onChange={(evento) => {
            // A MESMA allowlist do portão da URL, não um `includes` local: o
            // que passa aqui é o que a próxima leitura aceita.
            const escolhido = tamanhoDaJanelaValido(evento.target.value)
            trocarBusca({ meses: escolhido === PADRAO ? undefined : escolhido })
          }}
        />
        <p className={styles.notaDaFaixa} id={rotuloDoPeriodoId}>
          Vale para as três seções. A janela termina no mês escolhido no alto da página.
        </p>
      </div>

      {/* A `key` é a regra de frescor em uma linha: trocar o mês da casca ou o
          tamanho da janela remonta a seção, e com ela morrem o "Copiado." e o
          estado do `<details>` da janela anterior. */}
      <SecaoExportar key={`${janela.fromMonth}-${janela.toMonth}`} janela={janela} />

      {/* SEM `key` de propósito: a seção 2 tem um objeto que a remontagem
          levaria junto — o JSON colado. Ela mesma descarta a prévia quando a
          janela muda (o impacto foi medido noutro período) e publica o aviso
          "A janela mudou — confira de novo…", mantendo o texto no campo. */}
      <SecaoImportar janela={janela} />

      {/* SEM `key` também, e pelo mesmo motivo da seção 2: uma remontagem
          abandonaria uma execução em curso. A seção compara a chave da janela
          em que a prévia foi medida com a atual durante a renderização. */}
      <SecaoReprocessar janela={janela} />
    </div>
  )
}
