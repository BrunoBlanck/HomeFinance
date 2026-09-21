import { describe, expect, it } from 'vitest'
import {
  citarPalavra,
  indiceDaPalavra,
  keywordsSchema,
  MAX_FICHAS,
  MOTIVO,
  normalizarPalavra,
  palavrasParaAprender,
  STOPWORDS,
  tokenizar,
  validarPalavra,
} from './keywords'

describe('normalizarPalavra', () => {
  it('tira acento, baixa a caixa e colapsa espaços', () => {
    expect(normalizarPalavra('  Padaria   São  José ')).toBe('padaria sao jose')
    expect(normalizarPalavra('NETFLIX')).toBe('netflix')
    expect(normalizarPalavra('Açaí')).toBe('acai')
  })

  it('é a forma pela qual duas palavras são a mesma', () => {
    expect(normalizarPalavra('Padaria')).toBe(normalizarPalavra('PADARIA'))
    expect(normalizarPalavra('pão')).toBe(normalizarPalavra('pao'))
  })
})

describe('STOPWORDS', () => {
  // A lista é fechada e idêntica à do backend (`textmatch.Stopwords`). Uma
  // divergência faria a tela sugerir uma palavra que o servidor descarta.
  it('é exatamente a lista da spec 0005 §3', () => {
    const daSpec =
      'de do da dos das e o a os as em no na nos nas um uma por para com sem seu sua ltda me sa eireli epp'
    expect([...STOPWORDS].sort()).toEqual(daSpec.split(' ').sort())
  })
})

describe('tokenizar', () => {
  it('separa por não letra/dígito e descarta curtas e vazias', () => {
    expect(tokenizar('Mercado do seu José')).toEqual(['mercado', 'jose'])
    expect(tokenizar('uber*eats 12/09')).toEqual(['uber', 'eats', '12', '09'])
    expect(tokenizar('c & a')).toEqual([])
    expect(tokenizar('LTDA ME')).toEqual([])
  })

  it('devolve lista vazia para texto vazio', () => {
    expect(tokenizar('')).toEqual([])
    expect(tokenizar('   ')).toEqual([])
  })
})

describe('validarPalavra', () => {
  it('aceita a palavra como digitada, só sem espaço sobrando', () => {
    expect(validarPalavra('  Padaria  São José ')).toEqual({
      ok: true,
      palavra: 'Padaria São José',
      normalizada: 'padaria sao jose',
    })
    expect(validarPalavra('c6').ok).toBe(true)
    expect(validarPalavra("pão d'açúcar").ok).toBe(true)
    expect(validarPalavra('netflix.com').ok).toBe(true)
    expect(validarPalavra('uber/99').ok).toBe(true)
    expect(validarPalavra('ab-cd').ok).toBe(true)
  })

  it('recusa curta demais — abaixo de 2 runas', () => {
    expect(validarPalavra('a')).toEqual({ ok: false, motivo: MOTIVO.curta })
    expect(validarPalavra('')).toEqual({ ok: false, motivo: MOTIVO.curta })
    expect(validarPalavra('   ')).toEqual({ ok: false, motivo: MOTIVO.curta })
  })

  it('recusa longa demais — acima de 40 runas', () => {
    expect(validarPalavra('a'.repeat(40)).ok).toBe(true)
    expect(validarPalavra('a'.repeat(41))).toEqual({ ok: false, motivo: MOTIVO.longa })
    // Conta em runas, não em bytes nem em unidades UTF-16: 40 «ç» passam.
    expect(validarPalavra('ç'.repeat(40)).ok).toBe(true)
  })

  it('recusa caractere fora da allowlist', () => {
    expect(validarPalavra('<script>')).toEqual({ ok: false, motivo: MOTIVO.caractere })
    expect(validarPalavra('pix; drop')).toEqual({ ok: false, motivo: MOTIVO.caractere })
    expect(validarPalavra('mercado_livre')).toEqual({ ok: false, motivo: MOTIVO.caractere })
  })

  it('recusa o que só tem palavras vazias ou letras soltas', () => {
    expect(validarPalavra('de')).toEqual({ ok: false, motivo: MOTIVO.vazia })
    expect(validarPalavra('c & a')).toEqual({ ok: false, motivo: MOTIVO.vazia })
    expect(validarPalavra('ltda')).toEqual({ ok: false, motivo: MOTIVO.vazia })
    expect(validarPalavra('&&')).toEqual({ ok: false, motivo: MOTIVO.vazia })
  })
})

