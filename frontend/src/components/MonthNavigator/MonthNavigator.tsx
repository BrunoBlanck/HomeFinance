import { mesPorExtenso, mesPorExtensoCapitalizado, somarMeses } from '@/lib/month'
import { ChevronLeftIcon } from '../icons/ChevronLeftIcon'
import { ChevronRightIcon } from '../icons/ChevronRightIcon'
import styles from './MonthNavigator.module.css'

type MonthNavigatorProps = {
  /** `YYYY-MM`. */
  value: string
  onChange: (mes: string) => void
  /** Volta para o mês corrente. Ausente quando já se está nele. */
  onToday?: (() => void) | undefined
}

/** Seletor de mês do cabeçalho.
 *
 *  O mês é o eixo do app inteiro: ele vive na URL (`?mes=2026-09`) e é o mesmo
 *  entre telas, então trocar de mês no painel e ir para lançamentos mantém o
 *  mês. Este componente só emite a mudança — quem grava na URL é a casca.
 *
 *  O rótulo é `<output aria-live="polite">`: sem isso, quem usa leitor de tela
 *  aperta "mês anterior" e não ouve nada, porque o foco continua no botão e o
 *  texto que mudou está fora dele. */
export function MonthNavigator({ value, onChange, onToday }: MonthNavigatorProps) {
  const anterior = somarMeses(value, -1)
  const proximo = somarMeses(value, 1)

  return (
    <div className={styles.navigator}>
      <button
        type="button"
        className={styles.seta}
        onClick={() => onChange(anterior)}
        aria-label={`Mês anterior, ${mesPorExtenso(anterior)}`}
      >
        <ChevronLeftIcon size={18} />
      </button>

      <output className={styles.rotulo} aria-live="polite">
        {mesPorExtensoCapitalizado(value)}
      </output>

      <button
        type="button"
        className={styles.seta}
        onClick={() => onChange(proximo)}
        aria-label={`Próximo mês, ${mesPorExtenso(proximo)}`}
      >
        <ChevronRightIcon size={18} />
      </button>

      {onToday ? (
        <button type="button" className={styles.hoje} onClick={onToday}>
          Mês atual
        </button>
      ) : null}
    </div>
  )
}
