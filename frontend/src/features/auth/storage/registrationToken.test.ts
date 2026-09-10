import { beforeEach, describe, expect, it, vi } from 'vitest'
import { clearRegistration, readRegistrationTokenFor, saveRegistration } from './registrationToken'

const TOKEN = 'a'.repeat(64)
const EMAIL = 'bruno@example.com'
const KEY = 'hf.registration'

describe('registrationToken', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    clearRegistration()
    vi.restoreAllMocks()
  })

  it('guarda e devolve o token do endereço que o pediu', () => {
    expect(saveRegistration(TOKEN, EMAIL)).toBe(TOKEN)
    expect(readRegistrationTokenFor(EMAIL)).toBe(TOKEN)
  })

  // O servidor procura a tentativa pelo par (e-mail, hash do token): token de
  // outro endereço não casa com linha nenhuma. Tratar como token nenhum aqui é
  // o que faz a tela oferecer a recuperação em vez de um beco sem saída.
  it('trata token de OUTRO endereço como token nenhum', () => {
    saveRegistration(TOKEN, EMAIL)
    expect(readRegistrationTokenFor('outra@example.com')).toBeNull()
    expect(readRegistrationTokenFor('')).toBeNull()
  })

  it('compara o endereço normalizado, como o backend faz', () => {
    saveRegistration(TOKEN, '  Bruno@Example.COM  ')
    expect(readRegistrationTokenFor(EMAIL)).toBe(TOKEN)
    expect(readRegistrationTokenFor('BRUNO@EXAMPLE.COM')).toBe(TOKEN)
  })

  it('nunca toca o localStorage — o par morre quando a aba fecha', () => {
    saveRegistration(TOKEN, EMAIL)
    expect(localStorage.length).toBe(0)
  })

  it('descarta token que não tenha a forma exata de 64 hexadecimais', () => {
    for (const lixo of ['', 'nao-e-hex', 'A'.repeat(64), `${TOKEN}f`, TOKEN.slice(1), null]) {
      expect(saveRegistration(lixo, EMAIL)).toBeNull()
      expect(readRegistrationTokenFor(EMAIL)).toBeNull()
    }
  })

  it('descarta registro corrompido no storage sem estourar', () => {
    for (const lixo of ['{', 'null', '{"token":"x","email":"a@b.co"}', '"só uma string"']) {
      clearRegistration()
      sessionStorage.setItem(KEY, lixo)
      expect(readRegistrationTokenFor(EMAIL)).toBeNull()
    }
  })

  it('apaga o par guardado', () => {
    saveRegistration(TOKEN, EMAIL)
    clearRegistration()
    expect(readRegistrationTokenFor(EMAIL)).toBeNull()
    expect(sessionStorage.getItem(KEY)).toBeNull()
  })

  // Sem isto, storage bloqueado deixava a pessoa num laço: cadastra, cai em
  // "refaça o cadastro", cadastra de novo, para sempre.
  it('sobrevive a storage bloqueado usando a memória da aba', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('QuotaExceededError')
    })
    expect(saveRegistration(TOKEN, EMAIL)).toBe(TOKEN)
    expect(readRegistrationTokenFor(EMAIL)).toBe(TOKEN)
    expect(readRegistrationTokenFor('outra@example.com')).toBeNull()
  })

  it('sobrevive a storage ilegível na leitura', () => {
    saveRegistration(TOKEN, EMAIL)
    clearRegistration()
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('SecurityError')
    })
    expect(readRegistrationTokenFor(EMAIL)).toBeNull()
  })
})
