import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { PasswordField } from './PasswordField'

describe('PasswordField', () => {
  it('alterna entre mostrar e ocultar sem usar aria-pressed', async () => {
    const user = userEvent.setup()
    render(<PasswordField label="Senha" autoComplete="current-password" />)

    const input = screen.getByLabelText('Senha')
    expect(input).toHaveAttribute('type', 'password')

    const toggle = screen.getByRole('button', { name: 'Mostrar senha' })
    expect(toggle).not.toHaveAttribute('aria-pressed')

    await user.click(toggle)
    expect(input).toHaveAttribute('type', 'text')
    expect(screen.getByRole('button', { name: 'Ocultar senha' })).toBeInTheDocument()
  })

  it('marca o requisito de 12 caracteres só quando ele é atendido', async () => {
    const user = userEvent.setup()
    render(<PasswordField label="Senha" autoComplete="new-password" showRequirement />)

    const requirement = screen.getByText('Pelo menos 12 caracteres').closest('p')
    expect(requirement).not.toHaveAttribute('data-met')

    await user.type(screen.getByLabelText('Senha'), 'meu caderno de contas')
    expect(requirement).toHaveAttribute('data-met', 'true')
  })

  it('descreve o erro com texto, não apenas com cor', () => {
    render(
      <PasswordField
        label="Senha"
        autoComplete="current-password"
        error="A senha precisa de pelo menos 12 caracteres."
      />,
    )

    const input = screen.getByLabelText('Senha')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveAccessibleDescription(/pelo menos 12 caracteres/i)
  })
})
