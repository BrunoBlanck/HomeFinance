import { describe, expect, it } from 'vitest'
import { accountFormSchema } from './account-schema'

/** O schema existe para não oferecer na tela um caminho que o servidor recusa.
 *  Estes testes prendem as três regras que ele espelha: allowlist de
 *  instituição, faixa 1–31 dos dias e dia de fatura só em cartão. */

const BASE = {
  name: 'Cartão C6',
  kind: 'credit_card' as const,
  institution: 'c6' as const,
  statementClosingDay: null as number | null,
  statementDueDay: null as number | null,
  openingBalanceCents: -80_000,
  openingDate: '2026-09-01',
  keywords: [] as string[],
}

/** Os campos que o schema reprovou — é o caminho do erro que importa, porque é
 *  ele que decide sob qual campo a frase aparece na tela. */
function camposReprovados(valores: Record<string, unknown>): string[] {
  const resultado = accountFormSchema.safeParse(valores)
  if (resultado.success) return []
  return resultado.error.issues.map((issue) => String(issue.path[0]))
}

describe('accountFormSchema', () => {
  it('aceita os extremos da faixa de dias num cartão', () => {
    const resultado = accountFormSchema.safeParse({
      ...BASE,
      statementClosingDay: 1,
      statementDueDay: 31,
    })
    expect(resultado.success).toBe(true)
  })

  it('recusa dia fora de 1 a 31', () => {
    expect(camposReprovados({ ...BASE, statementClosingDay: 0 })).toEqual(['statementClosingDay'])
    expect(camposReprovados({ ...BASE, statementDueDay: 32 })).toEqual(['statementDueDay'])
    expect(camposReprovados({ ...BASE, statementClosingDay: 1.5 })).toEqual(['statementClosingDay'])
  })

  it('aceita dias nulos: "não configurado" é estado legítimo', () => {
    expect(accountFormSchema.safeParse(BASE).success).toBe(true)
  })

  // A trava que o achado D5 destravou: sem ela, conta criada pela tela nasceria
  // com dia de fatura numa conta corrente e o servidor devolveria 422.
  it('recusa dia de fatura quando o tipo não é cartão, no campo certo', () => {
    const campos = camposReprovados({
      ...BASE,
      kind: 'checking',
      statementClosingDay: 3,
      statementDueDay: 10,
    })
    expect(campos).toEqual(['statementClosingDay', 'statementDueDay'])
  })

  it('aceita conta que não é cartão com os dias nulos', () => {
    const resultado = accountFormSchema.safeParse({ ...BASE, kind: 'checking' })
    expect(resultado.success).toBe(true)
  })

  it('a instituição é allowlist fechada — texto livre não passa', () => {
    expect(camposReprovados({ ...BASE, institution: 'itau' })).toEqual(['institution'])
    expect(camposReprovados({ ...BASE, institution: '' })).toEqual(['institution'])
    for (const banco of ['c6', 'nubank', 'other']) {
      expect(accountFormSchema.safeParse({ ...BASE, institution: banco }).success).toBe(true)
    }
  })

  it('o tipo de conta também é allowlist', () => {
    expect(camposReprovados({ ...BASE, kind: 'crypto' })).toContain('kind')
  })

  it('entrega o nome já sem espaço nas pontas', () => {
    const resultado = accountFormSchema.safeParse({ ...BASE, name: '  Cartão C6  ' })
    expect(resultado.success && resultado.data.name).toBe('Cartão C6')
  })

  it('recusa nome vazio e data que não é AAAA-MM-DD', () => {
    expect(camposReprovados({ ...BASE, name: '   ' })).toEqual(['name'])
    expect(camposReprovados({ ...BASE, openingDate: '01/09/2026' })).toEqual(['openingDate'])
  })

  it('recusa valor de abertura fora da faixa, e aceita negativo', () => {
    expect(camposReprovados({ ...BASE, openingBalanceCents: 100_000_000_000 })).toEqual([
      'openingBalanceCents',
    ])
    expect(accountFormSchema.safeParse({ ...BASE, openingBalanceCents: -1 }).success).toBe(true)
  })

  // Spec 0005 §4.1: até 20 palavras-chave, cada uma válida; a lista vai
  // sempre, inteira — é substituição, e `[]` limpa.
  it('aceita até 20 palavras-chave e aponta a inválida pelo índice', () => {
    const vinte = Array.from({ length: 20 }, (_, i) => `loja ${i + 1}`)
    expect(accountFormSchema.safeParse({ ...BASE, keywords: vinte }).success).toBe(true)
    expect(camposReprovados({ ...BASE, keywords: [...vinte, 'loja 21'] })).toEqual(['keywords'])

    const resultado = accountFormSchema.safeParse({ ...BASE, keywords: ['nubank', '<b>'] })
    expect(resultado.success).toBe(false)
    if (!resultado.success) {
      expect(resultado.error.issues.map((issue) => issue.path)).toEqual([['keywords', 1]])
    }
  })
})
