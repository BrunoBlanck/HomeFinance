import { formatarDinheiro, formatarValor, tomDoValor } from '@/lib/money'
import styles from './MoneyText.module.css'

type MoneyTextProps = {
  /** Sempre em CENTAVOS, inteiro (ADR-003). */
  cents: number
  /** `plain` omite o "R$" — para coluna de tabela cujo cabeçalho já diz que é
   *  dinheiro. `currency` traz o símbolo. */
  format?: 'currency' | 'plain' | undefined
  /** `semantic` pinta receita e despesa; `neutral` deixa na cor do texto.
   *
   *  Neutro é o padrão de propósito: `--income` e `--expense` são reservados a
   *  SIGNIFICADO financeiro (docs/DESIGN.md, princípio 3), e saldo de conta não
   *  é receita nem despesa — é uma posição. Pintar tudo de verde e vermelho
   *  gasta o significado das duas cores. */
  tone?: 'neutral' | 'semantic' | undefined
  /** `always` acrescenta o `+` no positivo: `+1.600,00` / `-11,00`.
   *
   *  É o que usar numa coluna onde receita e despesa convivem. Ali a direção do
   *  dinheiro não pode depender da cor — em escala de cinza, no daltonismo ou
   *  impresso, `--income` e `--expense` viram o mesmo tom, e sem o sinal a
   *  coluna deixa de ser legível.
   *
   *  `auto` é o padrão: coluna de saldo, de total ou de valor absoluto não ganha
   *  `+` nenhum, e nada do que já existe muda. */
  sign?: 'auto' | 'always' | undefined
  /** `total` destaca a linha de total de uma tabela. `hero` é o número que
   *  mora no centro da rosca (docs/DESIGN.md, E6a): `--text-22`, peso 700 —
   *  o corpo é ditado pelo furo, que precisa caber `123.456,78`. */
  emphasis?: 'normal' | 'total' | 'hero' | undefined
}

/** Valor monetário.
 *
 *  Três coisas que este componente garante e que espalhar `Intl` pelas telas
 *  não garantiria:
 *
 *  1. **Dígitos tabulares.** Sem eles, as colunas de uma tabela de valores não
 *     alinham e a leitura vertical — que é a razão de existir a tabela — some.
 *  2. **O sinal negativo aparece, sempre.** Saldo devedor comunicado só por
 *     cor é invisível para quem não distingue vermelho de verde, e some numa
 *     impressão em preto e branco.
 *  3. **O valor sai legível para leitor de tela.** "-R$ 80,00" seria lido como
 *     "traço erre cifrão oitenta"; o `aria-label` diz "80 reais negativos". */
export function MoneyText({
  cents,
  format = 'plain',
  tone = 'neutral',
  sign = 'auto',
  emphasis = 'normal',
}: MoneyTextProps) {
  const tomSemantico = tomDoValor(cents)
  const opcoes = sign === 'always' ? ({ sinal: 'sempre' } as const) : undefined
  const texto =
    format === 'currency' ? formatarDinheiro(cents, opcoes) : formatarValor(cents, opcoes)

  return (
    <span
      className={styles.money}
      data-tone={tone === 'semantic' ? tomSemantico : 'neutral'}
      data-emphasis={emphasis}
    >
      {/* Texto visível escondido do leitor de tela, e uma frase legível ao
          lado. `aria-label` num <span> sem papel ARIA não é exposto de forma
          confiável (é o que o lint acusa, e com razão) — o par
          aria-hidden + sr-only funciona em qualquer combinação de navegador e
          leitor. Sem isso, "-80,00" sai como "traço oitenta". */}
      <span aria-hidden="true">{texto}</span>
      <span className="sr-only">{rotuloAcessivel(cents, sign)}</span>
    </span>
  )
}

/** O que o leitor de tela recebe precisa dizer exatamente o que a tela mostra.
 *
 *  Por isso "positivos" acompanha o `sign`, e não o valor: numa coluna `auto`,
 *  quem enxerga não vê `+` nenhum, e anunciar "positivos" daria a quem ouve uma
 *  informação que a tela não deu. Numa coluna `always` é o contrário — o `+`
 *  está lá para todo mundo, e omiti-lo no áudio entregaria metade da direção do
 *  dinheiro. O zero não ganha palavra em nenhum dos dois casos: "zero reais
 *  positivos" sugere um movimento que não houve. */
function rotuloAcessivel(cents: number, sign: 'auto' | 'always'): string {
  const absoluto = formatarDinheiro(Math.abs(cents))
  if (cents < 0) return `${absoluto} negativos`
  if (cents > 0 && sign === 'always') return `${absoluto} positivos`
  return absoluto
}
