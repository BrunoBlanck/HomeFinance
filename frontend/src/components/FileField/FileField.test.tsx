import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { FileField } from './FileField'

function arquivo(nome = 'Nubank_2026-09-13.csv', conteudo = 'a'.repeat(18 * 1024)) {
  return new File([conteudo], nome, { type: 'text/csv' })
}

/** Casca controlada — é assim que a tela de importação usa o componente. */
function Campo({ onSelect }: { onSelect?: (f: File | null) => void }) {
  const [file, setFile] = useState<File | null>(null)
  return (
    <FileField
      label="Arquivo do extrato ou da fatura"
      accept=".csv,.zip"
      hint="CSV do Nubank ou o ZIP do C6."
      file={file}
      onSelect={(f) => {
        setFile(f)
        onSelect?.(f)
      }}
    />
  )
}

describe('FileField', () => {
  it('é um <input type="file"> REAL, visível e rotulado', () => {
    render(<Campo />)

    // getByLabelText só encontra o campo se o <label for> estiver associado —
    // é a asserção que impede a regressão para um <div> clicável com o input
    // escondido atrás.
    const campo = screen.getByLabelText('Arquivo do extrato ou da fatura')
    expect(campo).toHaveAttribute('type', 'file')
    expect(campo).toBeVisible()
    expect(campo).toHaveAttribute('accept', '.csv,.zip')
  })

  it('alcança o campo pelo teclado', async () => {
    const user = userEvent.setup()
    render(<Campo />)

    await user.tab()
    expect(screen.getByLabelText('Arquivo do extrato ou da fatura')).toHaveFocus()
  })

  it('avisa a escolha e mostra nome e tamanho do arquivo', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    render(<Campo onSelect={onSelect} />)

    await user.upload(screen.getByLabelText('Arquivo do extrato ou da fatura'), arquivo())

    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect.mock.calls[0]?.[0]).toBeInstanceOf(File)
    expect(screen.getByText('Nubank_2026-09-13.csv')).toBeInTheDocument()
    expect(screen.getByText('18 KB')).toBeInTheDocument()
  })

  it('mostra o nome inteiro no title, para o que o ellipsis corta', async () => {
    const user = userEvent.setup()
    const longo = `${'extrato-conta-corrente-'.repeat(6)}.csv`
    render(<Campo />)

    await user.upload(screen.getByLabelText('Arquivo do extrato ou da fatura'), arquivo(longo))

    expect(screen.getByText(longo)).toHaveAttribute('title', longo)
  })

  it('remover limpa a escolha e devolve o foco ao campo', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    render(<Campo onSelect={onSelect} />)

    const campo = screen.getByLabelText<HTMLInputElement>('Arquivo do extrato ou da fatura')
    await user.upload(campo, arquivo())
    expect(campo.files).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: 'Remover' }))

    expect(onSelect).toHaveBeenLastCalledWith(null)
    // O input tem que ser esvaziado de verdade: sem isso, reescolher o MESMO
    // arquivo não dispara `change` e a tela trava sem explicação.
    expect(campo.files).toHaveLength(0)
    expect(campo).toHaveFocus()
    expect(screen.getByText('nenhum arquivo selecionado')).toBeInTheDocument()
  })

  it('o convite ao arraste some quando já há arquivo', async () => {
    const user = userEvent.setup()
    render(<Campo />)

    expect(screen.getByText('ou arraste o arquivo para cá')).toBeInTheDocument()
    await user.upload(screen.getByLabelText('Arquivo do extrato ou da fatura'), arquivo())
    expect(screen.queryByText('ou arraste o arquivo para cá')).not.toBeInTheDocument()
  })

  it('erro substitui a dica e marca o campo como inválido', () => {
    render(
      <FileField
        label="Arquivo do extrato ou da fatura"
        accept=".csv,.zip"
        hint="CSV do Nubank ou o ZIP do C6."
        error="Escolha o arquivo do extrato ou da fatura."
        file={null}
        onSelect={() => {}}
      />,
    )

    const campo = screen.getByLabelText('Arquivo do extrato ou da fatura')
    expect(campo).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText('Escolha o arquivo do extrato ou da fatura.')).toBeInTheDocument()
    expect(screen.queryByText('CSV do Nubank ou o ZIP do C6.')).not.toBeInTheDocument()
    // O erro precisa chegar a quem ouve, não só a quem vê.
    expect(campo).toHaveAccessibleDescription('Escolha o arquivo do extrato ou da fatura.')
  })
})
