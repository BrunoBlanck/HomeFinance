import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Button } from './Button'

describe('Button', () => {
  it('em carregamento não desabilita: continua focável, mas anuncia-se ocupado', async () => {
    const user = userEvent.setup()
    const onClick = vi.fn()
    render(
      <Button variant="primary" loading onClick={onClick}>
        Entrar
      </Button>,
    )

    const button = screen.getByRole('button', { name: 'Entrar' })
    expect(button).not.toBeDisabled()
    expect(button).toHaveAttribute('aria-disabled', 'true')
    expect(button).toHaveAttribute('aria-busy', 'true')

    button.focus()
    expect(button).toHaveFocus()

    await user.click(button)
    expect(onClick).not.toHaveBeenCalled()
  })

  it('mantém o rótulo textual durante o carregamento', () => {
    const { rerender } = render(<Button>Entrar</Button>)
    expect(screen.getByRole('button')).toHaveAccessibleName('Entrar')

    rerender(<Button loading>Entrar</Button>)
    expect(screen.getByRole('button')).toHaveAccessibleName('Entrar')
  })

  it('não vira submit por acidente: o tipo padrão é button', () => {
    render(<Button>Ação</Button>)
    expect(screen.getByRole('button')).toHaveAttribute('type', 'button')
  })

  it('dispara o clique quando não está carregando', async () => {
    const user = userEvent.setup()
    const onClick = vi.fn()
    render(<Button onClick={onClick}>Tentar de novo</Button>)

    await user.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalledTimes(1)
  })
})
