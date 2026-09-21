import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { type Column, DataTable, type RowGroup } from './DataTable'

type Linha = {
  id: string
  conta: string
  descricao: string
  valor: string
}

function linha(over: Partial<Linha> = {}): Linha {
  return { id: 'l-1', conta: 'Conta corrente', descricao: 'Padaria', valor: '-29,00', ...over }
}

const COLUNAS: readonly Column<Linha>[] = [
  { key: 'conta', header: 'Conta', hideBelow: 'sm', render: (l) => l.conta },
  { key: 'descricao', header: 'Descrição', render: (l) => l.descricao },
  { key: 'valor', header: 'Valor', align: 'end', width: 'min', render: (l) => l.valor },
]

function grupo(over: Partial<RowGroup<Linha>> = {}): RowGroup<Linha> {
  return { key: 'g-1', label: 'segunda, 31 de agosto', rows: [linha()], ...over }
}

describe('DataTable — comportamento que já existia', () => {
  it('renderiza cabeçalho, linhas e rodapé numa tabela semântica', () => {
    render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        rows={[linha(), linha({ id: 'l-2', descricao: 'Posto' })]}
        rowKey={(l) => l.id}
        footer={
          <tr>
            <td colSpan={3}>Total</td>
          </tr>
        }
      />,
    )

    expect(screen.getByRole('table', { name: 'Lançamentos' })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'Conta' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Padaria' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Posto' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Total' })).toBeInTheDocument()
  })

  it('aplica os atributos de linha que a tela pediu', () => {
    render(
      <DataTable
        caption="Contas"
        columns={COLUNAS}
        rows={[linha()]}
        rowKey={(l) => l.id}
        rowAttrs={() => ({ 'data-archived': 'true' })}
      />,
    )

    expect(screen.getByRole('row', { name: /Padaria/ })).toHaveAttribute('data-archived', 'true')
  })

  it('troca o corpo pelo estado vazio quando não há linha', () => {
    render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        rows={[]}
        rowKey={(l) => l.id}
        empty={<p>Nenhum lançamento em agosto.</p>}
      />,
    )

    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.getByText('Nenhum lançamento em agosto.')).toBeInTheDocument()
  })

  it('no esqueleto, tira a tabela da árvore de acessibilidade e anuncia o carregamento', () => {
    render(
      <DataTable caption="Lançamentos" columns={COLUNAS} rows={[]} rowKey={(l) => l.id} loading />,
    )

    // A tabela existe no DOM (o layout não pode saltar quando o dado chega),
    // mas aria-hidden a mantém fora da árvore — por isso não há role "table".
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Carregando lançamentos')
  })

  it('o esqueleto vence o estado vazio — dado a caminho não é dado ausente', () => {
    render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        rows={[]}
        rowKey={(l) => l.id}
        loading
        empty={<p>Nenhum lançamento em agosto.</p>}
      />,
    )

    expect(screen.queryByText('Nenhum lançamento em agosto.')).not.toBeInTheDocument()
  })
})

