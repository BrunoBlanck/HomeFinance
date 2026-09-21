import { describe, expect, it } from 'vitest'
import {
  capitalizar,
  contarLinhas,
  enumerarMeses,
  janelaDeTrabalho,
  nomeDoArquivoDoPrompt,
  rotuloDaJanela,
} from './janela'

/** A janela de trabalho é **uma** derivação para as três seções de `/ia`
 *  (spec 0010 §2.1). Estes casos são o contrato dela: se a seção Reprocessar
 *  vier amanhã e percorrer `janela.meses`, é esta lista que ela vai percorrer. */
describe('janelaDeTrabalho', () => {
  it('3 meses termina no mês escolhido e anda dois para trás', () => {
    expect(janelaDeTrabalho('2026-09', 3)).toEqual({
      fromMonth: '2026-07',
      toMonth: '2026-09',
      meses: ['2026-07', '2026-08', '2026-09'],
    })
  })

  it('2 meses', () => {
    expect(janelaDeTrabalho('2026-09', 2)).toEqual({
      fromMonth: '2026-08',
      toMonth: '2026-09',
      meses: ['2026-08', '2026-09'],
    })
  })

  it('1 mês é o próprio mês, nos dois extremos', () => {
    expect(janelaDeTrabalho('2026-09', 1)).toEqual({
      fromMonth: '2026-09',
      toMonth: '2026-09',
      meses: ['2026-09'],
    })
  })

  /** A virada de ano é o caso que uma subtração ingênua erra — e o erro seria
   *  silencioso: `2026-01` menos 2 daria `2026--1`, e o servidor recusaria com
   *  400 sem a tela saber por quê. */
  it('atravessa a virada de ano', () => {
    expect(janelaDeTrabalho('2026-01', 3)).toEqual({
      fromMonth: '2025-11',
      toMonth: '2026-01',
      meses: ['2025-11', '2025-12', '2026-01'],
    })
    expect(janelaDeTrabalho('2026-02', 3).meses).toEqual(['2025-12', '2026-01', '2026-02'])
    expect(janelaDeTrabalho('2026-01', 2)).toEqual({
      fromMonth: '2025-12',
      toMonth: '2026-01',
      meses: ['2025-12', '2026-01'],
    })
  })

  it('a lista vem do mais antigo para o mais novo, sempre com o tamanho pedido', () => {
    for (const tamanho of [1, 2, 3] as const) {
      const janela = janelaDeTrabalho('2026-03', tamanho)
      expect(janela.meses).toHaveLength(tamanho)
      expect(janela.meses[0]).toBe(janela.fromMonth)
      expect(janela.meses[janela.meses.length - 1]).toBe(janela.toMonth)
      expect([...janela.meses]).toEqual([...janela.meses].sort())
    }
  })
})

describe('rotuloDaJanela', () => {
  it('escreve os meses resolvidos, e o conectivo muda com o tamanho', () => {
    expect(rotuloDaJanela(janelaDeTrabalho('2026-09', 3))).toBe('julho a setembro')
    expect(rotuloDaJanela(janelaDeTrabalho('2026-09', 2))).toBe('agosto e setembro')
    expect(rotuloDaJanela(janelaDeTrabalho('2026-09', 1))).toBe('setembro')
  })

  it('na virada de ano nomeia os meses, não os números', () => {
    expect(rotuloDaJanela(janelaDeTrabalho('2026-01', 3))).toBe('novembro a janeiro')
  })
})

/** A enumeração é o que a seção Reprocessar diz e faz: "Reprocessar julho,
 *  agosto e setembro" nomeia cada mês que vai ser percorrido, ao contrário do
 *  intervalo de `rotuloDaJanela`. */
describe('enumerarMeses', () => {
  it('três meses: vírgula e "e"', () => {
    expect(enumerarMeses(janelaDeTrabalho('2026-09', 3).meses)).toBe('julho, agosto e setembro')
  })

  it('dois meses: só o "e"', () => {
    expect(enumerarMeses(janelaDeTrabalho('2026-09', 2).meses)).toBe('agosto e setembro')
  })

  it('um mês: o nome', () => {
    expect(enumerarMeses(janelaDeTrabalho('2026-09', 1).meses)).toBe('setembro')
  })

  it('atravessa a virada de ano sem mudar de forma', () => {
    expect(enumerarMeses(janelaDeTrabalho('2026-01', 3).meses)).toBe('novembro, dezembro e janeiro')
  })

  it('capitalizar só mexe na primeira letra', () => {
    expect(capitalizar('julho, agosto e setembro')).toBe('Julho, agosto e setembro')
    expect(capitalizar('')).toBe('')
  })
})

describe('nomeDoArquivoDoPrompt', () => {
  it('leva a janela no nome, e só mês, dígito e hífen entram nele', () => {
    const nome = nomeDoArquivoDoPrompt(janelaDeTrabalho('2026-09', 3))
    expect(nome).toBe('homefinance-prompt-2026-07-a-2026-09.md')
    expect(nome).toMatch(/^[a-z0-9-]+\.md$/)
  })
})

describe('contarLinhas', () => {
  it('conta as linhas do texto', () => {
    expect(contarLinhas('uma')).toBe(1)
    expect(contarLinhas('uma\nduas\ntrês')).toBe(3)
  })

  /** A quebra final não é uma linha a mais: um arquivo terminado em `\n` tem o
   *  mesmo número de linhas do mesmo arquivo sem ela, e o número aparece duas
   *  vezes na tela. */
  it('não conta a quebra final como linha', () => {
    expect(contarLinhas('uma\nduas\n')).toBe(2)
  })

  it('texto vazio é zero, não um', () => {
    expect(contarLinhas('')).toBe(0)
  })
})
