import { type Ref, useEffect, useId, useState } from 'react'
import { centavosDeDigitos, digitosParaTexto } from '@/lib/money'
import { FieldShell } from '../FieldShell/FieldShell'
import fieldStyles from '../FieldShell/FieldShell.module.css'
import styles from './MoneyInput.module.css'

type MoneyInputProps = {
  label: string
  /** Sempre em CENTAVOS, inteiro. Nunca reais, nunca fracionário (ADR-003). */
  value: number
  onChange: (centavos: number) => void
  hint?: string | undefined
  error?: string | undefined
  /** Permite valor negativo — é o caso do saldo de abertura de cartão, que
   *  nasce devendo. Desligado por padrão: a maioria dos campos de dinheiro do
   *  app é de módulo, e o sinal vem do tipo do lançamento. */
  allowNegative?: boolean | undefined
  ref?: Ref<HTMLInputElement> | undefined
  name?: string | undefined
  onBlur?: (() => void) | undefined
}

/** Campo de dinheiro, em centavos.
 *
 *  **Entrada centavos-primeiro.** Digitar 1, 2, 3, 4 vira 12,34 — os dígitos
 *  entram pela direita, empurrando a vírgula. É como funcionam o caixa do
 *  supermercado e o teclado numérico do celular, e resolve de uma vez o erro
 *  mais caro de um app de finanças: lançar R$ 1.500,00 no lugar de R$ 15,00
 *  porque a vírgula ficou no lugar errado. Não há vírgula para acertar.
 *
 *  O estado é o INTEIRO em centavos; o texto é só a projeção dele. Isso
 *  significa que não existe um instante sequer em que o valor viva como float.
 *
 *  `inputMode="decimal"` abre o teclado numérico no celular sem virar
 *  `type="number"` — que traria setas de incremento, rolagem acidental
 *  alterando o valor e notação científica em colagem. */
export function MoneyInput({
  label,
  value,
  onChange,
  hint,
  error,
  allowNegative = false,
  ref,
  name,
  onBlur,
}: MoneyInputProps) {
  const rawId = useId()
  const campoId = `money-${rawId.replace(/[^a-zA-Z0-9]/g, '')}`
  const mensagemId = `${campoId}-message`

  // O sinal é estado PRÓPRIO, e não `value < 0`, por um motivo concreto: em
  // JavaScript `-0 === 0`, então zero não consegue carregar sinal. Derivando do
  // valor, apertar "−" num campo zerado não fazia nada, e quem quisesse lançar
  // um saldo devedor tinha que digitar o número antes de escolher o sinal — uma
  // ordem que ninguém adivinha.
  const [negativo, setNegativo] = useState(value < 0)

  // Valor vindo de fora (abrir para editar) manda no botão. Zero é o único
  // caso que preserva a escolha em curso, porque zero não tem sinal.
  useEffect(() => {
    if (value < 0) setNegativo(true)
    else if (value > 0) setNegativo(false)
  }, [value])

  const texto = digitosParaTexto(value)

  function aplicar(digitado: string) {
    const modulo = centavosDeDigitos(digitado)
    onChange(negativo && allowNegative ? -modulo : modulo)
  }

  function alternarSinal() {
    const proximo = !negativo
    setNegativo(proximo)
    const modulo = Math.abs(value)
    onChange(proximo ? -modulo : modulo)
  }

  return (
    <FieldShell id={campoId} messageId={mensagemId} label={label} hint={hint} error={error}>
      <div className={styles.wrap}>
        {allowNegative ? (
          <button
            type="button"
            className={styles.sinal}
            onClick={alternarSinal}
            aria-pressed={negativo}
            // O rótulo diz o ESTADO e não a ação, porque `aria-pressed` já
            // comunica a ação. "Valor negativo, ativado" é o que se ouve.
            aria-label="Valor negativo"
          >
            <span aria-hidden="true">−</span>
          </button>
        ) : null}

        <span className={styles.moeda} aria-hidden="true">
          R$
        </span>

        <input
          ref={ref}
          id={campoId}
          name={name}
          type="text"
          inputMode="decimal"
          autoComplete="off"
          className={fieldStyles.control}
          value={texto}
          onChange={(event) => aplicar(event.target.value)}
          onBlur={onBlur}
          onFocus={(event) => event.target.select()}
          aria-describedby={mensagemId}
          aria-invalid={error ? true : undefined}
          data-negative={negativo || undefined}
        />
      </div>
    </FieldShell>
  )
}