describe('DataTable — agrupamento', () => {
  it('abre um <tbody> por grupo, com o rótulo num th scope="rowgroup"', () => {
    render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        groups={[
          grupo({ key: '2026-08-31', label: 'segunda, 31 de agosto' }),
          grupo({
            key: '2026-08-27',
            label: 'quinta, 27 de agosto',
            rows: [linha({ id: 'l-2', descricao: 'Posto' })],
          }),
        ]}
        rowKey={(l) => l.id}
      />,
    )

    // thead + dois tbody. Se os grupos virassem tabelas separadas, o cabeçalho
    // de coluna se repetiria e a navegação por coluna do leitor de tela
    // recomeçaria do zero em cada bloco.
    expect(screen.getAllByRole('rowgroup')).toHaveLength(3)
    expect(screen.getAllByRole('table')).toHaveLength(1)

    const cabecalho = screen.getByRole('rowheader', { name: /segunda, 31 de agosto/ })
    expect(cabecalho).toHaveAttribute('scope', 'rowgroup')
    expect(screen.getByRole('rowheader', { name: /quinta, 27 de agosto/ })).toBeInTheDocument()
  })

  it('o cabeçalho do grupo é UMA célula cobrindo a linha inteira, com o trailing dentro', () => {
    const { container } = render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        groups={[grupo({ trailing: <span>-5.029,00</span> })]}
        rowKey={(l) => l.id}
      />,
    )

    // Uma célula só, com colSpan igual ao número de colunas. Com o trailing
    // numa <td> vizinha, abaixo de 40rem (onde `hideBelow` apaga colunas) a
    // linha pedia mais colunas do que a tabela tinha e o navegador criava
    // largura fantasma — 94px comidos da coluna Descrição a 375px.
    const cabecalho = screen.getByRole('rowheader')
    expect(cabecalho).toHaveAttribute('colspan', String(COLUNAS.length))
    expect(container.querySelectorAll('tbody tr:first-child > *')).toHaveLength(1)
    expect(cabecalho).toHaveTextContent('-5.029,00')
  })

  it('sem trailing, o cabeçalho do grupo continua sendo uma célula só', () => {
    const { container } = render(
      <DataTable
        caption="Linhas barradas"
        columns={COLUNAS}
        groups={[grupo()]}
        rowKey={(l) => l.id}
      />,
    )

    expect(screen.getByRole('rowheader')).toHaveAttribute('colspan', String(COLUNAS.length))
    expect(container.querySelectorAll('tbody tr:first-child > *')).toHaveLength(1)
  })

  it('escreve a explicação do grupo uma vez, no cabeçalho, em vez de por linha', () => {
    render(
      <DataTable
        caption="Linhas barradas"
        columns={COLUNAS}
        groups={[
          grupo({
            label: 'Possível duplicata · 1',
            description:
              'Mesmo valor e data a até 3 dias de um lançamento que já existe, com descrição diferente.',
          }),
        ]}
        rowKey={(l) => l.id}
      />,
    )

    expect(
      screen.getByRole('rowheader', { name: /Mesmo valor e data a até 3 dias/ }),
    ).toBeInTheDocument()
  })

  it('mostra o estado vazio quando nenhum grupo tem linha', () => {
    render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        groups={[grupo({ rows: [] })]}
        rowKey={(l) => l.id}
        empty={<p>Nenhum lançamento em agosto.</p>}
      />,
    )

    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.getByText('Nenhum lançamento em agosto.')).toBeInTheDocument()
  })

  it('mantém as linhas dentro do grupo a que pertencem', () => {
    render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        groups={[
          grupo({ key: 'a', label: 'Grupo A', rows: [linha({ id: 'a1', descricao: 'Padaria' })] }),
          grupo({ key: 'b', label: 'Grupo B', rows: [linha({ id: 'b1', descricao: 'Posto' })] }),
        ]}
        rowKey={(l) => l.id}
      />,
    )

    const [, grupoA, grupoB] = screen.getAllByRole('rowgroup')
    expect(grupoA).toBeDefined()
    expect(grupoB).toBeDefined()
    expect(within(grupoA as HTMLElement).getByText('Padaria')).toBeInTheDocument()
    expect(within(grupoA as HTMLElement).queryByText('Posto')).not.toBeInTheDocument()
    expect(within(grupoB as HTMLElement).getByText('Posto')).toBeInTheDocument()
  })
})

describe('DataTable — coluna que some no celular', () => {
  it('marca th e td da mesma coluna, para os dois sumirem juntos', () => {
    const { container } = render(
      <DataTable caption="Lançamentos" columns={COLUNAS} rows={[linha()]} rowKey={(l) => l.id} />,
    )

    expect(screen.getByRole('columnheader', { name: 'Conta' })).toHaveAttribute('data-hide', 'sm')
    expect(screen.getByRole('cell', { name: 'Conta corrente' })).toHaveAttribute('data-hide', 'sm')
    // Só a coluna que pediu: esconder uma célula a mais desalinharia a grade.
    expect(container.querySelectorAll('[data-hide="sm"]')).toHaveLength(2)
  })

  it('não marca nada quando nenhuma coluna pediu para sumir', () => {
    const { container } = render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS.map(({ hideBelow: _ignorado, ...resto }) => resto)}
        rows={[linha()]}
        rowKey={(l) => l.id}
      />,
    )

    expect(container.querySelectorAll('[data-hide]')).toHaveLength(0)
  })

  it('marca também no esqueleto, senão a tabela salta de largura ao carregar', () => {
    const { container } = render(
      <DataTable caption="Lançamentos" columns={COLUNAS} rows={[]} rowKey={(l) => l.id} loading />,
    )

    // 1 th + 3 linhas de esqueleto.
    expect(container.querySelectorAll('[data-hide="sm"]')).toHaveLength(4)
  })
})

