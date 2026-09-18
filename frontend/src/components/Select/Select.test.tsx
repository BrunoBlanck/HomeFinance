import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { Select, type SelectOption } from './Select'

const OPCOES: readonly SelectOption[] = [
  { value: 'nao-importar', label: 'Não importar' },
  { value: 'importar', label: 'Importar assim mesmo (é outra)' },
]

describe('Select', () => {
  it('usa o <select> nativo, com placeholder como estado "não escolhido"', async () => {
    const user = userEvent.setup()
    render(<Select label="Decisão" options={OPCOES} placeholder="Escolha a conta" />)

    const campo = screen.getByLabelText('Decisão')
    expect(campo.tagName).toBe('SELECT')
    expect(campo).toHaveValue('')

    await user.selectOptions(campo, 'importar')
    expect(campo).toHaveValue('importar')
  })

  it('agrupa em <optgroup> preservando a ordem em que os grupos chegaram', () => {
    render(
      <Select
        label="Categoria"
        options={[
          { value: 'c1', label: 'Padaria', group: 'Alimentação' },
          { value: 'c2', label: 'Pão', group: 'Alimentação' },
          { value: 'c3', label: 'Ônibus', group: 'Transporte' },
        ]}
      />,
    )

    const grupos = screen.getByLabelText('Categoria').querySelectorAll('optgroup')
    expect([...grupos].map((g) => g.label)).toEqual(['Alimentação', 'Transporte'])
    expect([...grupos].map((g) => [...g.querySelectorAll('option')].map((o) => o.value))).toEqual([
      ['c1', 'c2'],
      ['c3'],
    ])
  })

  /** Correção da E7 (l): o agrupamento é por TRECHO CONSECUTIVO, não por nome.
   *
   *  O bug que ela fecha era alcançável: `opcoesDeCategoria` emite grupo sem
   *  subcategoria como opção SOLTA, então a casa com o grupo "Investimentos" da
   *  semente via "Investimentos" no topo do seletor de despesa, acima de
   *  "Alimentação › Mercado", sem cabeçalho e sem nada dizendo que aquilo era
   *  investimento. */
  it('a opção solta fica NO LUGAR dela, entre dois grupos — nunca no topo', () => {
    render(
      <Select
        label="Categoria"
        options={[
          { value: 'c1', label: 'Mercado', group: 'Alimentação' },
          { value: 'c2', label: 'Investimentos' },
          { value: 'c3', label: 'Ônibus', group: 'Transporte' },
        ]}
      />,
    )

    const campo = screen.getByLabelText('Categoria')
    // Os filhos diretos do <select>, na ordem do documento: grupo, solta, grupo.
    expect([...campo.children].map((n) => n.tagName)).toEqual(['OPTGROUP', 'OPTION', 'OPTGROUP'])
    // E a ordem das opções continua sendo exatamente a da entrada.
    expect([...campo.querySelectorAll('option')].map((o) => o.value)).toEqual(['c1', 'c2', 'c3'])
  })

  it('dois trechos com o mesmo rótulo produzem DOIS optgroup, não um só', () => {
    render(
      <Select
        label="Categoria"
        options={[
          { value: 'c1', label: 'Padaria', group: 'Alimentação' },
          { value: 'c2', label: 'Ônibus', group: 'Transporte' },
          { value: 'c3', label: 'Mercado', group: 'Alimentação' },
        ]}
      />,
    )

    const grupos = screen.getByLabelText('Categoria').querySelectorAll('optgroup')
    expect([...grupos].map((g) => g.label)).toEqual(['Alimentação', 'Transporte', 'Alimentação'])
    // Fundir os dois moveria "Mercado" para antes de "Ônibus": a ordem que o
    // servidor mandou deixaria de ser a que a pessoa lê.
    expect(
      [...screen.getByLabelText('Categoria').querySelectorAll('option')].map((o) => o.value),
    ).toEqual(['c1', 'c2', 'c3'])
  })

  it('descreve o erro por texto, não só por cor', () => {
    render(<Select label="Conta de destino" options={OPCOES} error="Escolha a conta de destino." />)

    const campo = screen.getByLabelText('Conta de destino')
    expect(campo).toHaveAttribute('aria-invalid', 'true')
    expect(campo).toHaveAccessibleDescription('Escolha a conta de destino.')
  })
})

describe('Select — densidade compacta', () => {
  it('o rótulo escondido continua no DOM e continua nomeando o controle', () => {
    render(<Select label="Decisão" options={OPCOES} density="compact" labelHidden />)

    // getByLabelText só encontra porque o <label for> continua associado: um
    // controle sem nome acessível não passaria daqui.
    const campo = screen.getByLabelText('Decisão')
    expect(campo).toBeInTheDocument()
    expect(screen.getByText('Decisão')).toHaveClass('sr-only')
  })

  it('deixa o aria-label dar o nome específico da linha quando a célula precisa', () => {
    render(
      <Select
        label="Decisão"
        options={OPCOES}
        density="compact"
        labelHidden
        aria-label="Decisão para Pagamento de fatura, 07/08, R$ 2.859,82"
      />,
    )

    expect(
      screen.getByRole('combobox', {
        name: 'Decisão para Pagamento de fatura, 07/08, R$ 2.859,82',
      }),
    ).toBeInTheDocument()
  })

  it('marca a densidade no campo e no invólucro, que é quem o CSS encolhe', () => {
    const { container } = render(<Select label="Conta" options={OPCOES} density="compact" />)

    expect(container.querySelector('[data-density="compact"]')).toBeInTheDocument()
    // Dois: o .field (altura e slot de mensagem) e o .wrap (a seta encostada).
    expect(container.querySelectorAll('[data-density="compact"]')).toHaveLength(2)
  })

  it('sem erro, o slot de mensagem fica vazio — é o :empty que o tira do fluxo', () => {
    const { container } = render(<Select label="Conta" options={OPCOES} density="compact" />)

    const mensagem = container.querySelector('p[id$="-message"]')
    expect(mensagem).toBeEmptyDOMElement()
  })

  it('com erro, a mensagem aparece e continua descrevendo o campo', () => {
    render(
      <Select
        label="Conta"
        options={OPCOES}
        density="compact"
        labelHidden
        error="Senha incorreta."
      />,
    )

    expect(screen.getByLabelText('Conta')).toHaveAccessibleDescription('Senha incorreta.')
  })

  it('a densidade padrão é a de formulário — nada que já existe muda', () => {
    const { container } = render(<Select label="Conta" options={OPCOES} />)

    expect(container.querySelector('[data-density="compact"]')).not.toBeInTheDocument()
    expect(container.querySelectorAll('[data-density="form"]')).toHaveLength(2)
    expect(screen.getByText('Conta')).not.toHaveClass('sr-only')
  })
})
