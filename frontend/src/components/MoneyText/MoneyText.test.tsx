import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MoneyText } from './MoneyText'

/** O texto visível fica num <span aria-hidden>; o que o leitor de tela recebe
 *  fica no .sr-only ao lado. Ler o DOM inteiro misturaria os dois. */
function visivel(container: HTMLElement): string {
  return container.querySelector('[aria-hidden="true"]')?.textContent ?? ''
}

describe('MoneyText', () => {
  it('por padrão, só o negativo mostra sinal', () => {
    const { container } = render(<MoneyText cents={160_000} />)
    expect(visivel(container)).toBe('1.600,00')
  })

  it('mostra o sinal do negativo mesmo sem pedir', () => {
    const { container } = render(<MoneyText cents={-1100} />)
    expect(visivel(container)).toBe('-11,00')
  })

  it('com sign="always", o positivo ganha o "+"', () => {
    const { container } = render(<MoneyText cents={160_000} sign="always" />)
    expect(visivel(container)).toBe('+1.600,00')
  })

  it('com sign="always", o negativo continua negativo', () => {
    const { container } = render(<MoneyText cents={-1100} sign="always" />)
    expect(visivel(container)).toBe('-11,00')
  })

  it('o sinal também vale com o símbolo da moeda', () => {
    const { container } = render(<MoneyText cents={160_000} format="currency" sign="always" />)
    expect(visivel(container).replace(/ /g, ' ')).toBe('+R$ 1.600,00')
  })
})

describe('MoneyText — o que o leitor de tela recebe', () => {
  it('diz "negativos" em vez de deixar o traço virar "traço"', () => {
    render(<MoneyText cents={-8000} />)
    expect(screen.getByText(/negativos/)).toHaveTextContent('R$ 80,00 negativos')
  })

  // Simetria: o áudio tem que dizer o que a tela mostra, nem mais nem menos.
  it('acrescenta "positivos" só quando o "+" está visível na tela', () => {
    const { rerender } = render(<MoneyText cents={160_000} sign="always" />)
    expect(screen.getByText(/positivos/)).toHaveTextContent('R$ 1.600,00 positivos')

    rerender(<MoneyText cents={160_000} />)
    expect(screen.queryByText(/positivos/)).not.toBeInTheDocument()
  })

  it('não chama o zero de positivo — sugeriria um movimento que não houve', () => {
    // Nem no texto que se vê, nem no que o leitor de tela anuncia. O `+` diz a
    // direção do dinheiro, e zero não tem direção: num dia em que entrada e
    // saída se anulam, `+0,00` afirmaria "entrou" sobre um saldo parado.
    const { container } = render(<MoneyText cents={0} sign="always" />)
    expect(visivel(container)).toBe('0,00')
    expect(screen.queryByText(/positivos/)).not.toBeInTheDocument()
    expect(screen.queryByText(/negativos/)).not.toBeInTheDocument()
  })
})

describe('MoneyText — ênfase', () => {
  it('expõe a variante no DOM, e o padrão é o normal', () => {
    const { container, rerender } = render(<MoneyText cents={512_345} />)
    expect(container.firstElementChild).toHaveAttribute('data-emphasis', 'normal')

    rerender(<MoneyText cents={512_345} emphasis="total" />)
    expect(container.firstElementChild).toHaveAttribute('data-emphasis', 'total')

    // A do centro da rosca: o mesmo texto tabular, só o corpo muda.
    rerender(<MoneyText cents={512_345} emphasis="hero" />)
    expect(container.firstElementChild).toHaveAttribute('data-emphasis', 'hero')
    expect(visivel(container)).toBe('5.123,45')
  })
})

describe('MoneyText — cor é reforço, nunca o portador único', () => {
  it('só pinta quando a tela pede tom semântico', () => {
    const { container, rerender } = render(<MoneyText cents={-1100} sign="always" />)
    expect(container.firstElementChild).toHaveAttribute('data-tone', 'neutral')

    rerender(<MoneyText cents={-1100} sign="always" tone="semantic" />)
    expect(container.firstElementChild).toHaveAttribute('data-tone', 'negativo')
  })

  it('a direção do dinheiro sobrevive sem a cor: o sinal está no texto', () => {
    const { container } = render(<MoneyText cents={-1100} sign="always" tone="neutral" />)
    expect(visivel(container)).toContain('-')
  })
})