describe('DataTable — coluna elástica', () => {
  /** Uma `auto` só (Descrição); o resto encolhe até o conteúdo. */
  const COM_UMA_ELASTICA: readonly Column<Linha>[] = COLUNAS.map((coluna) =>
    coluna.key === 'descricao' ? coluna : { ...coluna, width: 'min' as const },
  )

  it('elege a única coluna `auto` da tabela — th e td, para a grade não desalinhar', () => {
    const { container } = render(
      <DataTable
        caption="Lançamentos"
        columns={COM_UMA_ELASTICA}
        rows={[linha()]}
        rowKey={(l) => l.id}
      />,
    )

    // Descrição é a única sem `width: 'min'`: é ela que fica com o espaço que
    // sobra e trunca, em vez de empurrar Valor para fora da moldura.
    expect(screen.getByRole('columnheader', { name: 'Descrição' })).toHaveAttribute('data-flex')
    expect(screen.getByRole('cell', { name: 'Padaria' })).toHaveAttribute('data-flex')
    expect(container.querySelectorAll('[data-flex]')).toHaveLength(2)
  })

  it('não elege ninguém quando há mais de uma coluna `auto`', () => {
    // A tabela de pares de /transferencias: várias colunas sem largura fixa.
    // Encolher uma delas só engordaria as outras.
    const { container } = render(
      <DataTable
        caption="Pares"
        columns={COLUNAS.map(({ width: _ignorado, ...resto }) => resto)}
        rows={[linha()]}
        rowKey={(l) => l.id}
      />,
    )

    expect(container.querySelectorAll('[data-flex]')).toHaveLength(0)
  })

  it('marca também no esqueleto, senão a tabela salta de largura ao carregar', () => {
    const { container } = render(
      <DataTable
        caption="Lançamentos"
        columns={COM_UMA_ELASTICA}
        rows={[]}
        rowKey={(l) => l.id}
        loading
      />,
    )

    // 1 th + 3 linhas de esqueleto.
    expect(container.querySelectorAll('[data-flex]')).toHaveLength(4)
  })
})

describe('DataTable — linha de detalhe', () => {
  it('abre uma <tr> de detalhe logo abaixo da linha, com uma célula que cobre todas as colunas', () => {
    const { container } = render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        rows={[linha(), linha({ id: 'l-2', descricao: 'Posto' })]}
        rowKey={(l) => l.id}
        detail={(l) => (l.id === 'l-2' ? <p>Editor de Posto</p> : null)}
      />,
    )

    const linhas = container.querySelectorAll('tbody tr')
    // Duas de dado + uma de detalhe, e o detalhe vem DEPOIS da linha dele.
    expect(linhas).toHaveLength(3)
    expect(linhas[1]).toHaveTextContent('Posto')
    expect(linhas[2]?.className).toMatch(/detail/)
    expect(linhas[2]).toHaveTextContent('Editor de Posto')

    const celula = linhas[2]?.querySelector('td')
    expect(celula).toHaveAttribute('colspan', String(COLUNAS.length))
    expect(linhas[2]?.querySelectorAll('td')).toHaveLength(1)
    // A tabela continua sendo uma só, dentro da mesma moldura.
    expect(screen.getByRole('table', { name: 'Lançamentos' })).toContainElement(
      linhas[2] as HTMLElement,
    )
  })

  it('sem conteúdo (null) não acrescenta linha nenhuma', () => {
    const { container } = render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        rows={[linha()]}
        rowKey={(l) => l.id}
        detail={() => null}
      />,
    )
    expect(container.querySelectorAll('tbody tr')).toHaveLength(1)
  })

  it('a linha de detalhe não herda os atributos da linha de dado', () => {
    // `rowAttrs` fala da LINHA (hoje `data-archived`, que apaga a linha); o
    // detalhe é um editor aberto, e apagá-lo junto seria dizer outra coisa.
    const { container } = render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        rows={[linha()]}
        rowKey={(l) => l.id}
        rowAttrs={() => ({ 'data-archived': 'true' })}
        detail={() => <p>Editor</p>}
      />,
    )

    const linhas = container.querySelectorAll('tbody tr')
    expect(linhas[0]).toHaveAttribute('data-archived', 'true')
    expect(linhas[1]).not.toHaveAttribute('data-archived')
  })

  it('o esqueleto não abre linha de detalhe — não há linha sobre a qual abrir', () => {
    const { container } = render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        rows={[]}
        rowKey={(l) => l.id}
        loading
        detail={() => <p>Editor</p>}
      />,
    )
    expect(container.querySelectorAll('[class*="detail"]')).toHaveLength(0)
    expect(screen.queryByText('Editor')).not.toBeInTheDocument()
  })

  it('funciona também na tabela agrupada', () => {
    const { container } = render(
      <DataTable
        caption="Lançamentos"
        columns={COLUNAS}
        groups={[grupo({ rows: [linha(), linha({ id: 'l-2', descricao: 'Posto' })] })]}
        rowKey={(l) => l.id}
        detail={(l) => (l.id === 'l-1' ? <p>Editor de Padaria</p> : null)}
      />,
    )
    const linhas = container.querySelectorAll('tbody tr')
    // Cabeçalho do grupo + Padaria + detalhe + Posto.
    expect(linhas).toHaveLength(4)
    expect(linhas[2]?.className).toMatch(/detail/)
    expect(linhas[3]).toHaveTextContent('Posto')
  })
})
