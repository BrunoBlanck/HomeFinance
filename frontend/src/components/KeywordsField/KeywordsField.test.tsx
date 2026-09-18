import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { KeywordsField } from './KeywordsField'

const DICA =
  'Prefira o nome do estabelecimento — «padaria», «uber», «netflix». Enter ou vírgula adiciona.'

type HarnessProps = {
  inicial?: string[]
  onSubmit?: () => void
  error?: string | undefined
  invalidIndex?: number | undefined
}

/** O campo é controlado, então o teste precisa de um dono da lista — e de um
 *  `<form>` em volta, porque a promessa mais importante do componente é que
 *  Enter NUNCA submete. */
function Harness({ inicial = [], onSubmit, error, invalidIndex }: HarnessProps) {
  const [lista, setLista] = useState<string[]>(inicial)
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault()
        onSubmit?.()
      }}
    >
      <KeywordsField
        value={lista}
        onChange={setLista}
        hint={DICA}
        error={error}
        invalidIndex={invalidIndex}
      />
      <button type="submit">Salvar</button>
    </form>
  )
}

function fichas(): string[] {
  const lista = screen.getByRole('list', { name: 'Palavras-chave adicionadas' })
  return within(lista)
    .queryAllByRole('listitem')
    .map((item) => item.textContent ?? '')
}

function botaoRemover(palavra: string) {
  return screen.getByRole('button', { name: `Remover ${palavra}` })
}

