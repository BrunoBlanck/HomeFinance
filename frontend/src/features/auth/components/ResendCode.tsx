import { useEffect, useRef, useState } from 'react'
import { Button } from '@/components/Button/Button'
import { formatCountdown } from '@/lib/format'
import styles from './ResendCode.module.css'

type ResendCodeProps = {
  secondsLeft: number
  busy: boolean
  onResend: () => void
}

/** O contador NUNCA é live region — seriam sessenta anúncios seguidos. O que
 *  fala é um `role="status"` que só emite nos dois momentos que importam:
 *  quando o usuário tenta antes da hora e quando a espera termina. */
export function ResendCode({ secondsLeft, busy, onResend }: ResendCodeProps) {
  const waiting = secondsLeft > 0
  const [status, setStatus] = useState('')
  const wasWaiting = useRef(waiting)

  useEffect(() => {
    if (secondsLeft > 0) {
      wasWaiting.current = true
      return
    }
    if (wasWaiting.current) {
      wasWaiting.current = false
      setStatus('Você já pode pedir um novo código.')
    }
  }, [secondsLeft])

  function handleClick() {
    if (waiting) {
      const unit = secondsLeft === 1 ? 'segundo' : 'segundos'
      setStatus(
        `Ainda ${secondsLeft === 1 ? 'falta' : 'faltam'} ${secondsLeft} ${unit} para pedir outro código.`,
      )
      return
    }
    setStatus('')
    onResend()
  }

  return (
    <>
      {/* Continua focável durante a espera: sumir com o botão é pior do que
          explicar por que ele ainda não funciona. */}
      <Button
        variant="quiet"
        onClick={handleClick}
        loading={busy}
        aria-disabled={waiting ? true : undefined}
      >
        Enviar outro código
      </Button>
      {waiting ? (
        <span className={styles.countdown} aria-hidden="true">
          disponível em {formatCountdown(secondsLeft)}
        </span>
      ) : null}
      <span className="sr-only" role="status">
        {status}
      </span>
    </>
  )
}
