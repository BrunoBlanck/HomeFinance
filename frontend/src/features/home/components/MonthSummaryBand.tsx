import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { MoneyText } from '@/components/MoneyText/MoneyText'
import { Panel } from '@/components/Panel/Panel'
import { Skeleton } from '@/components/Skeleton/Skeleton'
import { TextLink } from '@/components/TextLink/TextLink'
import { isUnauthenticated, messageForError } from '@/lib/errors'
import { nomeDoMes } from '@/lib/month'
import { dashboardQueryOptions } from '../api/dashboard'
import styles from './MonthSummaryBand.module.css'

/** O `<h2>` visível que nomeia a região — e que diz de QUE mês são os números.
 *  Uma faixa por tela, então o id é literal, como em `/investimentos`. */
const ID_DO_TITULO = 'painel-resumo'

/** A faixa de resumo do mês do painel (spec 0008; docs/DESIGN.md E4a).
 *
 *  Três números, nesta ordem — `Receita do mês`, `Gasto no cartão de crédito`,
 *  `Investido no mês` —, num pedido de rede e só um. Quatro decisões que esta
 *  faixa sustenta:
 *
 *  1. **É a terceira ocorrência de um idioma que o app já tem:** a `<dl>` de
 *     rótulo à esquerda e número tabular à direita, separada por filete de 1px,
 *     de `TransferPairPanel` e da `ColunaDeNumeros` de `/investimentos`. Nenhum
 *     cartão por número, nenhuma sombra, nenhum ícone ao lado de valor.
 *  2. **Cromaticamente silenciosa.** Zero `--income`, zero `--expense`. A
 *     palavra do rótulo já diz a direção nos dois primeiros; e o líquido do
 *     terceiro **não recebe tinta** pelo mesmo motivo do `Líquido` de
 *     `/transferencias`: `--expense` diria "você gastou" sobre um resgate, que
 *     é dinheiro da casa voltando. O sinal (`sign="always"`) e a frase de apoio
 *     é que carregam a direção — nunca a cor.
 *  3. **Nenhuma aritmética de dinheiro aqui.** `investmentNetCents` chega
 *     pronto do servidor; subtrair aportes e resgates no cliente criaria uma
 *     segunda fonte para o mesmo número (ADR-003, lição de 18/09/2026).
 *  4. **Um pedido de rede.** É proibido buscar `/accounts` ou `/categories`
 *     para decidir estado vazio — o contrato traz `creditCardAccountCount` e
 *     `investmentCategoryCount` justamente para isso (aceite 18). */
export function MonthSummaryBand({ mes }: { mes: string }) {
  const resumo = useQuery(dashboardQueryOptions(mes))

  // 401 não é erro desta faixa: quem avisa e redireciona é a casca (spec 0008
  // §3.7), como já acontece com a sessão. Dois alarmes para o mesmo problema —
  // e um deles piscando enquanto a rota troca — é ruído.
  const sessaoVencida = resumo.isError && isUnauthenticated(resumo.error)

  if (resumo.isError && !sessaoVencida) {
    return (
      <Alert
        tone="error"
        title="Não foi possível carregar o resumo do mês."
        action={
          <Button onClick={() => void resumo.refetch()} loading={resumo.isFetching}>
            Tentar de novo
          </Button>
        }
      >
        {messageForError(resumo.error)}
      </Alert>
    )
  }

  const carregando = resumo.isPending || sessaoVencida
  // Refetch com dado na tela (trocar de mês): o quadro anterior fica, apagado e
  // marcado como ocupado. Skeleton só na PRIMEIRA carga.
  const atualizando = resumo.isFetching && !resumo.isPending
  /** Os números, ou `undefined` enquanto não há o que mostrar. Nunca um zero
   *  provisório: zero é um valor, e exibi-lo antes da resposta é mentir. */
  const numeros = carregando ? undefined : resumo.data

  // ⚠️ O traço exige os TRÊS campos zerados, e não só o contador de cadastro.
  // `creditCardAccountCount` conta apenas cartões vivos, mas o gasto de um
  // cartão ARQUIVADO continua contando: a condição ingênua
  // (`count === 0 → traço`) esconderia dinheiro de verdade. Havendo qualquer
  // valor ou qualquer contagem, o número ganha.
  const semCartao =
    numeros !== undefined &&
    numeros.creditCardAccountCount === 0 &&
    numeros.creditCardExpenseCents === 0 &&
    numeros.creditCardExpenseCount === 0

  // A mesma regra, pelo mesmo motivo: categoria de investimento arquivada
  // continua marcando lançamento (ADR-029d).
  const semInvestimento =
    numeros !== undefined &&
    numeros.investmentCategoryCount === 0 &&
    numeros.investmentNetCents === 0 &&
    numeros.investmentCount === 0

  return (
    <Panel padding="none">
      <section
        className={styles.secao}
        aria-labelledby={ID_DO_TITULO}
        aria-busy={carregando || atualizando || undefined}
      >
        {/* Sem o ano: ele está sempre visível no seletor de mês da casca. */}
        <h2 className={styles.tituloDaSecao} id={ID_DO_TITULO}>
          Resumo de {nomeDoMes(mes)}
        </h2>

        <dl className={styles.lista}>
          <LinhaDoResumo
            rotulo="Receita do mês"
            valor={
              numeros ? (
                // Neutro e sem sinal: a palavra "Receita" já diz a direção, e
                // pintar gastaria --income em informação que o texto carrega.
                <MoneyText cents={numeros.incomeCents} format="currency" emphasis="total" />
              ) : (
                <Esqueleto />
              )
            }
            apoio={numeros ? <Contagem quantas={numeros.incomeCount} /> : null}
          />

          {/* O rótulo é a CERCA do número: encurtá-lo para "Gastos" ou "Gasto
              do mês" transformaria um parcial em total. */}
          <LinhaDoResumo
            rotulo="Gasto no cartão de crédito"
            valor={
              numeros === undefined ? (
                <Esqueleto />
              ) : semCartao ? (
                <SemValor />
              ) : (
                <MoneyText
                  cents={numeros.creditCardExpenseCents}
                  format="currency"
                  emphasis="total"
                />
              )
            }
            apoio={
              numeros === undefined ? null : semCartao ? (
                <SemCadastro frase="Nenhum cartão de crédito cadastrado">
                  {/* Link, e não botão: navegar é trabalho de link. E leva o
                      mês da casca junto, para a pessoa voltar onde estava. */}
                  <TextLink to="/contas" search={{ mes }}>
                    Ir para contas
                  </TextLink>
                </SemCadastro>
              ) : (
                <Contagem quantas={numeros.creditCardExpenseCount} />
              )
            }
          />

          <LinhaDoResumo
            rotulo="Investido no mês"
            valor={
              numeros === undefined ? (
                <Esqueleto />
              ) : semInvestimento ? (
                <SemValor />
              ) : (
                // `sign="always"` e NEUTRO: o sinal é o portador visual da
                // direção, e a cor não entra — aporte não é gasto e resgate não
                // é ganho.
                <MoneyText
                  cents={numeros.investmentNetCents}
                  format="currency"
                  sign="always"
                  emphasis="total"
                />
              )
            }
            apoio={
              numeros === undefined ? null : semInvestimento ? (
                <SemCadastro frase="Nenhuma categoria de investimento ainda">
                  <TextLink to="/categorias" search={{ mes }}>
                    Ir para categorias
                  </TextLink>
                </SemCadastro>
              ) : (
                <Contagem
                  quantas={numeros.investmentCount}
                  complemento={
                    fraseDoLiquido(numeros.investmentNetCents, numeros.investmentCount) ?? undefined
                  }
                />
              )
            }
          />
        </dl>
      </section>
    </Panel>
  )
}

