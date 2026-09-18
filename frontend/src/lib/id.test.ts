import { describe, expect, it } from 'vitest'
import { uuidDaURL, uuidValido } from './id'

describe('uuidValido', () => {
  it('aceita o UUID v7 que o backend emite', () => {
    expect(uuidValido('0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55')).toBe(true)
  })

  it('aceita o v4 do caminho de contingência do backend', () => {
    expect(uuidValido('22222222-2222-4222-8222-222222222222')).toBe(true)
  })

  it('aceita maiúsculas', () => {
    expect(uuidValido('0199A0F1-7C3E-7A2B-9F41-2F6F1C9A8D55')).toBe(true)
  })

  it('recusa o que não tem a forma', () => {
    for (const ruim of [
      '',
      'acc-1',
      '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d5',
      '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d555',
      '0199a0f1_7c3e_7a2b_9f41_2f6f1c9a8d55',
      'gggggggg-7c3e-7a2b-9f41-2f6f1c9a8d55',
      '<script>alert(1)</script>',
      "' OR 1=1 --",
    ]) {
      expect(uuidValido(ruim), ruim).toBe(false)
    }
  })

  it('recusa variante fora da RFC 4122', () => {
    expect(uuidValido('0199a0f1-7c3e-7a2b-1f41-2f6f1c9a8d55')).toBe(false)
  })
})

describe('uuidDaURL', () => {
  it('deixa passar o id válido e descarta o resto', () => {
    expect(uuidDaURL('0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55')).toBe(
      '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55',
    )
    expect(uuidDaURL('<script>')).toBeUndefined()
    expect(uuidDaURL(undefined)).toBeUndefined()
    expect(uuidDaURL(42)).toBeUndefined()
    expect(uuidDaURL(['0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'])).toBeUndefined()
  })
})
