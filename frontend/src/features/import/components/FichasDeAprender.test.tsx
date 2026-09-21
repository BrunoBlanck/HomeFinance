import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Category } from '@/api/types'
import { FichasDeAprender } from './FichasDeAprender'

/** O ciclo completo (clique → PATCH → toast → foco) é coberto no teste da tela
 *  de revisão, onde o cache de categorias e o `ToastProvider` existem de
 *  verdade. A REGRA de quais palavras viram ficha (`palavrasParaAprender`)
 *  mora em `lib/keywords.ts` e é testada lá. */

function categoria(keywords: string[] = []): Category {
  return {
    id: 'cat-alimentacao',
    name: 'Alimentação',
    kind: 'expense',
    parentId: null,
    keywords,
    archivedAt: null,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    children: [],
  }
}

describe('FichasDeAprender', () => {
  it('cada ficha é um botão do sistema com o nome acessível completo', () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <FichasDeAprender
          descricao="Mercado do seu José"
          categoria={categoria()}
          onAprendida={() => {}}
        />
      </QueryClientProvider>,
    )
    expect(screen.getByText('Da próxima vez, reconhecer por')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Adicionar «mercado» às palavras-chave de Alimentação' }),
    ).toHaveTextContent('mercado')
    expect(
      screen.getByRole('button', { name: 'Adicionar «jose» às palavras-chave de Alimentação' }),
    ).toBeInTheDocument()
  })

  it('não renderiza nada quando não há palavra a oferecer', () => {
    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <FichasDeAprender descricao="123" categoria={categoria()} onAprendida={() => {}} />
      </QueryClientProvider>,
    )
    expect(container).toBeEmptyDOMElement()
  })
})