// -------------------------------------------------------------- pedaços

/** Uma linha da faixa: o termo à esquerda, o número à direita.
 *
 *  A contagem mora no `<dt>`, nunca na `<dd>` — ela qualifica o termo; na
 *  `<dd>` viraria um segundo valor concorrendo com o dinheiro. É o mesmo
 *  dispositivo de `/investimentos`. */
function LinhaDoResumo({
  rotulo,
  valor,
  apoio,
}: {
  rotulo: string
  valor: ReactNode
  /** A segunda linha do `<dt>`. `null` enquanto carrega: contagem inventada é
   *  pior que ausência. */
  apoio: ReactNode
}) {
  return (
    <div className={styles.linha}>
      <dt className={styles.termo}>
        <span>{rotulo}</span>
        {apoio}
      </dt>
      {/* O par <dt>/<dd> é o nome acessível do número; o valor falado sai do
          `sr-only` interno do MoneyText. Nada de aria-label em <span>. */}
      <dd className={styles.valor}>{valor}</dd>
    </div>
  )
}

function Contagem({
  quantas,
  complemento,
}: {
  quantas: number
  /** A frase que explica o sinal do líquido, quando ela existe. */
  complemento?: string | undefined
}) {
  return (
    <div className={styles.motivo}>
      <span className={styles.contagem}>{fraseDaContagem(quantas)}</span>
      {complemento === undefined ? null : (
        <>
          <Separador />
          <span>{complemento}</span>
        </>
      )}
    </div>
  )
}

/** "Nenhum cartão de crédito cadastrado · Ir para contas".
 *
 *  Este estado é diferente de "tem cartão e não gastou", que mostra `R$ 0,00` —
 *  e distingui-los é a razão de o contrato trazer os dois contadores. */
function SemCadastro({ frase, children }: { frase: string; children: ReactNode }) {
  return (
    <div className={styles.motivo}>
      <span>{frase}</span>
      <Separador />
      {children}
    </div>
  )
}

function Separador() {
  return (
    <span className={styles.separador} aria-hidden="true">
      ·
    </span>
  )
}

/** O traço do "não existe nesta casa" — o mesmo par da revisão de importação.
 *
 *  Não contradiz "zero é um valor, nunca travessão": lá o travessão
 *  substituiria um zero verdadeiro; aqui ele marca que o CONCEITO não existe. */
function SemValor() {
  return (
    <span className={styles.semValor}>
      <span aria-hidden="true">—</span>
      <span className="sr-only">sem valor</span>
    </span>
  )
}

function Esqueleto() {
  return <Skeleton width="4.5rem" height="1rem" />
}

// --------------------------------------------------------------- palavras

function fraseDaContagem(contagem: number): string {
  if (contagem === 0) return 'nenhum lançamento'
  if (contagem === 1) return '1 lançamento'
  return `${contagem} lançamentos`
}

/** A PALAVRA que acompanha o sinal do líquido (aceite 20 da spec 0008).
 *
 *  O negativo precisa ser legível por mais de um portador: o sinal, esta frase
 *  e o `sr-only` "negativos" do `MoneyText`. O zero COM movimento também ganha
 *  frase — `R$ 0,00` ao lado de "2 lançamentos", sem explicação, é contradição
 *  silenciosa. O zero sem movimento não ganha nada: "nenhum lançamento" já diz
 *  tudo.
 *
 *  Nenhuma delas conjuga o verbo "aportar" (proibido em qualquer tela);
 *  `aportes` e `resgates` são os substantivos ratificados na E7. */
function fraseDoLiquido(cents: number, contagem: number): string | null {
  if (cents < 0) return 'os resgates superaram os aportes'
  if (cents === 0 && contagem > 0) return 'aportes e resgates se anularam'
  return null
}
