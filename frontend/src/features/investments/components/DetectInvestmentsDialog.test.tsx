import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { InvestmentDetectResult } from '@/api/types'
import { ToastProvider } from '@/components/Toast/Toast'
import {
  DetectInvestmentsDialog,
  fraseDoResumo,
  fraseDoToast,
  rotuloDoConfirmar,
} from './DetectInvestmentsDialog'

const fetchMock = vi.fn()

const CDB = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8daa'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function previa(over: Partial<InvestmentDetectResult> = {}): InvestmentDetectResult {
  return {
    month: '2026-09',
    marked: 0,
    unmatched: 0,
    alreadyCategorized: 0,
    items: [],
    unmatchedItems: [],
    alreadyCategorizedItems: [],
    ...over,
  }
}

function novo(
  id: string,
  descricao: string,
  pontuacao = 92,
): InvestmentDetectResult['items'][number] {
  return {
    id,
    description: descricao,
    categoryId: CDB,
    categoryName: 'CDB',
    flow: 'contribution',
    matchScore: pontuacao,
    matchedKeyword: 'cdb',
  }
}

function jaTem(
  id: string,
  descricao: string,
): InvestmentDetectResult['alreadyCategorizedItems'][number] {
  return {
    id,
    description: descricao,
    currentCategoryId: '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8dbb',
    currentCategoryName: 'Outros',
    categoryId: CDB,
    categoryName: 'CDB',
    flow: 'contribution',
    matchScore: 100,
  }
}

function semCategoria(
  id: string,
  descricao: string,
  motivo: InvestmentDetectResult['unmatchedItems'][number]['reason'],
): InvestmentDetectResult['unmatchedItems'][number] {
  return { id, description: descricao, reason: motivo }
}

/** Uma prévia com os três grupos: 1 novo, 1 já categorizado e 2 sem categoria. */
const PREVIA_COMPLETA = previa({
  marked: 1,
  unmatched: 2,
  alreadyCategorized: 1,
  items: [novo('i-1', 'CDB 15 DIAS')],
  alreadyCategorizedItems: [jaTem('i-2', 'APLIC AUTOMATICA')],
  unmatchedItems: [
    semCategoria('i-3', 'PAGAMENTO DIVERSO', 'below_threshold'),
    semCategoria('i-4', 'TRANSF XPTO', 'other_category'),
  ],
})

/** Corpos enviados a `POST /investments/detect`, na ordem. */
function corpos(): Record<string, unknown>[] {
  return fetchMock.mock.calls
    .filter((chamada) => String(chamada[0]).includes('/investments/detect'))
    .map((chamada) => JSON.parse(String((chamada[1] as RequestInit).body)))
}

function renderDialogo(resposta: (corpo: Record<string, unknown>) => Response) {
  fetchMock.mockImplementation((_url: string, init?: RequestInit) =>
    Promise.resolve(resposta(JSON.parse(String(init?.body ?? '{}')))),
  )
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const onClose = vi.fn()
  const onIrParaCategorias = vi.fn()
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <DetectInvestmentsDialog
          open
          mes="2026-09"
          onClose={onClose}
          onIrParaCategorias={onIrParaCategorias}
        />
      </ToastProvider>
    </QueryClientProvider>,
  )
  return { onClose, onIrParaCategorias }
}

/** A prévia sempre; a gravação devolve `marked` combinado. */
function respostaPadrao(gravou: number) {
  return (corpo: Record<string, unknown>) =>
    corpo.dryRun === true
      ? jsonResponse(200, PREVIA_COMPLETA)
      : jsonResponse(200, previa({ marked: gravou }))
}

