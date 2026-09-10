import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearResendCooldown, useResendCooldown } from './useResendCooldown'

describe('useResendCooldown', () => {
  beforeEach(() => {
    sessionStorage.clear()
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('começa com 60 segundos e conta para baixo', () => {
    const { result } = renderHook(() => useResendCooldown())
    expect(result.current.secondsLeft).toBe(60)

    act(() => {
      vi.advanceTimersByTime(5000)
    })
    expect(result.current.secondsLeft).toBe(55)
  })

  it('escalona 60 → 120 → 240 → 300 e para no teto', () => {
    const { result } = renderHook(() => useResendCooldown())

    act(() => result.current.startNextCooldown())
    expect(result.current.secondsLeft).toBe(120)

    act(() => result.current.startNextCooldown())
    expect(result.current.secondsLeft).toBe(240)

    act(() => result.current.startNextCooldown())
    expect(result.current.secondsLeft).toBe(300)

    act(() => result.current.startNextCooldown())
    expect(result.current.secondsLeft).toBe(300)
  })

  it('sobrevive a um recarregamento sem reiniciar a espera', () => {
    const first = renderHook(() => useResendCooldown())
    act(() => {
      vi.advanceTimersByTime(10_000)
    })
    expect(first.result.current.secondsLeft).toBe(50)
    first.unmount()

    const second = renderHook(() => useResendCooldown())
    expect(second.result.current.secondsLeft).toBe(50)
  })

  it('guarda apenas números — nunca o e-mail de quem está tentando entrar', () => {
    renderHook(() => useResendCooldown())

    const stored = Object.keys(sessionStorage).map((key) => sessionStorage.getItem(key) ?? '')
    expect(stored.length).toBeGreaterThan(0)
    for (const value of stored) {
      expect(value).toMatch(/^\d+$/)
    }
  })

  it('um fluxo novo zera a espera herdada', () => {
    const first = renderHook(() => useResendCooldown())
    act(() => first.result.current.startNextCooldown())
    expect(first.result.current.secondsLeft).toBe(120)
    first.unmount()

    clearResendCooldown()

    const second = renderHook(() => useResendCooldown())
    expect(second.result.current.secondsLeft).toBe(60)
  })
})
