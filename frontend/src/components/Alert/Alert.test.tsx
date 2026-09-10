import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Alert } from './Alert'

describe('Alert', () => {
  it('usa role="alert" em erro e aviso', () => {
    const { unmount } = render(<Alert tone="error">Falhou</Alert>)
    expect(screen.getByRole('alert')).toHaveTextContent('Falhou')
    unmount()

    render(<Alert tone="warning">Atenção</Alert>)
    expect(screen.getByRole('alert')).toHaveTextContent('Atenção')
  })

  it('usa role="status" em sucesso e informação', () => {
    const { unmount } = render(<Alert tone="success">Pronto</Alert>)
    expect(screen.getByRole('status')).toHaveTextContent('Pronto')
    unmount()

    render(<Alert tone="info">Aviso</Alert>)
    expect(screen.getByRole('status')).toHaveTextContent('Aviso')
  })

  it('recebe o foco quando pedido', () => {
    render(
      <Alert tone="error" autoFocus>
        E-mail ou senha incorretos.
      </Alert>,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveAttribute('tabindex', '-1')
    expect(alert).toHaveFocus()
  })
})