describe('indiceDaPalavra', () => {
  it('encontra pela forma normalizada, não pela digitada', () => {
    const lista = ['Padaria', 'Uber Eats', 'Açaí']
    expect(indiceDaPalavra(lista, 'padaria')).toBe(0)
    expect(indiceDaPalavra(lista, 'uber eats')).toBe(1)
    expect(indiceDaPalavra(lista, 'acai')).toBe(2)
    expect(indiceDaPalavra(lista, 'netflix')).toBe(-1)
  })
})

describe('keywordsSchema', () => {
  it('aceita até 20 palavras válidas', () => {
    const vinte = Array.from({ length: 20 }, (_, i) => `loja ${i + 1}`)
    expect(keywordsSchema.safeParse(vinte).success).toBe(true)
    expect(keywordsSchema.safeParse([]).success).toBe(true)
  })

  it('recusa a 21ª e aponta a palavra inválida pelo índice', () => {
    const vinteUma = Array.from({ length: 21 }, (_, i) => `loja ${i + 1}`)
    expect(keywordsSchema.safeParse(vinteUma).success).toBe(false)

    const resultado = keywordsSchema.safeParse(['padaria', 'x'])
    expect(resultado.success).toBe(false)
    if (!resultado.success) {
      expect(resultado.error.issues[0]?.path).toEqual([1])
      expect(resultado.error.issues[0]?.message).toBe(MOTIVO.curta)
    }
  })
})

describe('citarPalavra', () => {
  it('põe a palavra entre aspas angulares, como ela aparece em toda frase do app', () => {
    expect(citarPalavra('mercado')).toBe('«mercado»')
    expect(citarPalavra('Padaria São José')).toBe('«Padaria São José»')
  })
})

describe('palavrasParaAprender', () => {
  const categoria = (keywords: string[] = []) => ({ keywords })

  it('são os tokens da descrição, na ordem, sem palavras vazias', () => {
    expect(palavrasParaAprender('Mercado do seu José LTDA', categoria())).toEqual([
      'mercado',
      'jose',
    ])
  })

  it('no máximo cinco', () => {
    expect(MAX_FICHAS).toBe(5)
    expect(
      palavrasParaAprender('um dois tres quatro cinco seis sete oito', categoria()),
    ).toHaveLength(5)
  })

  it('deixa de fora os só de dígitos e os longos demais', () => {
    const longa = 'a'.repeat(41)
    expect(palavrasParaAprender(`Pedido 123456 ${longa} padaria`, categoria())).toEqual([
      'pedido',
      'padaria',
    ])
  })

  it('deixa de fora o que a categoria já tem — comparando pela forma normalizada', () => {
    expect(palavrasParaAprender('Padaria Pão Doce', categoria(['PADARIA', 'pão']))).toEqual([
      'doce',
    ])
  })

  it('não repete o mesmo token duas vezes', () => {
    expect(palavrasParaAprender('uber uber trip', categoria())).toEqual(['uber', 'trip'])
  })

  it('nenhuma ficha quando a categoria já tem 20 palavras', () => {
    const cheia = categoria(Array.from({ length: 20 }, (_, i) => `p${i}`))
    expect(palavrasParaAprender('mercado novo', cheia)).toEqual([])
  })

  it('a palavra já aprendida nesta linha fica, mesmo depois de entrar na categoria', () => {
    // Depois da invalidação a palavra está na categoria; a ficha desta linha
    // precisa continuar visível como "feito", não sumir.
    expect(palavrasParaAprender('mercado novo', categoria(['mercado']), ['mercado'])).toEqual([
      'mercado',
      'novo',
    ])
    // E com a categoria no limite, só a aprendida — nenhuma ficha nova.
    const cheia = categoria([...Array.from({ length: 19 }, (_, i) => `p${i}`), 'mercado'])
    expect(palavrasParaAprender('mercado novo', cheia, ['mercado'])).toEqual(['mercado'])
  })

  it('descrição só com dígitos não rende ficha nenhuma', () => {
    expect(palavrasParaAprender('123 456', categoria())).toEqual([])
    expect(palavrasParaAprender('', categoria())).toEqual([])
  })
})
