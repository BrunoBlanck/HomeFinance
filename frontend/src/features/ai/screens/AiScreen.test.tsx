import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AiExportPrompt } from '@/api/types'
import { createAppRouter } from '@/app/router'

const fetchMock = vi.fn()

const SESSAO = {
  user: {
    id: '11111111-1111-4111-8111-111111111111',
    name: 'Bruno Blanck',
    email: 'bruno@example.com',
    emailVerifiedAt: '2026-09-09T12:00:00Z',
    createdAt: '2026-09-09T12:00:00Z',
  },
  household: {
    id: '22222222-2222-4222-8222-222222222222',
    name: 'Casa de Bruno',
    role: 'owner',
    timezone: 'America/Sao_Paulo',
    currency: 'BRL',
  },
  households: [],
}

/** Um texto pequeno mas com estrutura de verdade: três linhas, acento e um
 *  bloco de código — é o que vai ser comparado byte a byte entre o que está na
 *  tela, o que vai para o clipboard e o que entra no `.md`. */
const TEXTO = '# HomeFinance\n\nVocê é um assistente.\n\n```json\n{ "ok": true }\n```\n'

function prompt(parcial: Partial<AiExportPrompt> = {}): AiExportPrompt {
  return {
    prompt: TEXTO,
    fromMonth: '2026-07',
    toMonth: '2026-09',
    generatedAt: '2026-09-21T12:00:00Z',
    stats: {
      accounts: 4,
      categories: 41,
      descriptions: 32,
      transactions: 540,
      truncatedDescriptions: 0,
    },
    ...parcial,
  }
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** Uma resposta NOVA por chamada, roteada pelo caminho: o corpo de um
 *  `Response` só pode ser lido uma vez, e a casca pede `/me` ao lado da tela. */
function rotearApi(exportacao: () => Promise<Response>) {
  fetchMock.mockImplementation((entrada: string) => {
    const url = new URL(String(entrada), 'https://app.invalido')
    if (url.pathname.endsWith('/ai/export-prompt')) return exportacao()
    return Promise.resolve(jsonResponse(200, SESSAO))
  })
}

function renderIa(entrada = '/ia') {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [entrada] }))
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

/** O clipboard não existe no jsdom. Instalamos um espião e devolvemos o
 *  controle de sucesso/falha para o caso de teste — a falha é caminho real
 *  (permissão negada, contexto inseguro, navegador antigo). */
function instalarClipboard(comportamento: 'ok' | 'falha') {
  const writeText = vi.fn(() =>
    comportamento === 'ok' ? Promise.resolve() : Promise.reject(new Error('negado')),
  )
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
    writable: true,
  })
  return writeText
}

type Baixado = { download: string; tipo: string; conteudo: string }

/** Espia o download inteiro: o Blob que virou URL, o nome do arquivo e o
 *  `revokeObjectURL`. É assim que se afirma "o `.md` tem o MESMO conteúdo". */
function instalarDownload() {
  const capturado: { blob: Blob | null; url: string | null; revogado: string | null } = {
    blob: null,
    url: null,
    revogado: null,
  }
  const cliques: Array<{ download: string; href: string }> = []

  vi.spyOn(URL, 'createObjectURL').mockImplementation((objeto: Blob | MediaSource) => {
    capturado.blob = objeto as Blob
    capturado.url = 'blob:homefinance/1'
    return capturado.url
  })
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation((url: string) => {
    capturado.revogado = url
  })
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    cliques.push({ download: this.download, href: this.getAttribute('href') ?? '' })
  })

  return {
    cliques,
    capturado,
    async arquivo(): Promise<Baixado> {
      const blob = capturado.blob
      const clique = cliques[0]
      if (!blob || !clique) throw new Error('nenhum download aconteceu')
      return { download: clique.download, tipo: blob.type, conteudo: await blob.text() }
    },
  }
}

