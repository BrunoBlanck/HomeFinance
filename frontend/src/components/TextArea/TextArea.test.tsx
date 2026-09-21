import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRef } from 'react'
import { describe, expect, it } from 'vitest'
import { TextArea } from './TextArea'

describe('TextArea', () => {
  it('é um <textarea> nativo com rótulo visível associado e a dica descrevendo o campo', () => {
    render(<TextArea label="Cole aqui o JSON" hint="Só o JSON — do primeiro { ao último }." />)

    const campo = screen.getByLabelText('Cole aqui o JSON')
    expect(campo.tagName).toBe('TEXTAREA')
    // O rótulo é pintado, não `sr-only`: nunca placeholder como rótulo.
    expect(screen.getByText('Cole aqui o JSON')).not.toHaveClass('sr-only')
    expect(campo).toHaveAccessibleDescription('Só o JSON — do primeiro { ao último }.')
    expect(campo).not.toHaveAttribute('aria-invalid')
  })

  it('o erro substitui a dica, marca aria-invalid e vem com ícone (nunca só cor)', () => {
    render(<TextArea label="JSON" hint="uma dica" error="Isso não é um JSON válido." />)

    const campo = screen.getByLabelText('JSON')
    expect(campo).toHaveAttribute('aria-invalid', 'true')
    expect(campo).toHaveAccessibleDescription('Isso não é um JSON válido.')
    expect(screen.queryByText('uma dica')).not.toBeInTheDocument()
    const mensagem = screen.getByText('Isso não é um JSON válido.').closest('p')
    expect(mensagem).toHaveAttribute('data-tone', 'error')
    expect(mensagem?.querySelector('svg')).not.toBeNull()
  })

  it('desliga corretor, maiúscula automática e verificação ortográfica — é texto para outro programa', () => {
    render(<TextArea label="JSON" />)

    const campo = screen.getByLabelText('JSON')
    expect(campo).toHaveAttribute('spellcheck', 'false')
    expect(campo).toHaveAttribute('autocapitalize', 'off')
    expect(campo).toHaveAttribute('autocorrect', 'off')
  })

  it('é controlado como qualquer textarea e entrega o ref ao elemento', async () => {
    const user = userEvent.setup()
    const ref = createRef<HTMLTextAreaElement>()
    let valor = ''
    render(
      <TextArea
        label="JSON"
        ref={ref}
        value={valor}
        onChange={(evento) => {
          valor = evento.target.value
        }}
      />,
    )

    const campo = screen.getByLabelText('JSON')
    expect(ref.current).toBe(campo)
    // Colar, e não digitar: é o gesto real deste campo — e `{` é caractere
    // especial para o teclado sintético do user-event.
    await user.click(campo)
    await user.paste('{"a":1}')
    expect(valor).toBe('{"a":1}')
  })

  it('não aceita className nem style — o estilo é do design system', () => {
    render(<TextArea label="JSON" />)
    const campo = screen.getByLabelText('JSON')
    // O único jeito de o campo ter classe é a do próprio componente.
    expect(campo.className).toMatch(/control/)
    expect(campo.className).toMatch(/textarea/)
    expect(campo).not.toHaveAttribute('style')
  })
})