describe('KeywordsField', () => {
  it('Enter adiciona a palavra e nunca submete o formulário', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<Harness onSubmit={onSubmit} />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.type(input, 'padaria{Enter}')

    expect(fichas()).toEqual(['padaria'])
    expect(input).toHaveValue('')
    expect(onSubmit).not.toHaveBeenCalled()

    // Enter com o input VAZIO também não submete.
    await user.keyboard('{Enter}')
    expect(onSubmit).not.toHaveBeenCalled()
    expect(fichas()).toEqual(['padaria'])
  })

  it('vírgula adiciona e a vírgula não entra no texto', async () => {
    const user = userEvent.setup()
    render(<Harness />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.type(input, 'uber,netflix,')

    expect(fichas()).toEqual(['uber', 'netflix'])
    expect(input).toHaveValue('')
  })

  it('guarda a palavra como foi digitada, sem espaço sobrando', async () => {
    const user = userEvent.setup()
    render(<Harness />)

    await user.type(screen.getByLabelText('Palavras-chave'), '  Pão de Açúcar  {Enter}')
    expect(fichas()).toEqual(['Pão de Açúcar'])
  })

  it('colar divide por vírgula e por quebra de linha e adiciona todas', async () => {
    const user = userEvent.setup()
    render(<Harness />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.click(input)
    await user.paste('padaria, panificadora\nnetflix\r\nuber')

    expect(fichas()).toEqual(['padaria', 'panificadora', 'netflix', 'uber'])
    expect(input).toHaveValue('')
  })

  it('ao colar, a duplicata é pulada e a inválida fica no input com o motivo', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria']} />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.click(input)
    await user.paste('Padaria, x, netflix')

    // «Padaria» já existia (mesma forma normalizada); «x» é curta demais e
    // ficou no input, para nada se perder; «netflix» entrou.
    expect(fichas()).toEqual(['padaria', 'netflix'])
    expect(input).toHaveValue('x')
    expect(input).toHaveAccessibleDescription(/Use ao menos 2 letras ou números\./)
  })

  it('colar sem separador segue o caminho normal do input', async () => {
    const user = userEvent.setup()
    render(<Harness />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.click(input)
    await user.paste('mercado')

    expect(input).toHaveValue('mercado')
    expect(screen.queryByRole('list')).toBeInTheDocument()
    expect(within(screen.getByRole('list')).queryAllByRole('listitem')).toHaveLength(0)
  })

  it('Backspace com o input vazio remove a última; com texto, só apaga texto', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria', 'uber']} />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.type(input, 'ab{Backspace}')
    expect(fichas()).toEqual(['padaria', 'uber'])
    expect(input).toHaveValue('a')

    await user.keyboard('{Backspace}{Backspace}')
    expect(fichas()).toEqual(['padaria'])
    // O foco fica no input: quem apertou Backspace estava digitando.
    expect(input).toHaveFocus()
  })

  it('o × remove a ficha e não é parada de Tab', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria', 'uber']} />)

    const remover = botaoRemover('padaria')
    expect(remover).toHaveAttribute('tabindex', '-1')
    expect(remover).toHaveAttribute('type', 'button')

    await user.click(remover)
    expect(fichas()).toEqual(['uber'])
    expect(screen.queryByRole('button', { name: 'Remover padaria' })).not.toBeInTheDocument()
  })

  it('percorre as fichas com as setas e volta ao input pela direita', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria', 'uber', 'netflix']} />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.click(input)

    // ← no início do input vai ao × da ÚLTIMA ficha.
    await user.keyboard('{ArrowLeft}')
    expect(botaoRemover('netflix')).toHaveFocus()

    await user.keyboard('{ArrowLeft}')
    expect(botaoRemover('uber')).toHaveFocus()
    await user.keyboard('{ArrowLeft}')
    expect(botaoRemover('padaria')).toHaveFocus()
    // Na primeira, ← não vai a lugar nenhum.
    await user.keyboard('{ArrowLeft}')
    expect(botaoRemover('padaria')).toHaveFocus()

    await user.keyboard('{ArrowRight}{ArrowRight}')
    expect(botaoRemover('netflix')).toHaveFocus()
    // → na última volta ao input.
    await user.keyboard('{ArrowRight}')
    expect(input).toHaveFocus()
  })

  it('← só sai do input quando o cursor está no início', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria']} />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.type(input, 'ab')
    await user.keyboard('{ArrowLeft}')
    // Cursor moveu dentro do texto; o foco continua no input.
    expect(input).toHaveFocus()
    await user.keyboard('{ArrowLeft}{ArrowLeft}')
    expect(botaoRemover('padaria')).toHaveFocus()
  })

  it('Delete num × remove e o foco vai para a seguinte, senão a anterior, senão o input', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria', 'uber', 'netflix']} />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.click(input)
    await user.keyboard('{ArrowLeft}{ArrowLeft}{ArrowLeft}')
    expect(botaoRemover('padaria')).toHaveFocus()

    // Remove a primeira: o foco vai para a que era a seguinte.
    await user.keyboard('{Delete}')
    expect(fichas()).toEqual(['uber', 'netflix'])
    expect(botaoRemover('uber')).toHaveFocus()

    // Remove a última: não há seguinte, vai para a anterior.
    await user.keyboard('{ArrowRight}')
    await user.keyboard('{Backspace}')
    expect(fichas()).toEqual(['uber'])
    expect(botaoRemover('uber')).toHaveFocus()

    // Remove a única: sobra o input.
    await user.keyboard('{Enter}')
    expect(fichas()).toEqual([])
    expect(input).toHaveFocus()
  })

  it('Space num × também remove', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria', 'uber']} />)

    await user.click(screen.getByLabelText('Palavras-chave'))
    await user.keyboard('{ArrowLeft}')
    expect(botaoRemover('uber')).toHaveFocus()
    await user.keyboard(' ')
    expect(fichas()).toEqual(['padaria'])
    expect(botaoRemover('padaria')).toHaveFocus()
  })

  it('anuncia a contagem numa live region e descreve o input com contador, mensagem e instrução', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria', 'uber']} />)

    const contador = screen.getByRole('status')
    expect(contador.tagName).toBe('OUTPUT')
    expect(contador).toHaveAttribute('aria-live', 'polite')
    expect(contador).toHaveTextContent('2 de 20')

    await user.type(screen.getByLabelText('Palavras-chave'), 'netflix{Enter}')
    expect(contador).toHaveTextContent('3 de 20')

    const input = screen.getByLabelText('Palavras-chave')
    const descricao = input.getAttribute('aria-describedby')?.split(' ') ?? []
    expect(descricao).toHaveLength(3)
    expect(input).toHaveAccessibleDescription(
      /3 de 20.*Prefira o nome do estabelecimento.*Use as setas para percorrer as palavras e Backspace para remover\./,
    )
    // Sem placeholder: o exemplo mora na dica.
    expect(input).not.toHaveAttribute('placeholder')
  })

  it('duplicata não entra: o texto fica, a mensagem explica e some na próxima tecla', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['Padaria']} />)

    const input = screen.getByLabelText('Palavras-chave')
    // Mesma forma normalizada (caixa e acento não contam).
    await user.type(input, 'padária{Enter}')

    expect(fichas()).toEqual(['Padaria'])
    expect(input).toHaveValue('padária')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText('«padária» já está na lista.')).toBeInTheDocument()

    await user.keyboard('s')
    expect(screen.queryByText('«padária» já está na lista.')).not.toBeInTheDocument()
    expect(input).not.toHaveAttribute('aria-invalid')
    expect(input).toHaveValue('padárias')
  })

  it('inválida não entra e diz o motivo, com o texto mantido', async () => {
    const user = userEvent.setup()
    render(<Harness />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.type(input, '<script>{Enter}')
    expect(fichas()).toEqual([])
    expect(input).toHaveValue('<script>')
    expect(screen.getByText("Só letras, números, espaço e & . - / '")).toBeInTheDocument()

    await user.clear(input)
    await user.type(input, 'de{Enter}')
    expect(screen.getByText(/comum demais para reconhecer um lançamento/)).toBeInTheDocument()

    await user.clear(input)
    await user.type(input, `${'a'.repeat(41)}{Enter}`)
    expect(screen.getByText('No máximo 40 caracteres.')).toBeInTheDocument()

    await user.clear(input)
    await user.type(input, 'a{Enter}')
    expect(screen.getByText('Use ao menos 2 letras ou números.')).toBeInTheDocument()
    expect(fichas()).toEqual([])
  })

  it('em 20 de 20 o input é readOnly (não disabled), a dica muda e Backspace ainda remove', async () => {
    const user = userEvent.setup()
    const vinte = Array.from({ length: 20 }, (_, i) => `loja ${i + 1}`)
    render(<Harness inicial={vinte} />)

    const input = screen.getByLabelText('Palavras-chave')
    expect(input).toHaveAttribute('readonly')
    expect(input).not.toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent('20 de 20')
    expect(screen.getByRole('status')).toHaveAttribute('data-full', 'true')
    expect(
      screen.getByText('Limite de 20. Remova uma palavra para incluir outra.'),
    ).toBeInTheDocument()

    // Continua focável: é readOnly, não disabled.
    await user.click(input)
    expect(input).toHaveFocus()
    await user.keyboard('x')
    expect(input).toHaveValue('')

    await user.keyboard('{Backspace}')
    expect(fichas()).toHaveLength(19)
    expect(input).not.toHaveAttribute('readonly')
    expect(screen.getByText(DICA)).toBeInTheDocument()
  })

  it('erro do servidor marca a ficha indicada com borda e ícone, e a mensagem vai na FieldShell', () => {
    render(
      <Harness
        inicial={['padaria', 'uber']}
        error="«uber» já está em Transporte."
        invalidIndex={1}
      />,
    )

    const itens = within(screen.getByRole('list')).getAllByRole('listitem')
    expect(itens[0]).not.toHaveAttribute('data-invalid')
    expect(itens[1]).toHaveAttribute('data-invalid', 'true')
    // Erro nunca só por cor: a ficha marcada carrega um ícone.
    expect(itens[1]?.querySelector('svg')).not.toBeNull()
    expect(itens[0]?.querySelectorAll('svg')).toHaveLength(1) // só o × do botão
    expect(itens[1]?.querySelectorAll('svg')).toHaveLength(2) // alerta + ×

    const input = screen.getByLabelText('Palavras-chave')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveAccessibleDescription(/«uber» já está em Transporte\./)
    // Com erro, a dica sai — hint OU erro, nunca os dois.
    expect(screen.queryByText(DICA)).not.toBeInTheDocument()
  })

  it('a ficha marcada continua removível', async () => {
    const user = userEvent.setup()
    render(
      <Harness
        inicial={['padaria', 'uber']}
        error="«uber» já está em Transporte."
        invalidIndex={1}
      />,
    )

    await user.click(botaoRemover('uber'))
    expect(fichas()).toEqual(['padaria'])
  })

  // ---- bordas acrescentadas pelo QA (T13, 17/09/2026) ---------------------

  it('clicar no vão da caixa leva o foco ao input; clicar num × continua sendo clicar no ×', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria', 'uber']} />)

    const input = screen.getByLabelText('Palavras-chave')
    // A caixa é o pai imediato do input — a mesma "linha do caderno" do TextField.
    const caixa = input.parentElement as HTMLElement
    expect(document.activeElement).not.toBe(input)

    await user.click(caixa)
    expect(input).toHaveFocus()

    // O × não cede o clique para a caixa: remove, e o foco vai para a
    // seguinte, não para o input.
    await user.click(botaoRemover('padaria'))
    expect(fichas()).toEqual(['uber'])
    expect(botaoRemover('uber')).toHaveFocus()
  })

  it('a caixa carrega data-invalid junto com o erro, e perde ao corrigir', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria']} />)

    const input = screen.getByLabelText('Palavras-chave')
    const caixa = input.parentElement as HTMLElement
    expect(caixa).not.toHaveAttribute('data-invalid')

    await user.type(input, 'padaria{Enter}')
    expect(caixa).toHaveAttribute('data-invalid', 'true')

    await user.keyboard('s')
    expect(caixa).not.toHaveAttribute('data-invalid')
  })

  it('ao colar, o que não cabe no limite fica no input — nada se perde', async () => {
    const user = userEvent.setup()
    const dezenove = Array.from({ length: 19 }, (_, i) => `loja ${i + 1}`)
    render(<Harness inicial={dezenove} />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.click(input)
    await user.paste('mercearia, quitanda, sacolao')

    // Só a primeira coube (20 de 20); as outras duas ficam no input, na ordem.
    expect(fichas()).toHaveLength(20)
    expect(fichas().at(-1)).toBe('mercearia')
    expect(input).toHaveValue('quitanda, sacolao')
    expect(screen.getByRole('status')).toHaveTextContent('20 de 20')
    expect(input).toHaveAttribute('readonly')
  })

  it('em 20 de 20, colar não faz nada e Enter com texto não adiciona', async () => {
    const user = userEvent.setup()
    const vinte = Array.from({ length: 20 }, (_, i) => `loja ${i + 1}`)
    render(<Harness inicial={vinte} />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.click(input)
    await user.paste('mercearia, quitanda')
    expect(fichas()).toHaveLength(20)
    expect(input).toHaveValue('')

    await user.keyboard('{Enter}')
    expect(fichas()).toHaveLength(20)
  })

  it('a mensagem local (da tecla de agora) vence a do servidor, e a próxima tecla devolve a do servidor', async () => {
    const user = userEvent.setup()
    render(
      <Harness
        inicial={['padaria', 'uber']}
        error="«uber» já está em Transporte."
        invalidIndex={1}
      />,
    )

    const input = screen.getByLabelText('Palavras-chave')
    expect(screen.getByText('«uber» já está em Transporte.')).toBeInTheDocument()

    await user.type(input, 'padaria{Enter}')
    expect(screen.getByText('«padaria» já está na lista.')).toBeInTheDocument()
    expect(screen.queryByText('«uber» já está em Transporte.')).not.toBeInTheDocument()
    // A ficha marcada pelo servidor continua marcada: o erro dele não sumiu,
    // só a mensagem cedeu a vez.
    expect(within(screen.getByRole('list')).getAllByRole('listitem')[1]).toHaveAttribute(
      'data-invalid',
      'true',
    )

    await user.keyboard('s')
    expect(screen.queryByText('«padaria» já está na lista.')).not.toBeInTheDocument()
    expect(screen.getByText('«uber» já está em Transporte.')).toBeInTheDocument()
  })

  it('a palavra citada na mensagem é a forma limpa, não a crua com espaços', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria']} />)

    const input = screen.getByLabelText('Palavras-chave')
    await user.type(input, '  padaria  {Enter}')
    expect(screen.getByText('«padaria» já está na lista.')).toBeInTheDocument()
  })

  it('remover pelo × devolve ao pai a lista sem a palavra, na mesma ordem', async () => {
    const user = userEvent.setup()
    render(<Harness inicial={['padaria', 'uber', 'netflix']} />)

    await user.click(botaoRemover('uber'))
    expect(fichas()).toEqual(['padaria', 'netflix'])
    expect(screen.getByRole('status')).toHaveTextContent('2 de 20')
  })
})
