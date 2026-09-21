import { describe, expect, it } from 'vitest'
import type {
  ApiErrorCode,
  HouseholdRole,
  ResendCodeInput,
  Session,
  VerifyEmailInput,
} from './types'

/** Estes testes guardam a **derivação** do contrato (ADR-015), não o
 *  comportamento de nenhuma tela.
 *
 *  As afirmações que importam são de tipo, e quem as executa é o
 *  `tsc --noEmit` do `npm run build` — os `@ts-expect-error` abaixo falham a
 *  compilação se o erro que eles esperam deixar de acontecer. O corpo dos `it`
 *  existe para que o arquivo também apareça no `npm test` e ninguém o apague
 *  achando que é morto.
 *
 *  O que quebraria sem isto: alguém "conserta" um erro de tipo trocando um
 *  tipo derivado por `string` ou por um literal escrito à mão, o `tsc` fica
 *  verde, e o frontend volta a ter uma cópia do contrato que envelhece
 *  sozinha — que é exatamente o problema que o ADR-015 resolveu. */

describe('tipos derivados do contrato OpenAPI', () => {
  it('ApiErrorCode aceita os códigos da spec e recusa o que ela não define', () => {
    const conhecido: ApiErrorCode = 'VALIDATION_FAILED'
    // @ts-expect-error código inexistente na spec não pode ser um ApiErrorCode
    const inventado: ApiErrorCode = 'ERRO_QUE_NAO_EXISTE'

    expect(conhecido).toBe('VALIDATION_FAILED')
    expect(inventado).toBe('ERRO_QUE_NAO_EXISTE')
  })

  it('o papel na casa é uma união fechada, não string', () => {
    const owner: HouseholdRole = 'owner'
    const member: HouseholdRole = 'member'
    // @ts-expect-error a spec só define owner e member
    const admin: HouseholdRole = 'admin'

    expect([owner, member, admin]).toEqual(['owner', 'member', 'admin'])
  })

  it('verify-email exige o registrationToken (ADR-014)', () => {
    const completo: VerifyEmailInput = {
      email: 'ana@exemplo.com.br',
      code: '012345',
      registrationToken: 'a'.repeat(64),
    }
    // @ts-expect-error sem registrationToken o backend responde 400
    const semToken: VerifyEmailInput = { email: 'ana@exemplo.com.br', code: '012345' }

    expect(completo.registrationToken).toHaveLength(64)
    expect(semToken.email).toBe('ana@exemplo.com.br')
  })

  it('resend-code exige o token no cliente, mesmo sendo opcional na spec', () => {
    // A spec marca o campo como opcional de propósito (o servidor responde o
    // mesmo 202 sem ele, só não emite nada). O cliente é mais estrito porque
    // chamar sem token é sempre bug — ver comentário em types.ts.
    // @ts-expect-error chamada sem token gastaria a requisição sem enviar e-mail
    const semToken: ResendCodeInput = { email: 'ana@exemplo.com.br' }

    expect(semToken.email).toBe('ana@exemplo.com.br')
  })

  it('a sessão traz usuário, casa corrente e a lista de casas', () => {
    const sessao: Session = {
      user: {
        id: '0192f0c0-0000-7000-8000-000000000001',
        name: 'Ana',
        email: 'ana@exemplo.com.br',
        emailVerifiedAt: '2026-09-12T10:00:00Z',
        createdAt: '2026-09-12T09:00:00Z',
      },
      household: {
        id: '0192f0c0-0000-7000-8000-000000000002',
        name: 'Casa de Ana',
        role: 'owner',
        timezone: 'America/Sao_Paulo',
        currency: 'BRL',
      },
      households: [
        {
          id: '0192f0c0-0000-7000-8000-000000000002',
          name: 'Casa de Ana',
          role: 'owner',
          timezone: 'America/Sao_Paulo',
          currency: 'BRL',
        },
      ],
    }

    expect(sessao.household.role).toBe('owner')
    expect(sessao.households).toHaveLength(1)
  })
})
