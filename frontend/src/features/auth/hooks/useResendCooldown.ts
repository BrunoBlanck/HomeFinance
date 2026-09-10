import { useCallback, useEffect, useState } from 'react'

/** Escalonamento conversando com o rate limit do backend. */
const STEPS = [60, 120, 240, 300] as const

/** Só o instante do próximo envio, sem o e-mail junto: o storage do navegador
 *  não é lugar de guardar quem está tentando entrar. */
const DEADLINE_KEY = 'hf.resend.deadline'
const STEP_KEY = 'hf.resend.step'

function readNumber(key: string): number {
  try {
    const raw = sessionStorage.getItem(key)
    if (raw === null) return 0
    const parsed = Number(raw)
    return Number.isFinite(parsed) ? parsed : 0
  } catch {
    return 0
  }
}

function writeNumber(key: string, value: number): void {
  try {
    sessionStorage.setItem(key, String(value))
  } catch {
    // Sem storage a espera vale só enquanto a tela viver. Melhor que quebrar.
  }
}

/** Zera a espera guardada. Chamado quando um fluxo NOVO começa (cadastro ou
 *  pedido de recuperação), para o usuário não herdar o degrau de outra conta. */
export function clearResendCooldown(): void {
  try {
    sessionStorage.removeItem(DEADLINE_KEY)
    sessionStorage.removeItem(STEP_KEY)
  } catch {
    // Sem storage não há o que limpar.
  }
}

function secondsUntil(deadline: number): number {
  return Math.max(0, Math.ceil((deadline - Date.now()) / 1000))
}

export type ResendCooldown = {
  secondsLeft: number
  /** Reinicia a espera avançando um degrau (60 → 120 → 240 → 300). */
  startNextCooldown: () => void
}

export function useResendCooldown(): ResendCooldown {
  const [secondsLeft, setSecondsLeft] = useState(() => secondsUntil(readNumber(DEADLINE_KEY)))

  // Quem chega nesta tela acabou de receber um código: a espera já começa
  // correndo, no primeiro degrau. Recarregar a página não zera nada.
  useEffect(() => {
    if (readNumber(DEADLINE_KEY) > Date.now()) return
    const [first] = STEPS
    writeNumber(DEADLINE_KEY, Date.now() + first * 1000)
    writeNumber(STEP_KEY, 0)
    setSecondsLeft(first)
  }, [])

  useEffect(() => {
    const timer = window.setInterval(() => {
      setSecondsLeft(secondsUntil(readNumber(DEADLINE_KEY)))
    }, 1000)
    return () => window.clearInterval(timer)
  }, [])

  const startNextCooldown = useCallback(() => {
    const nextStep = Math.min(readNumber(STEP_KEY) + 1, STEPS.length - 1)
    const seconds = STEPS[nextStep] ?? STEPS[STEPS.length - 1] ?? 300
    writeNumber(STEP_KEY, nextStep)
    writeNumber(DEADLINE_KEY, Date.now() + seconds * 1000)
    setSecondsLeft(seconds)
  }, [])

  return { secondsLeft, startNextCooldown }
}