describe('AiScreen', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-09-15T12:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('abre com a janela padrão de 3 meses e pede o prompt do período', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    renderIa()

    expect(await screen.findByRole('heading', { level: 1, name: 'IA' })).toBeInTheDocument()
    expect(document.title).toBe('IA · HomeFinance')

    // A janela é UMA, no topo, e diz os meses RESOLVIDOS.
    const periodo = screen.getByLabelText('Período')
    expect(periodo).toHaveValue('3')
    expect(
      within(periodo as HTMLSelectElement).getByRole('option', {
        name: '3 meses · julho a setembro',
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        'Vale para as três seções. A janela termina no mês escolhido no alto da página.',
      ),
    ).toBeInTheDocument()

    await waitFor(() => expect(chamadasDe('/ai/export-prompt')).toHaveLength(1))
    const [url] = chamadasDe('/ai/export-prompt')[0] as [string]
    expect(url).toContain('fromMonth=2026-07')
    expect(url).toContain('toMonth=2026-09')
  })

  /** Aceite 50 da spec 0010: o aviso de envio a terceiros está visível **antes**
   *  de qualquer botão de copiar ou baixar. Aqui isso é afirmado na ORDEM DO
   *  DOM, que é a ordem que o leitor de tela e o Tab seguem — "está na tela"
   *  não bastaria: um aviso embaixo do botão também está na tela.
   *
   *  As **três** linhas são afirmadas, e a do meio é a que a revisão de
   *  segurança exigiu (21/09/2026): o sanitizador da importação mantém o nome
   *  da contraparte e a mensagem do Pix dentro da descrição
   *  (`Pix enviado - Fulano de Tal`), então um aviso que dissesse "não vão
   *  nomes" seria falso justamente sobre o dado de TERCEIRO que mais sai
   *  daqui — e a mitigação inteira desta feature é o consentimento informado. */
  it('as três linhas do aviso vêm antes de qualquer botão, e dizem a verdade', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    renderIa()

    const linhaDoEnvio = await screen.findByText(/Este texto leva/)
    expect(linhaDoEnvio).toHaveTextContent(
      'Este texto leva as descrições e os valores das suas movimentações do período. O aplicativo não envia nada: quem copia e cola numa IA de fora é você.',
    )
    const linhaDoQueVai = screen.getByText(
      'As descrições vão como você as vê no app — e costumam trazer o nome de quem pagou ou recebeu, e o que a pessoa escreveu na mensagem do Pix.',
    )
    const linhaDoQueNaoVai = screen.getByText(
      'Não vão: saldos, instituição, agência e número de conta, dias de fatura, seu nome e e-mail de cadastro, nem identificador de lançamento.',
    )

    // Guarda de regressão da redação antiga, que NEGAVA o que de fato sai.
    expect(screen.queryByText(/Não vão no texto: saldos, nomes, e-mails/)).not.toBeInTheDocument()

    // `querySelectorAll('*')` devolve a árvore em ORDEM DE DOCUMENTO — a mesma
    // ordem que o Tab e o leitor de tela percorrem.
    const emOrdem = Array.from(document.querySelectorAll('*'))
    for (const nome of ['Copiar o prompt', 'Baixar .md']) {
      const botao = await screen.findByRole('button', { name: nome })
      for (const linha of [linhaDoEnvio, linhaDoQueVai, linhaDoQueNaoVai]) {
        expect(emOrdem.indexOf(linha)).toBeGreaterThanOrEqual(0)
        expect(emOrdem.indexOf(linha)).toBeLessThan(emOrdem.indexOf(botao))
      }
    }
  })

  it('mostra as contagens numa linha e o tamanho do texto no <details>', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    renderIa()

    // 7 linhas: o texto tem 8 quebras contando a final, que não conta.
    expect(
      await screen.findByText('7 linhas · 32 descrições distintas · 4 contas · 41 categorias'),
    ).toBeInTheDocument()
    expect(screen.getByText('Ver o texto (7 linhas)')).toBeInTheDocument()
  })

  it('declara o corte quando o corpus não coube inteiro', async () => {
    rotearApi(() =>
      Promise.resolve(
        jsonResponse(200, prompt({ stats: { ...prompt().stats, truncatedDescriptions: 12 } })),
      ),
    )
    renderIa()

    expect(
      await screen.findByText(
        '7 linhas · 32 descrições distintas · 4 contas · 41 categorias · 12 descrições ficaram de fora (as menos frequentes)',
      ),
    ).toBeInTheDocument()
  })

  it('período sem movimentação continua gerando prompt, e a linha diz isso', async () => {
    rotearApi(() =>
      Promise.resolve(
        jsonResponse(
          200,
          prompt({
            stats: {
              accounts: 4,
              categories: 41,
              descriptions: 0,
              transactions: 0,
              truncatedDescriptions: 0,
            },
          }),
        ),
      ),
    )
    renderIa()

    expect(
      await screen.findByText(
        '7 linhas · 4 contas · 41 categorias · nenhuma movimentação no período',
      ),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Copiar o prompt' })).not.toHaveAttribute(
      'aria-disabled',
      'true',
    )
  })

  /** Carregando: `aria-disabled` nos botões — **nunca** `disabled` — e um
   *  `role="status"` dizendo o que está sendo montado e de que período. */
  it('enquanto monta, anuncia o período e os botões ficam aria-disabled (não disabled)', async () => {
    rotearApi(() => new Promise<Response>(() => {}))
    renderIa()

    // Por TEXTO, e não por `getByRole('status')`: a tela tem outras duas live
    // regions legítimas (o mês do cabeçalho e o `<output>` da cópia), e pegar
    // "a" status seria pegar a errada.
    const status = await screen.findByText(/Montando o prompt de julho a setembro/)
    expect(status).toHaveAttribute('role', 'status')

    for (const nome of ['Copiar o prompt', 'Baixar .md']) {
      const botao = screen.getByRole('button', { name: nome })
      expect(botao).toHaveAttribute('aria-disabled', 'true')
      expect(botao).not.toBeDisabled()
    }
  })

  it('o erro tem título próprio e saída, e nenhum botão de copiar sobrevive', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } })))
    renderIa()

    expect(await screen.findByText('Não foi possível montar o prompt.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Copiar o prompt' })).not.toBeInTheDocument()
  })

  it('"Tentar de novo" refaz o pedido', async () => {
    let tentativas = 0
    rotearApi(() => {
      tentativas += 1
      return Promise.resolve(
        tentativas === 1
          ? jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } })
          : jsonResponse(200, prompt()),
      )
    })
    const user = userEvent.setup()
    renderIa()

    await screen.findByText('Não foi possível montar o prompt.')
    await user.click(screen.getByRole('button', { name: 'Tentar de novo' }))

    expect(await screen.findByRole('button', { name: 'Copiar o prompt' })).toBeInTheDocument()
  })

  /** Aceite 49 (metade 1): o clipboard recebe EXATAMENTE o texto exibido. */
  it('copia o mesmo texto que está no <pre>, e confirma sem trocar o rótulo do botão', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    // A ORDEM importa: `userEvent.setup()` instala um stub próprio de
    // `navigator.clipboard`, então o nosso tem de vir DEPOIS — senão o espião
    // fica embaixo do dele e nunca é chamado.
    const user = userEvent.setup()
    const writeText = instalarClipboard('ok')
    renderIa()

    const copiar = await screen.findByRole('button', { name: 'Copiar o prompt' })
    await user.click(copiar)

    expect(writeText).toHaveBeenCalledTimes(1)
    expect(writeText).toHaveBeenCalledWith(TEXTO)
    // O que está na tela é o mesmo objeto de texto.
    expect(screen.getByLabelText('Texto do prompt')).toHaveTextContent(/Você é um assistente/)

    expect(await screen.findByText('Copiado.')).toBeInTheDocument()
    // O rótulo NÃO muda: o botão continua sendo o botão de copiar.
    expect(screen.getByRole('button', { name: 'Copiar o prompt' })).toBeInTheDocument()

    // E some sozinho depois de 6 s.
    await act(async () => {
      vi.advanceTimersByTime(6_000)
    })
    await waitFor(() => expect(screen.queryByText('Copiado.')).not.toBeInTheDocument())
  })

  /** Falha do clipboard é caminho real, não borda exótica: contexto inseguro,
   *  permissão negada, navegador antigo. A tela abre a saída manual. */
  it('falhando ao copiar, abre "Ver o texto" sozinha e diz o que fazer', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    const user = userEvent.setup()
    instalarClipboard('falha')
    renderIa()

    const detalhes = (await screen.findByText(/Ver o texto/)).closest('details')
    expect(detalhes).not.toBeNull()
    expect(detalhes).not.toHaveAttribute('open')

    await user.click(screen.getByRole('button', { name: 'Copiar o prompt' }))

    expect(
      await screen.findByText('Não consegui copiar. Abra "Ver o texto" e copie à mão.'),
    ).toBeInTheDocument()
    expect(detalhes).toHaveAttribute('open')

    // E a mensagem de falha NÃO some com o tempo: ela pede uma ação.
    await act(async () => {
      vi.advanceTimersByTime(10_000)
    })
    expect(
      screen.getByText('Não consegui copiar. Abra "Ver o texto" e copie à mão.'),
    ).toBeInTheDocument()
  })

  /** Aceite 49 (metade 2): o `.md` tem o MESMO conteúdo, e o nome carrega a
   *  janela. */
  it('baixa um .md com o mesmo conteúdo e o nome da janela, e revoga a URL', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    const download = instalarDownload()
    const user = userEvent.setup()
    renderIa()

    await user.click(await screen.findByRole('button', { name: 'Baixar .md' }))

    const arquivo = await download.arquivo()
    expect(arquivo.conteudo).toBe(TEXTO)
    expect(arquivo.download).toBe('homefinance-prompt-2026-07-a-2026-09.md')
    expect(arquivo.tipo).toBe('text/markdown;charset=utf-8')
    expect(download.capturado.revogado).toBe(download.capturado.url)
  })

  it('não copia nem baixa enquanto o prompt não chegou', async () => {
    rotearApi(() => new Promise<Response>(() => {}))
    const user = userEvent.setup()
    const writeText = instalarClipboard('ok')
    const download = instalarDownload()
    renderIa()

    await user.click(await screen.findByRole('button', { name: 'Copiar o prompt' }))
    await user.click(screen.getByRole('button', { name: 'Baixar .md' }))

    expect(writeText).not.toHaveBeenCalled()
    expect(download.cliques).toHaveLength(0)
  })

  it('trocar a janela troca o período pedido e a URL, sem escrever o padrão', async () => {
    rotearApi(() =>
      Promise.resolve(jsonResponse(200, prompt({ fromMonth: '2026-09', toMonth: '2026-09' }))),
    )
    const user = userEvent.setup()
    const router = renderIa()

    await screen.findByRole('button', { name: 'Copiar o prompt' })
    await user.selectOptions(screen.getByLabelText('Período'), '1')

    await waitFor(() => expect(router.state.location.search).toEqual({ meses: 1 }))
    await waitFor(() => expect(chamadasDe('/ai/export-prompt')).toHaveLength(2))
    const [url] = chamadasDe('/ai/export-prompt')[1] as [string]
    expect(url).toContain('fromMonth=2026-09')
    expect(url).toContain('toMonth=2026-09')

    // Voltar ao padrão APAGA a chave: a URL canônica não escreve o padrão.
    await user.selectOptions(screen.getByLabelText('Período'), '3')
    await waitFor(() => expect(router.state.location.search).toEqual({}))
  })

  it('respeita a janela que veio na URL, e valor inválido cai no padrão', async () => {
    rotearApi(() =>
      Promise.resolve(jsonResponse(200, prompt({ fromMonth: '2026-08', toMonth: '2026-09' }))),
    )
    renderIa('/ia?meses=2')

    const periodo = await screen.findByLabelText('Período')
    expect(periodo).toHaveValue('2')

    await waitFor(() => expect(chamadasDe('/ai/export-prompt')).toHaveLength(1))
    expect(String(chamadasDe('/ai/export-prompt')[0]?.[0])).toContain('fromMonth=2026-08')
  })

  it('janela maior que 3 na URL não vira tela de erro — some e cai no padrão', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    renderIa('/ia?meses=9')

    expect(await screen.findByLabelText('Período')).toHaveValue('3')
    await waitFor(() => expect(chamadasDe('/ai/export-prompt')).toHaveLength(1))
    expect(String(chamadasDe('/ai/export-prompt')[0]?.[0])).toContain('fromMonth=2026-07')
  })

  /** As três seções são reais (E9a, E9b e E9c): nenhuma diz "Ainda não está
   *  no ar", e cada uma tem o seu controle — o campo de colar na 2, o
   *  `Conferir` na 3. A seção 3 nasce ociosa: nenhuma chamada de
   *  reprocessamento sai ao abrir a tela. */
  it('as três seções são reais; a 3 nasce ociosa e não pede nada ao servidor', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    renderIa()

    await screen.findByRole('heading', { level: 2, name: '1 · Exportar o prompt' })
    const importar = screen.getByRole('heading', {
      level: 2,
      name: '2 · Importar o que a IA respondeu',
    })
    const reprocessar = screen.getByRole('heading', { level: 2, name: '3 · Reprocessar' })
    expect(document.body).not.toHaveTextContent('Ainda não está no ar.')

    const secaoImportar = importar.closest('section') as HTMLElement
    expect(
      within(secaoImportar).getByLabelText('Cole aqui o JSON que a IA respondeu'),
    ).toBeInTheDocument()

    const secao = reprocessar.closest('section') as HTMLElement
    expect(within(secao).getByRole('button', { name: 'Conferir' })).toBeInTheDocument()
    expect(secao).toHaveTextContent('Julho, agosto e setembro.')
    expect(chamadasDe('/transfers/detect')).toHaveLength(0)
    expect(chamadasDe('/transactions/auto-categorize')).toHaveLength(0)
  })

  /** Regra de frescor, vista da CASCA: trocar a janela no seletor do topo
   *  descarta a prévia da seção 2 e publica o aviso — o impacto foi medido
   *  no período anterior. O JSON colado fica. */
  it('trocar a janela no topo descarta a prévia da seção 2 e avisa, mantendo o JSON', async () => {
    fetchMock.mockImplementation((entrada: string) => {
      const url = new URL(String(entrada), 'https://app.invalido')
      if (url.pathname.endsWith('/ai/export-prompt')) {
        return Promise.resolve(jsonResponse(200, prompt()))
      }
      if (url.pathname.endsWith('/ai/keyword-import/preview')) {
        return Promise.resolve(
          jsonResponse(200, {
            totals: {
              categoriesCreated: 0,
              added: 1,
              skipped: 0,
              rejected: 0,
              periodTransactions: 212,
            },
            newCategories: [],
            items: [
              {
                type: 'account',
                id: '018f0000-0000-7000-8000-00000000c001',
                name: 'Nubank',
                added: ['nu pagamentos'],
                skipped: [],
                rejected: [],
                impact: {
                  transferCandidates: 4,
                  byKeyword: [{ keyword: 'nu pagamentos', transferCandidates: 4 }],
                },
              },
            ],
          }),
        )
      }
      if (url.pathname.endsWith('/categories')) {
        return Promise.resolve(
          jsonResponse(200, { expense: [], income: [], investment: [], redemption: [] }),
        )
      }
      if (url.pathname.endsWith('/accounts')) {
        return Promise.resolve(jsonResponse(200, { items: [] }))
      }
      return Promise.resolve(jsonResponse(200, SESSAO))
    })
    const user = userEvent.setup()
    renderIa()

    const campo = await screen.findByLabelText('Cole aqui o JSON que a IA respondeu')
    await user.click(campo)
    await user.paste('{"homefinanceKeywordImport": 1}')
    // As seções 2 e 3 têm um "Conferir" cada: o da seção 2 é o do campo.
    const secaoImportar = campo.closest('section') as HTMLElement
    await user.click(within(secaoImportar).getByRole('button', { name: 'Conferir' }))
    await screen.findByRole('button', { name: 'Gravar 1 palavra' })
    const nomeDaTabela = 'Palavras-chave de conta e o impacto no período'
    expect(screen.getByRole('table', { name: nomeDaTabela })).toHaveTextContent('4 de 212')

    await user.selectOptions(screen.getByLabelText('Período'), '1')

    expect(
      await screen.findByText(
        'A janela mudou — confira de novo para medir o impacto no novo período.',
      ),
    ).toHaveAttribute('role', 'status')
    expect(screen.queryByRole('table', { name: nomeDaTabela })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Gravar 1 palavra' })).not.toBeInTheDocument()
    expect(campo).toHaveValue('{"homefinanceKeywordImport": 1}')
  })

  /** Aceite 49, apertado: o `<pre>` mostra o texto do servidor **byte a byte**,
   *  e é esse mesmo objeto de texto que vai para o clipboard e para o `.md`.
   *
   *  `toHaveTextContent` normaliza espaço em branco, então para um texto em que
   *  as quebras de linha SÃO o conteúdo — um Markdown com cerca de código — ele
   *  passaria mesmo se a tela estivesse mostrando outra coisa. A comparação é
   *  com `textContent` cru. */
  it('o <pre> mostra o texto do servidor byte a byte', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    renderIa()

    const caixa = await screen.findByLabelText('Texto do prompt')
    expect(caixa.textContent).toBe(TEXTO)
  })

  /** Aceite 47: a janela é **uma**. Afirmar que "existe um seletor no topo" não
   *  fecha o critério — o defeito que ele previne é uma das três seções ganhar
   *  o seu próprio período e passar a contradizer o do alto da página. */
  it('existe um único seletor de período na tela inteira', async () => {
    rotearApi(() => Promise.resolve(jsonResponse(200, prompt())))
    renderIa()

    await screen.findByRole('button', { name: 'Copiar o prompt' })
    expect(screen.getAllByRole('combobox')).toHaveLength(1)
    expect(screen.getByRole('combobox')).toBe(screen.getByLabelText('Período'))
  })

  /** O eco é conferido (ADR-030): a tela AFIRMA a janela em texto visível e no
   *  nome do arquivo, então uma resposta que fala de OUTRO período não pode ser
   *  exibida — um `.md` chamado `…2026-07-a-2026-09.md` com o texto de outro
   *  trimestre é exatamente o defeito que o eco existe para pegar. */
  it('resposta com eco de outra janela vira erro, não texto na tela', async () => {
    rotearApi(() =>
      Promise.resolve(jsonResponse(200, prompt({ fromMonth: '2025-01', toMonth: '2025-03' }))),
    )
    renderIa()

    expect(await screen.findByText('Não foi possível montar o prompt.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Copiar o prompt' })).not.toBeInTheDocument()
  })
})

function chamadasDe(caminho: string): unknown[][] {
  return fetchMock.mock.calls.filter((chamada) => String(chamada[0]).includes(caminho))
}
