import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { KeywordImportReport } from '@/api/types'
import {
  arvoreFixa,
  CONTA_INTER,
  CONTA_NUBANK,
  ENTRADA_MERCADO,
  ENTRADA_NUBANK,
  JSON_DA_IA,
  PADARIA,
  relatorioFixo,
} from '../fixtures'
import type { JanelaDeTrabalho } from '../janela'
import { ID_DO_TITULO_REPROCESSAR } from '../secoes'
import { SecaoImportar } from './SecaoImportar'

const fetchMock = vi.fn()

const JANELA: JanelaDeTrabalho = {
  fromMonth: '2026-07',
  toMonth: '2026-09',
  meses: ['2026-07', '2026-08', '2026-09'],
}

const OUTRA_JANELA: JanelaDeTrabalho = {
  fromMonth: '2026-09',
  toMonth: '2026-09',
  meses: ['2026-09'],
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

type Rotas = {
  preview?: (corpo: unknown) => Promise<Response>
  confirm?: (corpo: unknown) => Promise<Response>
}

/** Roteia o `fetch` pelo caminho. As listas de categorias e contas respondem
 *  sempre — são o cache que resolve o NOME do dono de uma palavra recusada. */
function rotearApi(rotas: Rotas) {
  fetchMock.mockImplementation((entrada: string, init?: RequestInit) => {
    const url = new URL(String(entrada), 'https://app.invalido')
    const corpo = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
    if (url.pathname.endsWith('/ai/keyword-import/preview')) {
      return (rotas.preview ?? (() => Promise.resolve(jsonResponse(200, relatorioFixo()))))(corpo)
    }
    if (url.pathname.endsWith('/ai/keyword-import/confirm')) {
      return (rotas.confirm ?? (() => Promise.resolve(jsonResponse(200, relatorioFixo()))))(corpo)
    }
    if (url.pathname.endsWith('/categories'))
      return Promise.resolve(jsonResponse(200, arvoreFixa()))
    if (url.pathname.endsWith('/accounts')) {
      return Promise.resolve(
        jsonResponse(200, {
          items: [
            { id: CONTA_NUBANK, name: 'Nubank' },
            { id: CONTA_INTER, name: 'Inter' },
          ],
        }),
      )
    }
    return Promise.resolve(jsonResponse(404, { error: { code: 'NOT_FOUND', message: '' } }))
  })
}

function corposDe(caminho: string): unknown[] {
  return fetchMock.mock.calls
    .filter((chamada) => String(chamada[0]).includes(caminho))
    .map((chamada) => JSON.parse(String((chamada[1] as RequestInit).body)))
}

function renderSecao(janela: JanelaDeTrabalho = JANELA) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const arvore = (j: JanelaDeTrabalho) => (
    <QueryClientProvider client={queryClient}>
      <SecaoImportar janela={j} />
      {/* O título da seção 3, que "Ir para Reprocessar" foca. */}
      <h2 id={ID_DO_TITULO_REPROCESSAR} tabIndex={-1}>
        3 · Reprocessar
      </h2>
    </QueryClientProvider>
  )
  const resultado = render(arvore(janela))
  return { ...resultado, trocarJanela: (j: JanelaDeTrabalho) => resultado.rerender(arvore(j)) }
}

/** Cola o JSON e clica em Conferir. */
async function conferir(user: ReturnType<typeof userEvent.setup>, texto = JSON_DA_IA) {
  const campo = screen.getByLabelText('Cole aqui o JSON que a IA respondeu')
  await user.click(campo)
  await user.paste(texto)
  await user.click(screen.getByRole('button', { name: 'Conferir' }))
}

