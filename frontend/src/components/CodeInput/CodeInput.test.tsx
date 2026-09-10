import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { CodeInput } from './CodeInput'

function Harness({ onComplete }: { onComplete?: (value: string) => void }) {
  const [value, setValue] = useState('')
  return <CodeInput value={value} onChange={setValue} onComplete={onComplete} />
}

function field() {
  return screen.getByLabelText('Código de 6 dígitos')
}

describe('CodeInput', () => {
  it('limpa separadores ao colar: "12 34-56" vira 123456', async () => {
    const user = userEvent.setup()
    render(<Harness />)

    await user.click(field())
    await user.paste('12 34-56')

    expect(field()).toHaveValue('123456')
  })

  it('ignora letras e para no sexto dígito', async () => {
    const user = userEvent.setup()
    render(<Harness />)

    await user.click(field())
    await user.paste('seu código é 1234567')

    expect(field()).toHaveValue('123456')
  })

  it('dispara onComplete ao crescer até 6 dígitos e não dispara ao apagar', async () => {
    const user = userEvent.setup()
    const onComplete = vi.fn()
    render(<Harness onComplete={onComplete} />)

    await user.click(field())
    await user.keyboard('123456')

    expect(onComplete).toHaveBeenCalledTimes(1)
    expect(onComplete).toHaveBeenCalledWith('123456')

    await user.keyboard('{Backspace}')

    expect(field()).toHaveValue('12345')
    expect(onComplete).toHaveBeenCalledTimes(1)
  })

  it('não redispara onComplete depois de um erro, com o mesmo valor', () => {
    const onComplete = vi.fn()
    const noop = () => {}

    const { rerender } = render(<CodeInput value="" onChange={noop} onComplete={onComplete} />)
    rerender(<CodeInput value="123456" onChange={noop} onComplete={onComplete} />)
    expect(onComplete).toHaveBeenCalledTimes(1)

    // O servidor recusou: o campo fica inválido, mas o valor não mudou.
    rerender(<CodeInput value="123456" onChange={noop} onComplete={onComplete} invalid />)
    rerender(<CodeInput value="123456" onChange={noop} onComplete={onComplete} invalid />)

    expect(onComplete).toHaveBeenCalledTimes(1)
    expect(field()).toHaveAttribute('aria-invalid', 'true')
    expect(field()).toHaveValue('123456')
  })

  it('volta a disparar quando o usuário corrige o código', () => {
    const onComplete = vi.fn()
    const noop = () => {}

    const { rerender } = render(
      <CodeInput value="123456" onChange={noop} onComplete={onComplete} invalid />,
    )
    expect(onComplete).toHaveBeenCalledTimes(1)

    rerender(<CodeInput value="12345" onChange={noop} onComplete={onComplete} />)
    rerender(<CodeInput value="123457" onChange={noop} onComplete={onComplete} />)

    expect(onComplete).toHaveBeenCalledTimes(2)
    expect(onComplete).toHaveBeenLastCalledWith('123457')
  })

  it('durante o envio fica somente leitura, nunca desabilitado', () => {
    render(<CodeInput value="123456" onChange={() => {}} busy />)

    expect(field()).toHaveAttribute('readonly')
    expect(field()).not.toBeDisabled()
    expect(field()).toHaveAttribute('aria-busy', 'true')
  })

  it('usa um único campo com os atributos que o autofill de OTP espera', () => {
    render(<CodeInput value="" onChange={() => {}} />)

    const input = field()
    expect(screen.getAllByRole('textbox')).toHaveLength(1)
    expect(input).toHaveAttribute('type', 'text')
    expect(input).toHaveAttribute('inputmode', 'numeric')
    expect(input).toHaveAttribute('autocomplete', 'one-time-code')
  })
})