describe('DetectInvestmentsDialog', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('abre pedindo a prévia, sem gravar e sem sobrescrever', async () => {
    renderDialogo(respostaPadrao(1))

    expect(await screen.findByText('1 lançamento vira aporte ou resgate.')).toBeInTheDocument()
    // Ausente é `false`, e o ausente é o SEGURO: a prévia nunca promete troca
    // que ninguém pediu.
    expect(corpos()).toEqual([{ month: '2026-09', dryRun: true, overwriteCategorized: false }])
  })

  it('mostra os três grupos, com o motivo de cada lançamento que ficou de fora', async () => {
    renderDialogo(respostaPadrao(1))

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    expect(screen.getByText('Viram aporte ou resgate · 1')).toBeInTheDocument()
    expect(screen.getByText('Já têm categoria · 1')).toBeInTheDocument()
    expect(screen.getByText('Continuam sem categoria · 2')).toBeInTheDocument()

    // O PORQUÊ mora na descrição do grupo, dito uma vez só (regra da E2) — e o
    // terceiro motivo tem próximo passo próprio: quem escreve categoria comum é
    // o auto-categorize de /lancamentos, não esta detecção.
    expect(screen.getByText(/use Categorizar automaticamente em Lançamentos/)).toBeInTheDocument()

    // Os três motivos viram PALAVRA curta na célula — nunca o código cru do
    // contrato, e nunca uma oração inteira numa coluna de largura mínima.
    expect(screen.getByText('abaixo de 80%')).toBeInTheDocument()
    expect(screen.getByText('bateu com outra categoria')).toBeInTheDocument()
    expect(screen.queryByText('below_threshold')).not.toBeInTheDocument()
    expect(screen.queryByText('other_category')).not.toBeInTheDocument()

    // Proveniência: pontuação é texto, e a palavra-chave vem entre angulares.
    expect(screen.getByText('92% · «cdb»')).toBeInTheDocument()
    // A direção da troca é dita em palavras para quem não vê a seta.
    expect(screen.getByText('de Outros para CDB')).toBeInTheDocument()
  })

  it('sem a caixa marcada, grava com overwriteCategorized false', async () => {
    const { onClose } = renderDialogo(respostaPadrao(1))

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 lançamento' }))

    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(corpos()[1]).toEqual({
      month: '2026-09',
      dryRun: false,
      overwriteCategorized: false,
    })
  })

  it('a caixa muda o rótulo do confirmar, e marcá-la não grava nada', async () => {
    renderDialogo(respostaPadrao(2))

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    expect(screen.getByRole('button', { name: 'Marcar 1 lançamento' })).toBeInTheDocument()

    await userEvent.click(
      screen.getByLabelText('Trocar também a categoria de 1 lançamento que já tem uma'),
    )

    // O rótulo é quem diz o que vai acontecer.
    expect(screen.getByRole('button', { name: 'Marcar 1 e trocar 1' })).toBeInTheDocument()
    // Marcar a caixa não é um pedido: nenhuma escrita saiu.
    expect(corpos()).toHaveLength(1)
  })

  /** A confirmação PRÓPRIA da troca: é a primeira escrita do projeto autorizada
   *  a substituir escolha humana, e não existe desfazer. */
  it('a troca exige um segundo sim, que diz em português que não há desfazer', async () => {
    renderDialogo(respostaPadrao(2))

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    await userEvent.click(
      screen.getByLabelText('Trocar também a categoria de 1 lançamento que já tem uma'),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 e trocar 1' }))

    // Ainda NÃO gravou: o confirmar levou à pergunta.
    expect(corpos()).toHaveLength(1)

    const aviso = await screen.findByText('Isto troca uma categoria que já foi escolhida')
    expect(aviso).toBeInTheDocument()
    expect(screen.getByText(/Não dá para desfazer de uma vez/)).toBeInTheDocument()
    expect(screen.getByText(/recategorizar em Lançamentos, um a um/)).toBeInTheDocument()
    // A lista do que muda continua na tela.
    expect(screen.getByText('APLIC AUTOMATICA')).toBeInTheDocument()

    // E há caminho de volta que não grava nada.
    expect(screen.getByRole('button', { name: 'Voltar' })).toBeInTheDocument()
  })

  it('o segundo sim é o que manda overwriteCategorized true', async () => {
    const { onClose } = renderDialogo(respostaPadrao(2))

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    await userEvent.click(
      screen.getByLabelText('Trocar também a categoria de 1 lançamento que já tem uma'),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 e trocar 1' }))
    await screen.findByText('Isto troca uma categoria que já foi escolhida')
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 e trocar 1' }))

    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(corpos()[1]).toEqual({ month: '2026-09', dryRun: false, overwriteCategorized: true })
  })

  it('Voltar desfaz a pergunta sem gravar, e a prévia continua na tela', async () => {
    renderDialogo(respostaPadrao(2))

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    await userEvent.click(
      screen.getByLabelText('Trocar também a categoria de 1 lançamento que já tem uma'),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 e trocar 1' }))
    await screen.findByText('Isto troca uma categoria que já foi escolhida')
    await userEvent.click(screen.getByRole('button', { name: 'Voltar' }))

    expect(await screen.findByText('Viram aporte ou resgate · 1')).toBeInTheDocument()
    expect(corpos()).toHaveLength(1)
  })

  it('o toast usa o número do SERVIDOR, não o da prévia', async () => {
    // A prévia prometeu 1 + 1; o servidor recalculou e gravou 7. O toast diz 7.
    renderDialogo(respostaPadrao(7))

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 lançamento' }))

    expect(
      await screen.findByText('7 lançamentos marcados como aporte ou resgate.'),
    ).toBeInTheDocument()
  })

  it('sem nada a fazer, o confirmar é aria-disabled — nunca disabled', async () => {
    renderDialogo(() => jsonResponse(200, previa()))

    const confirmar = await screen.findByRole('button', { name: 'Nada a marcar' })
    // Fica focável e o rótulo explica; `disabled` tiraria o foco de quem navega
    // por teclado.
    expect(confirmar).toHaveAttribute('aria-disabled', 'true')
    expect(confirmar).not.toBeDisabled()
  })

  it('o vazio só aparece quando NÃO há troca possível', async () => {
    renderDialogo(() =>
      jsonResponse(
        200,
        previa({
          unmatched: 2,
          unmatchedItems: [
            semCategoria('i-3', 'PAGAMENTO DIVERSO', 'below_threshold'),
            semCategoria('i-4', 'OUTRO', 'ambiguous'),
          ],
        }),
      ),
    )

    expect(
      await screen.findByText('Nenhum lançamento receberia categoria de investimento.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Ir para categorias' })).toBeInTheDocument()
    // A auditoria fica num <details> fechado: só interessa a quem quer o motivo.
    expect(screen.getByText('Ver os 2 lançamentos e o motivo')).toBeInTheDocument()
  })

  it('com só o que trocar, o vazio NÃO aparece e o rótulo vira Trocar', async () => {
    // `marked` é 0, mas `alreadyCategorized` é 1: ainda há uma saída, e escondê-la
    // seria fechar a única que existe.
    renderDialogo(() =>
      jsonResponse(
        200,
        previa({ alreadyCategorized: 1, alreadyCategorizedItems: [jaTem('i-2', 'APLIC')] }),
      ),
    )

    await screen.findByText('Já têm categoria · 1')
    expect(
      screen.queryByText('Nenhum lançamento receberia categoria de investimento.'),
    ).not.toBeInTheDocument()

    await userEvent.click(
      screen.getByLabelText('Trocar também a categoria de 1 lançamento que já tem uma'),
    )
    expect(screen.getByRole('button', { name: 'Trocar 1 lançamento' })).toBeInTheDocument()
  })

  /** O 409 `CONFLICT` (mesma forma do ADR-028d).
   *
   *  O plano é calculado FORA da transação, e nessa janela a categoria de
   *  destino pode deixar de qualificar por qualquer um dos CINCO qualificadores
   *  do contrato: casa, viva, não arquivada, sem subcategoria ativa e de
   *  natureza compatível com o lote. O servidor reconfere dentro da transação e
   *  desfaz tudo. Três coisas precisam ser verdade na tela ao mesmo tempo, e é
   *  por elas juntas que este caso existe: a frase diz que NADA foi marcado, o
   *  diálogo NÃO fecha como se tivesse gravado, e nenhum toast de sucesso
   *  aparece. */
  it('o 409 diz que nada foi marcado, e o diálogo não fecha nem comemora', async () => {
    const { onClose } = renderDialogo((corpo) =>
      corpo.dryRun === true
        ? jsonResponse(200, PREVIA_COMPLETA)
        : jsonResponse(409, {
            error: { code: 'CONFLICT', message: 'destino deixou de ser atribuível' },
          }),
    )

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 lançamento' }))

    expect(
      await screen.findByText('As categorias mudaram enquanto a prévia estava aberta.'),
    ).toBeInTheDocument()
    expect(screen.getByText(/Nada foi marcado/)).toBeInTheDocument()

    // Não fechou, e não mentiu que gravou.
    expect(onClose).not.toHaveBeenCalled()
    expect(
      screen.queryByText('1 lançamento marcado como aporte ou resgate.'),
    ).not.toBeInTheDocument()

    // Nenhum nome nem id de categoria: o backend não os envia de propósito,
    // porque o erro atravessa o log.
    const aviso = screen.getByRole('alert')
    expect(aviso.textContent).not.toContain('CDB')
    expect(aviso.textContent).not.toContain('Outros')
    expect(aviso.textContent).not.toMatch(/[0-9a-f]{8}-[0-9a-f]{4}/)

    // Achado N4: a frase é AGNÓSTICA DE CAUSA. São cinco os qualificadores que
    // o servidor reconfere (casa, viva, não arquivada, sem subcategoria ativa,
    // natureza compatível) e o 409 não diz qual faltou — o contrato manda um
    // CONFLICT sem campos. Nomear dois deles, como a versão anterior desta copy
    // fazia, manda procurar no lugar errado quem levou 409 por arquivamento ou
    // por troca de natureza.
    expect(
      screen.getByText(
        'Nada foi marcado. A detecção só marca em categoria que pode receber lançamento, e uma das categorias desta prévia deixou de poder.',
      ),
    ).toBeInTheDocument()
    expect(aviso.textContent).not.toMatch(/exclu|subcategoria|arquiv|natureza/i)
  })

  it('"Conferir de novo" refaz a prévia em vez de gravar sobre uma prévia velha', async () => {
    renderDialogo((corpo) =>
      corpo.dryRun === true
        ? jsonResponse(200, PREVIA_COMPLETA)
        : jsonResponse(409, { error: { code: 'CONFLICT', message: 'x' } }),
    )

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 lançamento' }))
    await screen.findByText('As categorias mudaram enquanto a prévia estava aberta.')

    await userEvent.click(screen.getByRole('button', { name: 'Conferir de novo' }))

    // O terceiro pedido é uma PRÉVIA, e não uma segunda tentativa de gravar.
    await waitFor(() => expect(corpos()).toHaveLength(3))
    expect(corpos()[2]).toEqual({ month: '2026-09', dryRun: true, overwriteCategorized: false })
    // E o aviso sai da tela quando a prévia nova chega.
    await waitFor(() =>
      expect(
        screen.queryByText('As categorias mudaram enquanto a prévia estava aberta.'),
      ).not.toBeInTheDocument(),
    )
  })

  it('o 409 depois da troca volta para a prévia com a caixa DESMARCADA', async () => {
    // Autorização de sobrescrita não se herda de um pedido que não aconteceu:
    // a prévia nova pode ter outras contagens.
    renderDialogo((corpo) =>
      corpo.dryRun === true
        ? jsonResponse(200, PREVIA_COMPLETA)
        : jsonResponse(409, { error: { code: 'CONFLICT', message: 'x' } }),
    )

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    await userEvent.click(
      screen.getByLabelText('Trocar também a categoria de 1 lançamento que já tem uma'),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 e trocar 1' }))
    await screen.findByText('Isto troca uma categoria que já foi escolhida')
    await userEvent.click(screen.getByRole('button', { name: 'Marcar 1 e trocar 1' }))

    await screen.findByText('As categorias mudaram enquanto a prévia estava aberta.')
    await userEvent.click(screen.getByRole('button', { name: 'Conferir de novo' }))

    // Saiu da pergunta, voltou para a prévia, e a caixa está desmarcada.
    expect(await screen.findByText('Viram aporte ou resgate · 1')).toBeInTheDocument()
    expect(
      screen.getByLabelText('Trocar também a categoria de 1 lançamento que já tem uma'),
    ).not.toBeChecked()
    expect(screen.getByRole('button', { name: 'Marcar 1 lançamento' })).toBeInTheDocument()
  })

  it('o teto de 10.000 é um 422 em fields.month, e não oferece "tentar de novo"', async () => {
    renderDialogo(() =>
      jsonResponse(422, {
        error: {
          code: 'VALIDATION_FAILED',
          message: 'mês com lançamentos demais',
          fields: { month: 'too_many_candidates' },
        },
      }),
    )

    expect(
      await screen.findByText('Este mês tem lançamentos demais para detectar de uma vez.'),
    ).toBeInTheDocument()
    expect(
      screen.getByText(/O limite é 10\.000 por execução e nada foi alterado/),
    ).toBeInTheDocument()
    // Repetir daria o mesmo 422: o que muda o resultado é reduzir o trabalho.
    expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
  })

  it('erro comum da prévia mantém o caminho de tentar de novo', async () => {
    renderDialogo(() => jsonResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'falhou' } }))

    expect(
      await screen.findByText('Não foi possível conferir as palavras-chave.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()
  })

  it('a tabela é uma só, com as três colunas do desenho', async () => {
    renderDialogo(respostaPadrao(1))

    await screen.findByText('1 lançamento vira aporte ou resgate.')
    const tabela = screen.getByRole('table', { name: 'Prévia da detecção de investimentos' })
    const cabecalhos = within(tabela)
      .getAllByRole('columnheader')
      .map((celula) => celula.textContent)
    expect(cabecalhos).toEqual(['Descrição', 'Movimento', 'Categoria'])
  })
})

describe('copy do diálogo', () => {
  it('rotuloDoConfirmar cobre os cinco casos da tabela (k)', () => {
    const base = previa()
    expect(rotuloDoConfirmar(undefined, false)).toBe('Marcar')
    expect(rotuloDoConfirmar({ ...base, marked: 0 }, false)).toBe('Nada a marcar')
    expect(rotuloDoConfirmar({ ...base, marked: 1 }, false)).toBe('Marcar 1 lançamento')
    expect(rotuloDoConfirmar({ ...base, marked: 5 }, false)).toBe('Marcar 5 lançamentos')
    expect(rotuloDoConfirmar({ ...base, marked: 5, alreadyCategorized: 1 }, true)).toBe(
      'Marcar 5 e trocar 1',
    )
    expect(rotuloDoConfirmar({ ...base, marked: 0, alreadyCategorized: 3 }, true)).toBe(
      'Trocar 3 lançamentos',
    )
    // Caixa desmarcada não conta a troca, mesmo havendo candidatos.
    expect(rotuloDoConfirmar({ ...base, marked: 0, alreadyCategorized: 3 }, false)).toBe(
      'Nada a marcar',
    )
  })

  it('as frases concordam em número', () => {
    expect(fraseDoResumo(1)).toBe('1 lançamento vira aporte ou resgate.')
    expect(fraseDoResumo(5)).toBe('5 lançamentos viram aporte ou resgate.')
    expect(fraseDoToast(0)).toBe('Nada mudou — nenhuma descrição bateu com as palavras-chave.')
    expect(fraseDoToast(1)).toBe('1 lançamento marcado como aporte ou resgate.')
    expect(fraseDoToast(5)).toBe('5 lançamentos marcados como aporte ou resgate.')
  })
})