describe('SecaoImportar', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('vazia: só o campo, a dica e o botão — sem EmptyState, sem disabled', () => {
    rotearApi({})
    renderSecao()

    const campo = screen.getByLabelText('Cole aqui o JSON que a IA respondeu')
    expect(campo.tagName).toBe('TEXTAREA')
    expect(campo).toHaveAccessibleDescription(
      'Só o JSON — do primeiro { ao último }. Se a IA escreveu texto antes ou depois, apague.',
    )
    const botao = screen.getByRole('button', { name: 'Cole o JSON para conferir' })
    expect(botao).toHaveAttribute('aria-disabled', 'true')
    expect(botao).not.toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Limpar' })).not.toBeInTheDocument()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(document.querySelector('[disabled]')).toBeNull()
  })

  /** Achado B2 do QA: pelo proxy, um corpo de 200 KB vira 502 e a tela
   *  ficaria em "tentar de novo" para sempre. A pré-checagem barra ANTES do
   *  pedido — nenhum `fetch` sai. */
  it('JSON acima de 128 KiB: barra no campo, sem nenhuma requisição', async () => {
    rotearApi({})
    const user = userEvent.setup()
    renderSecao()

    const molde = '{"homefinanceKeywordImport":1,"notes":""}'
    const grande = molde.replace('""', `"${'x'.repeat(131_073 - molde.length)}"`)
    await conferir(user, grande)

    const campo = screen.getByLabelText('Cole aqui o JSON que a IA respondeu')
    expect(campo).toHaveAttribute('aria-invalid', 'true')
    expect(campo).toHaveAccessibleDescription(
      'O JSON passa de 128 KB. Reduza o período e peça de novo.',
    )
    expect(campo).toHaveFocus()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('JSON inválido: o erro mora no campo, o foco volta ao campo e nada é enviado', async () => {
    rotearApi({})
    const user = userEvent.setup()
    renderSecao()

    await conferir(user, 'Aqui está o JSON: {"homefinanceKeywordImport": 1')

    const campo = screen.getByLabelText('Cole aqui o JSON que a IA respondeu')
    expect(campo).toHaveAttribute('aria-invalid', 'true')
    expect(campo).toHaveAccessibleDescription(
      'Isso não é um JSON válido. Cole o texto da resposta inteiro, do primeiro { ao último }.',
    )
    expect(campo).toHaveFocus()
    expect(corposDe('/ai/keyword-import')).toHaveLength(0)
    // Nunca toast, nunca Alert no topo.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('manda o envelope com o payload como veio e a janela, e desenha os três blocos', async () => {
    rotearApi({})
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)

    // O envelope: o JSON da IA byte a byte (inclusive `notes`), a janela, e
    // nada de `skipNewCategories` na prévia.
    await waitFor(() => expect(corposDe('/ai/keyword-import/preview')).toHaveLength(1))
    expect(corposDe('/ai/keyword-import/preview')[0]).toEqual({
      payload: JSON.parse(JSON_DA_IA),
      fromMonth: '2026-07',
      toMonth: '2026-09',
    })

    // Totais do servidor, numa frase, `role="status"`.
    const totais = await screen.findByText(
      '3 categorias novas · 17 palavras entram · 6 já estavam lá · 4 recusadas.',
    )
    expect(totais).toHaveAttribute('role', 'status')

    // Bloco A: três caixas nativas, marcadas, com o caminho e a contagem.
    expect(
      screen.getByRole('heading', { level: 3, name: 'Categorias a criar · 3' }),
    ).toBeInTheDocument()
    const caixas = screen.getAllByRole('checkbox')
    expect(caixas.map((c) => c.getAttribute('aria-label'))).toEqual([
      'Criar Alimentação > Padaria com 2 palavras-chave',
      'Criar Saúde > Farmácia com 3 palavras-chave',
      'Criar Saúde > Academia sem palavra-chave',
    ])
    for (const caixa of caixas) expect(caixa).toBeChecked()
    expect(screen.getAllByText('Grupo novo · natureza: despesa')).toHaveLength(2)
    // As palavras são texto citado, não fichas.
    expect(screen.getAllByText('«padaria» · «panificadora»').length).toBeGreaterThan(0)

    // Bloco B: agrupado por conta (ordem do JSON), UMA linha por palavra em
    // impacto decrescente — e só a palavra genérica chama atenção, não a conta.
    expect(
      screen.getByRole('heading', { level: 3, name: 'Palavras-chave de conta · 5' }),
    ).toBeInTheDocument()
    const tabelaDeContas = screen.getByRole('table', {
      name: 'Palavras-chave de conta e o impacto no período',
    })
    expect(tabelaDeContas).toHaveTextContent('Inter · 2')
    expect(tabelaDeContas).toHaveTextContent('Nubank · 3')
    // As linhas de DADO são as que têm duas células (`td`): a do cabeçalho e
    // as dos grupos têm `th`.
    const linhas = within(tabelaDeContas)
      .getAllByRole('row')
      .filter((linha) => linha.querySelectorAll('td').length === 2)
    expect(linhas.map((linha) => linha.querySelector('td')?.firstChild?.textContent)).toEqual([
      '«pagamento»',
      '«banco inter»',
      '«nu pagamentos»',
      '«nubank»',
      '«nu invest»',
    ])
    expect(linhas.map((linha) => linha.getAttribute('data-atencao'))).toEqual([
      'true',
      null,
      null,
      null,
      null,
    ])
    expect(linhas[0]).toHaveTextContent('87 de 212 lançamentos do período')
    expect(linhas[0]).toHaveTextContent(
      'Palavra genérica: «pagamento» casa com 87 dos 212 lançamentos do período. Para deixá-la de fora, apague-a do JSON e confira de novo.',
    )
    expect(linhas[1]).toHaveTextContent('6 de 212')
    expect(linhas[1]).not.toHaveTextContent('genérica')
    expect(linhas[2]).toHaveTextContent('4 de 212')
    // O total do item (a união, 89) NÃO aparece: o número que importa é o da palavra.
    expect(tabelaDeContas).not.toHaveTextContent('89')

    // Bloco C: uma <dl>, com a mesclada e a nota.
    expect(
      screen.getByRole('heading', { level: 3, name: 'Palavras-chave de categoria · 3' }),
    ).toBeInTheDocument()
    const lista = screen.getByRole('heading', { level: 3, name: 'Palavras-chave de categoria · 3' })
      .parentElement?.parentElement
    expect(lista?.querySelector('dl')).not.toBeNull()
    expect(screen.getByText('«ifood» · «rappi»')).toBeInTheDocument()
    expect(screen.getByText('já existia — as palavras vão para ela')).toBeInTheDocument()

    // O que fica de fora: <details> fechado, sem controle nenhum.
    const resumo = screen.getByText('Ver o que não entra · 11')
    const detalhes = resumo.closest('details')
    expect(detalhes).not.toHaveAttribute('open')
    expect(within(detalhes as HTMLElement).queryAllByRole('checkbox')).toHaveLength(0)
    expect(within(detalhes as HTMLElement).queryAllByRole('button')).toHaveLength(0)

    // A barra: nuance e rótulo que diz a saída.
    expect(screen.getByText('Inclui 1 grupo novo.')).toBeInTheDocument()
    const confirmar = screen.getByRole('button', {
      name: 'Criar 3 categorias e gravar 17 palavras',
    })
    expect(confirmar).not.toHaveAttribute('aria-disabled')

    // Nenhum `disabled` na seção inteira.
    expect(document.querySelector('[disabled]')).toBeNull()
    // `notes` nunca aparece — fora do próprio campo, onde é o texto colado.
    expect(
      screen.queryByText(/explicação que a IA sempre quer dar/, {
        ignore: 'script, style, textarea',
      }),
    ).not.toBeInTheDocument()
  })

  it('a palavra recusada por keyword_taken diz de quem é, pelo cache — nunca pelo JSON', async () => {
    rotearApi({})
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)
    await screen.findByText('Ver o que não entra · 11')

    expect(await screen.findByText('já está em Alimentação > Padaria')).toBeInTheDocument()
    // `pix` nas duas contas: as DUAS recusadas, cada uma com o motivo.
    expect(screen.getAllByText('aparece em dois itens no mesmo JSON')).toHaveLength(2)
    // Nenhum motivo cru.
    expect(
      screen.queryByText(/keyword_taken|ambiguous_in_payload|invalid_keyword/),
    ).not.toBeInTheDocument()
  })

  /** Aceite 48: desmarcar recalcula o rótulo do botão E monta
   *  `skipNewCategories` com os `ref` do servidor. */
  it('desmarcar recalcula o botão e o confirm leva os refs do servidor com o MESMO payload', async () => {
    const relatorioDoConfirm: KeywordImportReport = relatorioFixo({
      totals: { categoriesCreated: 2, added: 15, skipped: 6, rejected: 4, periodTransactions: 212 },
      newCategories: [
        ...relatorioFixo().newCategories.filter((e) => e.ref !== PADARIA.ref),
        { ...PADARIA, outcome: 'skipped_by_user', add: [] },
      ],
    })
    rotearApi({ confirm: () => Promise.resolve(jsonResponse(200, relatorioDoConfirm)) })
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)
    await screen.findByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' })

    await user.click(
      screen.getByRole('checkbox', { name: 'Criar Alimentação > Padaria com 2 palavras-chave' }),
    )

    // 17 → 15: as duas palavras da Padaria foram junto.
    expect(
      screen.getByRole('button', { name: 'Criar 2 categorias e gravar 15 palavras' }),
    ).toBeInTheDocument()
    expect(
      screen.getByText('2 categorias novas · 15 palavras entram · 6 já estavam lá · 4 recusadas.'),
    ).toHaveAttribute('role', 'status')
    // A linha desmarcada é apagada, mas continua lá — e marcável.
    const caixa = screen.getByRole('checkbox', {
      name: 'Criar Alimentação > Padaria com 2 palavras-chave',
    })
    expect(caixa).not.toBeChecked()
    expect(caixa.closest('tr')).toHaveAttribute('data-desmarcada', 'true')

    await user.click(
      screen.getByRole('button', { name: 'Criar 2 categorias e gravar 15 palavras' }),
    )

    await waitFor(() => expect(corposDe('/ai/keyword-import/confirm')).toHaveLength(1))
    expect(corposDe('/ai/keyword-import/confirm')[0]).toEqual({
      payload: JSON.parse(JSON_DA_IA),
      fromMonth: '2026-07',
      toMonth: '2026-09',
      skipNewCategories: ['alimentacao > padaria'],
    })

    // O relatório final usa os números do CONFIRM (2 e 15), não os da prévia.
    expect(await screen.findByText('2 categorias criadas e 15 palavras gravadas.')).toHaveAttribute(
      'role',
      'status',
    )
    const relatorio = screen.getByText('2 categorias criadas e 15 palavras gravadas.').parentElement
    const dl = relatorio?.querySelector('dl')
    expect(dl?.textContent).toContain('Categorias criadas2')
    expect(dl?.textContent).toContain('Palavras gravadas15')
    expect(dl?.textContent).toContain('Já estavam lá6')
    expect(dl?.textContent).toContain('Recusadas4')
    expect(dl?.textContent).toContain('Desmarcadas por você1')
    // O campo e a prévia deram lugar ao relatório; sem toast.
    expect(screen.queryByLabelText('Cole aqui o JSON que a IA respondeu')).not.toBeInTheDocument()
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0)
    expect(screen.getByRole('button', { name: 'Ir para Reprocessar' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Importar outro JSON' })).toBeInTheDocument()
  })

  it('o relatório final não renderiza linha zerada', async () => {
    rotearApi({
      confirm: () =>
        Promise.resolve(
          jsonResponse(
            200,
            relatorioFixo({
              totals: {
                categoriesCreated: 0,
                added: 12,
                skipped: 0,
                rejected: 0,
                periodTransactions: 212,
              },
              newCategories: [],
            }),
          ),
        ),
    })
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)
    await user.click(
      await screen.findByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' }),
    )

    const frase = await screen.findByText('12 palavras gravadas.')
    const dl = frase.parentElement?.querySelector('dl')
    expect(dl?.querySelectorAll('dt')).toHaveLength(1)
    expect(dl?.textContent).toBe('Palavras gravadas12')
  })

  it('"Desmarcar todas" e "Marcar todas" alternam, e sem categoria o rótulo vira "Gravar N palavras"', async () => {
    rotearApi({})
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)
    await user.click(await screen.findByRole('button', { name: 'Desmarcar todas' }))

    for (const caixa of screen.getAllByRole('checkbox')) expect(caixa).not.toBeChecked()
    expect(screen.getByRole('button', { name: 'Gravar 12 palavras' })).toBeInTheDocument()
    expect(screen.queryByText(/Inclui/)).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Marcar todas' }))
    for (const caixa of screen.getAllByRole('checkbox')) expect(caixa).toBeChecked()
    expect(
      screen.getByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' }),
    ).toBeInTheDocument()
  })

  it('janela sem lançamento: o bloco B diz que não houve o que medir, em vez de "0 de 0"', async () => {
    rotearApi({
      preview: () =>
        Promise.resolve(
          jsonResponse(
            200,
            relatorioFixo({
              totals: { ...relatorioFixo().totals, periodTransactions: 0 },
              items: [
                {
                  ...ENTRADA_NUBANK,
                  impact: {
                    transferCandidates: 0,
                    byKeyword: [{ keyword: 'nu pagamentos', transferCandidates: 0 }],
                  },
                  added: ['nu pagamentos'],
                },
              ],
            }),
          ),
        ),
    })
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)
    const tabela = await screen.findByRole('table', {
      name: 'Palavras-chave de conta e o impacto no período',
    })
    expect(tabela).toHaveTextContent('sem lançamentos no período')
    expect(tabela).not.toHaveTextContent('0 de 0')
    expect(tabela.querySelector('tr[data-atencao="true"]')).toBeNull()
  })

  it('prévia sem nada para aplicar: a frase diz isso e o botão não confirma', async () => {
    rotearApi({
      preview: () =>
        Promise.resolve(
          jsonResponse(
            200,
            relatorioFixo({
              totals: {
                categoriesCreated: 0,
                added: 0,
                skipped: 6,
                rejected: 4,
                periodTransactions: 212,
              },
              newCategories: [],
              items: [{ ...ENTRADA_MERCADO, added: [] }],
            }),
          ),
        ),
    })
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)

    expect(await screen.findByText('Nada deste JSON pode ser aplicado.')).toHaveAttribute(
      'role',
      'status',
    )
    const botao = screen.getByRole('button', { name: 'Nada para aplicar' })
    expect(botao).toHaveAttribute('aria-disabled', 'true')
    expect(botao).not.toBeDisabled()
    await user.click(botao)
    expect(corposDe('/ai/keyword-import/confirm')).toHaveLength(0)
  })

  it.each([
    [
      'versão ausente',
      {
        error: {
          code: 'VALIDATION_FAILED',
          message: 'x',
          fields: { 'payload.homefinanceKeywordImport': 'x' },
        },
      },
      400,
      'Falta a linha "homefinanceKeywordImport": 1 no começo do JSON — a IA respondeu noutro formato.',
    ],
    [
      'listas vazias',
      { error: { code: 'VALIDATION_FAILED', message: 'x', fields: { payload: 'x' } } },
      400,
      'O JSON não traz nenhuma palavra-chave nem categoria.',
    ],
    [
      'campo desconhecido',
      { error: { code: 'VALIDATION_FAILED', message: 'x' } },
      400,
      'O JSON traz um campo que este app não aceita. Peça à IA para responder só com o formato do prompt.',
    ],
    [
      '413',
      { error: { code: 'PAYLOAD_TOO_LARGE', message: 'x' } },
      413,
      'O JSON passa de 128 KB. Reduza o período e peça de novo.',
    ],
    [
      '429',
      { error: { code: 'RATE_LIMITED', message: 'x' } },
      429,
      'Muitas conferências seguidas. Tente de novo em um minuto.',
    ],
  ])('%s: o erro mora no campo e o foco volta a ele', async (_, corpo, status, mensagem) => {
    rotearApi({ preview: () => Promise.resolve(jsonResponse(status, corpo)) })
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)

    const campo = screen.getByLabelText('Cole aqui o JSON que a IA respondeu')
    await waitFor(() => expect(campo).toHaveAttribute('aria-invalid', 'true'))
    expect(campo).toHaveAccessibleDescription(mensagem)
    expect(campo).toHaveFocus()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    // O texto colado continua no campo, para corrigir.
    expect(campo).toHaveValue(JSON_DA_IA)
  })

  it('500 vira Alert dentro da seção, com "Tentar de novo" que refaz o pedido', async () => {
    let tentativas = 0
    rotearApi({
      preview: () => {
        tentativas += 1
        return Promise.resolve(
          tentativas === 1
            ? jsonResponse(500, { error: { code: 'INTERNAL_ERROR', message: '' } })
            : jsonResponse(200, relatorioFixo()),
        )
      },
    })
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)

    expect(await screen.findByText('Não foi possível conferir o JSON.')).toBeInTheDocument()
    expect(screen.getByLabelText('Cole aqui o JSON que a IA respondeu')).not.toHaveAttribute(
      'aria-invalid',
    )
    await user.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    expect(
      await screen.findByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' }),
    ).toBeInTheDocument()
    expect(screen.queryByText('Não foi possível conferir o JSON.')).not.toBeInTheDocument()
  })

  it('enquanto confere: a frase no status e UMA tabela em carregamento', async () => {
    rotearApi({ preview: () => new Promise<Response>(() => {}) })
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)

    expect(await screen.findByText('Conferindo o que a IA respondeu…')).toHaveAttribute(
      'role',
      'status',
    )
    expect(document.querySelectorAll('[aria-busy="true"] table')).toHaveLength(1)
    expect(document.querySelector('[disabled]')).toBeNull()
  })

  /** Regra de frescor: o impacto foi medido num período. Trocar a janela
   *  descarta a prévia, mantém o JSON e diz por quê. */
  it('trocar a janela descarta a prévia, mantém o JSON colado e avisa', async () => {
    rotearApi({})
    const user = userEvent.setup()
    const { trocarJanela } = renderSecao()

    await conferir(user)
    await screen.findByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' })

    trocarJanela(OUTRA_JANELA)

    expect(
      screen.getByText('A janela mudou — confira de novo para medir o impacto no novo período.'),
    ).toHaveAttribute('role', 'status')
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0)
    expect(screen.queryByText(/de 212/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Criar 3 categorias/ })).not.toBeInTheDocument()
    expect(screen.getByLabelText('Cole aqui o JSON que a IA respondeu')).toHaveValue(JSON_DA_IA)

    // Conferir de novo mede no período novo — e o aviso some.
    await user.click(screen.getByRole('button', { name: 'Conferir' }))
    await waitFor(() => expect(corposDe('/ai/keyword-import/preview')).toHaveLength(2))
    expect(corposDe('/ai/keyword-import/preview')[1]).toMatchObject({
      fromMonth: '2026-09',
      toMonth: '2026-09',
    })
    await screen.findByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' })
    expect(screen.queryByText(/A janela mudou/)).not.toBeInTheDocument()
  })

  it('409 no confirm: o estado mudou, e a saída é conferir de novo', async () => {
    rotearApi({
      confirm: () =>
        Promise.resolve(jsonResponse(409, { error: { code: 'CONFLICT', message: '' } })),
    })
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)
    await user.click(
      await screen.findByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' }),
    )

    expect(await screen.findByText('O estado mudou desde a conferência.')).toBeInTheDocument()
    expect(
      screen.getByText('Os dados mudaram enquanto a operação rodava. Confira a prévia de novo.'),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Conferir de novo' }))
    await waitFor(() => expect(corposDe('/ai/keyword-import/preview')).toHaveLength(2))
  })

  /** 422 em `fields.toMonth` (contrato): a janela é grande demais para a
   *  medição. Sem "Tentar de novo" — daria o mesmo 422 —, e o aviso some
   *  quando a janela muda, porque a ação era justamente essa. */
  it('422 na prévia: aviso sem "tentar de novo", que some ao trocar a janela', async () => {
    rotearApi({
      preview: () =>
        Promise.resolve(
          jsonResponse(422, {
            error: { code: 'VALIDATION_FAILED', message: '', fields: { toMonth: 'x' } },
          }),
        ),
    })
    const user = userEvent.setup()
    const { trocarJanela } = renderSecao()

    await conferir(user)

    expect(await screen.findByText('A janela é grande demais para conferir.')).toBeInTheDocument()
    expect(
      screen.getByText(
        'A janela tem lançamentos demais para medir o impacto. Escolha um período menor no alto da página e confira de novo.',
      ),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('Cole aqui o JSON que a IA respondeu')).not.toHaveAttribute(
      'aria-invalid',
    )

    trocarJanela(OUTRA_JANELA)
    expect(screen.queryByText('A janela é grande demais para conferir.')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Cole aqui o JSON que a IA respondeu')).toHaveValue(JSON_DA_IA)
  })

  it('422 no confirm: o mesmo aviso, sem "tentar de novo"', async () => {
    rotearApi({
      confirm: () =>
        Promise.resolve(
          jsonResponse(422, {
            error: { code: 'VALIDATION_FAILED', message: '', fields: { toMonth: 'x' } },
          }),
        ),
    })
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)
    await user.click(
      await screen.findByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' }),
    )

    expect(await screen.findByText('A janela é grande demais para aplicar.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
    // A prévia continua na tela: nada foi gravado.
    expect(screen.getAllByRole('checkbox')).toHaveLength(3)
  })

  it('"Limpar" zera campo e prévia e devolve o foco ao campo', async () => {
    rotearApi({})
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)
    await screen.findByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' })

    await user.click(screen.getByRole('button', { name: 'Limpar' }))

    const campo = screen.getByLabelText('Cole aqui o JSON que a IA respondeu')
    expect(campo).toHaveValue('')
    expect(campo).toHaveFocus()
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0)
    expect(screen.queryByRole('button', { name: 'Limpar' })).not.toBeInTheDocument()
  })

  it('depois de aplicar: "Ir para Reprocessar" foca o título da seção 3 e "Importar outro JSON" volta ao campo', async () => {
    rotearApi({})
    const user = userEvent.setup()
    renderSecao()

    await conferir(user)
    await user.click(
      await screen.findByRole('button', { name: 'Criar 3 categorias e gravar 17 palavras' }),
    )
    await screen.findByText('3 categorias criadas e 17 palavras gravadas.')

    await user.click(screen.getByRole('button', { name: 'Ir para Reprocessar' }))
    expect(screen.getByRole('heading', { level: 2, name: '3 · Reprocessar' })).toHaveFocus()

    await user.click(screen.getByRole('button', { name: 'Importar outro JSON' }))
    const campo = await screen.findByLabelText('Cole aqui o JSON que a IA respondeu')
    expect(campo).toHaveValue('')
    await waitFor(() => expect(campo).toHaveFocus())
  })
})
