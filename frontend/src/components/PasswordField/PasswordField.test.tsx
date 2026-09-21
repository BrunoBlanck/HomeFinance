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

  // Regressão de segurança. O `off` existe para UM caso e só ele: segredo de
  // uso único que não é credencial de ninguém — hoje a senha do arquivo da
  // importação, que é o CPF do titular do cartão. Trocá-lo por
  // `current-password` faria o navegador oferecer salvar esse dado de terceiro
  // como credencial da origem do HomeFinance. E `off` num campo de senha da
  // CONTA é o erro simétrico: esconder a senha do gerenciador empurra o usuário
  // para uma senha pior. O componente repassa o que recebe, sem "consertar".
  it.each(['current-password', 'new-password', 'off'] as const)(
    'repassa autoComplete="%s" literalmente para o input',
    (valor) => {
      render(<PasswordField label="Senha" autoComplete={valor} />)
      expect(screen.getByLabelText('Senha')).toHaveAttribute('autocomplete', valor)
    },
  )

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
