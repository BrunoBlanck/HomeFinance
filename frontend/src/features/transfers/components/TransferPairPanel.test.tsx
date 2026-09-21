import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { TransferPair } from '@/api/types'
import { fraseDeDirecao, orientarPar, TransferPairPanel } from './TransferPairPanel'

const NUBANK = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
const C6 = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d66'

/** O servidor orienta por A = MENOR id: aqui A é a Nubank. A → B = 2.500,
 *  B → A = 800, líquido = +1.700 ("A enviou mais para B"). */
const PAR: TransferPair = {
  accountAId: NUBANK,
  accountAName: 'Nubank',
  accountBId: C6,
  accountBName: 'C6',
  aToBCents: 250_000,
  bToACents: 80_000,
  netCents: 170_000,
  count: 4,
}

const SALDOS = [
  { accountId: NUBANK, accountName: 'Nubank', balanceAtMonthEndCents: 412_000 },
  { accountId: C6, accountName: 'C6', balanceAtMonthEndCents: -198_050 },
]

describe('orientarPar', () => {
  it('com a conta do filtro sendo A, o líquido mostrado é +netCents', () => {
    expect(orientarPar(PAR, NUBANK)).toEqual({
      xParaY: 250_000,
      yParaX: 80_000,
      liquido: PAR.netCents,
    })
  })

  it('com a conta do filtro sendo B, os sentidos trocam e o líquido é −netCents', () => {
    expect(orientarPar(PAR, C6)).toEqual({
      xParaY: 80_000,
      yParaX: 250_000,
      liquido: -PAR.netCents,
    })
  })

  it('líquido zero visto de B continua +0, nunca −0 (que sairia "-0,00")', () => {
    const empate = { ...PAR, aToBCents: 1000, bToACents: 1000, netCents: 0 }
    const orientado = orientarPar(empate, C6)
    expect(orientado?.liquido).toBe(0)
    expect(Object.is(orientado?.liquido, -0)).toBe(false)
  })

  it('par que não envolve a conta não é orientado — direção errada é o pior erro', () => {
    expect(orientarPar(PAR, '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d77')).toBeNull()
  })
})

describe('fraseDeDirecao', () => {
  it('fala na voz de quem mandou mais', () => {
    render(<p>{fraseDeDirecao('Nubank', 'C6', 170_000, 'setembro')}</p>)
    expect(screen.getByText(/enviou/).textContent).toMatch(
      /^Nubank enviou R\$\s1\.700,00 a mais para C6 em setembro\.$/,
    )
  })

  it('líquido negativo inverte quem fala', () => {
    render(<p>{fraseDeDirecao('Nubank', 'C6', -170_000, 'setembro')}</p>)
    expect(screen.getByText(/enviou/).textContent).toMatch(
      /^C6 enviou R\$\s1\.700,00 a mais para Nubank em setembro\.$/,
    )
  })

  it('líquido zero: equilíbrio, sem inventar direção', () => {
    render(<p>{fraseDeDirecao('Nubank', 'C6', 0, 'setembro')}</p>)
    expect(
      screen.getByText('As duas contas se equilibraram em setembro: o que foi, voltou.'),
    ).toBeInTheDocument()
  })
})

describe('TransferPairPanel', () => {
  it('orienta as duas <dl> pela conta do filtro, não pelo A do servidor', () => {
    render(
      <TransferPairPanel
        conta={{ id: C6, nome: 'C6' }}
        contraparte={{ id: NUBANK, nome: 'Nubank' }}
        par={PAR}
        saldos={SALDOS}
        mes="2026-09"
        carregando={false}
      />,
    )

    // Primeira linha é sempre "X → Y", com X = a conta do filtro.
    const termos = screen.getAllByRole('term').map((el) => el.textContent)
    expect(termos).toEqual(['C6 → Nubank', 'Nubank → C6', 'Líquido', 'C6', 'Nubank'])

    const definicoes = screen.getAllByRole('definition')
    expect(definicoes[0]).toHaveTextContent('800,00')
    expect(definicoes[1]).toHaveTextContent('2.500,00')
    // −netCents, com sinal explícito: C6 recebeu mais do que mandou.
    expect(definicoes[2]).toHaveTextContent('-1.700,00')

    // A frase é de quem mandou mais — a Nubank, mesmo com o filtro pela C6.
    expect(screen.getByText(/enviou/).textContent).toMatch(
      /^Nubank enviou R\$\s1\.700,00 a mais para C6 em setembro\.$/,
    )

    // Saldos no fim do mês, um por conta, na ordem do filtro.
    expect(screen.getByText('Saldo no fim de setembro')).toBeInTheDocument()
    expect(definicoes[3]).toHaveTextContent('-1.980,50')
    expect(definicoes[4]).toHaveTextContent('4.120,00')
  })

  it('o líquido é neutro e só o saldo é semântico', () => {
    render(
      <TransferPairPanel
        conta={{ id: NUBANK, nome: 'Nubank' }}
        contraparte={{ id: C6, nome: 'C6' }}
        par={PAR}
        saldos={SALDOS}
        mes="2026-09"
        carregando={false}
      />,
    )
    const definicoes = screen.getAllByRole('definition')
    const liquido = definicoes[2]?.querySelector('[data-tone]')
    expect(liquido).toHaveAttribute('data-tone', 'neutral')
    expect(liquido).toHaveAttribute('data-emphasis', 'total')
    expect(liquido).toHaveTextContent('+1.700,00')

    const saldoC6 = definicoes[4]?.querySelector('[data-tone]')
    expect(saldoC6).toHaveAttribute('data-tone', 'negativo')
  })

  it('carregando: esqueleto no lugar de cada número e nenhuma frase de direção', () => {
    const { container } = render(
      <TransferPairPanel
        conta={{ id: NUBANK, nome: 'Nubank' }}
        contraparte={{ id: C6, nome: 'C6' }}
        par={undefined}
        saldos={[]}
        mes="2026-09"
        carregando
      />,
    )
    expect(container.querySelectorAll('[aria-hidden="true"]').length).toBe(5)
    expect(screen.queryByText(/enviou|equilibraram/)).not.toBeInTheDocument()
    expect(container.firstChild).toHaveAttribute('aria-busy', 'true')
  })
})
