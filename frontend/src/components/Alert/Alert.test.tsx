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

  /** E7 (l): resultado de uma ação já FEITA é assertivo; prévia de uma ação
   *  ainda NÃO feita é polido. O tom deixa de ser quem decide isso — mas só
   *  quando alguém pede, para que nenhum Alert existente mude. */
  it('live="polite" tira a urgência de um aviso que é PRÉVIA, sem mexer no tom', () => {
    render(
      <Alert tone="warning" live="polite">
        Isto muda os totais de meses já fechados
      </Alert>,
    )

    // role="status" já implica aria-live="polite": um atributo só, sem dois que
    // possam divergir.
    expect(screen.getByRole('status')).toHaveTextContent('Isto muda os totais')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('live="assertive" interrompe mesmo num tom que não interromperia', () => {
    render(
      <Alert tone="success" live="assertive">
        Importação concluída
      </Alert>,
    )

    expect(screen.getByRole('alert')).toHaveTextContent('Importação concluída')
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('sem a prop, o papel continua vindo do tom — nada que já existe muda', () => {
    const { unmount } = render(<Alert tone="warning">Atenção</Alert>)
    expect(screen.getByRole('alert')).toBeInTheDocument()
    unmount()

    render(<Alert tone="info">Aviso</Alert>)
    expect(screen.getByRole('status')).toBeInTheDocument()
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
