import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { MoneyInput } from './MoneyInput'

/** Envoltório controlado: o componente guarda CENTAVOS, e o teste espia o
 *  inteiro que sairia no corpo da requisição — não o texto da tela. */
function Campo({
  inicial = 0,
  allowNegative = false,
}: {
  inicial?: number
  allowNegative?: boolean
}) {
  const [valor, setValor] = useState(inicial)
  return (
    <>
      <MoneyInput
        label="Saldo de abertura"
        value={valor}
        onChange={setValor}
        allowNegative={allowNegative}
      />
      <output data-testid="centavos">{valor}</output>
    </>
  )
}

function centavos() {
  return Number(screen.getByTestId('centavos').textContent)
}

describe('MoneyInput', () => {
  it('digita centavos primeiro: 1,2,3,4 vira 12,34', async () => {
    const user = userEvent.setup()
    render(<Campo />)
    const campo = screen.getByLabelText('Saldo de abertura')

    await user.click(campo)
    await user.keyboard('1')
    expect(campo).toHaveValue('0,01')
    await user.keyboard('2')
    expect(campo).toHaveValue('0,12')
    await user.keyboard('3')
    expect(campo).toHaveValue('1,23')
    await user.keyboard('4')
    expect(campo).toHaveValue('12,34')

    expect(centavos()).toBe(1234)
  })

  // O estado NUNCA é float. É a defesa que impede 0.1 + 0.2 de virar
  // 0.30000000000000004 num total de cem lançamentos (ADR-003).
  it('o valor entregue é sempre inteiro', async () => {
    const user = userEvent.setup()
    render(<Campo />)

    await user.click(screen.getByLabelText('Saldo de abertura'))
    await user.keyboard('123456')

    expect(centavos()).toBe(123456)
    expect(Number.isInteger(centavos())).toBe(true)
  })

  it('colar um valor formatado funciona', async () => {
    const user = userEvent.setup()
    render(<Campo />)
    const campo = screen.getByLabelText('Saldo de abertura')

    await user.click(campo)
    await user.paste('R$ 1.234,56')

    expect(centavos()).toBe(123456)
    expect(campo).toHaveValue('1.234,56')
  })

  it('ignora letras e símbolos', async () => {
    const user = userEvent.setup()
    render(<Campo />)

    await user.click(screen.getByLabelText('Saldo de abertura'))
    await user.keyboard('a1b2c3')

    expect(centavos()).toBe(123)
  })

  it('apagar tudo volta para zero, sem NaN', async () => {
    const user = userEvent.setup()
    render(<Campo inicial={5000} />)
    const campo = screen.getByLabelText('Saldo de abertura')

    await user.click(campo)
    await user.clear(campo)

    expect(centavos()).toBe(0)
    expect(campo).toHaveValue('0,00')
  })

  it('abre o teclado numérico no celular sem ser type=number', () => {
    render(<Campo />)
    const campo = screen.getByLabelText('Saldo de abertura')

    // `type="number"` traria setas de incremento e rolagem acidental mudando o
    // valor — num campo de dinheiro isso é um erro caro esperando acontecer.
    expect(campo).toHaveAttribute('type', 'text')
    expect(campo).toHaveAttribute('inputmode', 'decimal')
  })

  describe('valor negativo', () => {
    it('não oferece o sinal quando não é permitido', () => {
      render(<Campo />)
      expect(screen.queryByRole('button', { name: 'Valor negativo' })).not.toBeInTheDocument()
    })

    it('alterna o sinal e mantém o módulo digitado', async () => {
      const user = userEvent.setup()
      render(<Campo allowNegative />)

      await user.click(screen.getByLabelText('Saldo de abertura'))
      await user.keyboard('8000')
      expect(centavos()).toBe(8000)

      await user.click(screen.getByRole('button', { name: 'Valor negativo' }))
      expect(centavos()).toBe(-8000)
      // O campo mostra o módulo; quem carrega o sinal é o botão.
      expect(screen.getByLabelText('Saldo de abertura')).toHaveValue('80,00')

      await user.click(screen.getByRole('button', { name: 'Valor negativo' }))
      expect(centavos()).toBe(8000)
    })

    // O estado do botão precisa ser anunciado, não só pintado de vermelho:
    // cor sozinha é invisível para daltônico e some numa impressão.
    it('anuncia o estado por aria-pressed', async () => {
      const user = userEvent.setup()
      render(<Campo allowNegative />)
      const botao = screen.getByRole('button', { name: 'Valor negativo' })

      expect(botao).toHaveAttribute('aria-pressed', 'false')
      await user.click(botao)
      expect(botao).toHaveAttribute('aria-pressed', 'true')
    })

    it('continuar digitando preserva o sinal negativo', async () => {
      const user = userEvent.setup()
      render(<Campo allowNegative />)

      await user.click(screen.getByRole('button', { name: 'Valor negativo' }))
      await user.click(screen.getByLabelText('Saldo de abertura'))
      await user.keyboard('1500')

      expect(centavos()).toBe(-1500)
    })
  })

  it('mostra o erro recebido', () => {
    render(
      <MoneyInput
        label="Saldo"
        value={0}
        onChange={() => {}}
        error="Valor fora da faixa permitida."
      />,
    )
    expect(screen.getByText('Valor fora da faixa permitida.')).toBeInTheDocument()
    expect(screen.getByLabelText('Saldo')).toHaveAttribute('aria-invalid', 'true')
  })
})
