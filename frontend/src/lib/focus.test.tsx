import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { useFocoNoTitulo } from './focus'

function Tela({ titulo = 'Gastos por categoria' }: { titulo?: string }) {
  const tituloRef = useFocoNoTitulo()
  return (
    <h1 ref={tituloRef} tabIndex={-1}>
      {titulo}
    </h1>
  )
}

describe('useFocoNoTitulo', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('foca o <h1> ao montar', () => {
    render(<Tela />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveFocus()
  })

  /** O caso que o defeito de 17/09/2026 exercitou: chegar pela navegação
   *  significa chegar com o foco no `<a>` do menu. Uma guarda por
   *  `document.activeElement` passa no teste acima e falha neste. */
  it('foca o <h1> mesmo quando outro elemento já tem o foco', () => {
    const link = document.createElement('a')
    link.href = '/relatorios/categorias'
    link.textContent = 'Relatórios'
    document.body.append(link)
    link.focus()
    expect(link).toHaveFocus()

    render(<Tela />)

    expect(screen.getByRole('heading', { level: 1 })).toHaveFocus()
  })

  it('re-renderizar NÃO toma o foco de volta — só a montagem o move', () => {
    const { rerender } = render(<Tela />)

    const campo = document.createElement('input')
    document.body.append(campo)
    campo.focus()

    // Uma troca de busca (`?natureza=`) chega na tela como isto: props novas,
    // mesma montagem. O foco tem de ficar onde a pessoa o deixou.
    rerender(<Tela titulo="Receitas por categoria" />)

    expect(campo).toHaveFocus()
    expect(screen.getByRole('heading', { level: 1 })).not.toHaveFocus()
  })
})
