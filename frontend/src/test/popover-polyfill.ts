/** Polyfill mínimo da Popover API para o jsdom.
 *
 *  **Por que existe:** o jsdom 30 não implementa nada da Popover API —
 *  verificado em 12/09/2026: `showPopover` é `undefined`, a propriedade IDL
 *  `popover` não existe e `:popover-open` nunca casa. Consequência prática: um
 *  painel escrito com `popover="auto"` fica permanentemente escondido no teste,
 *  porque a regra `.panel:not(:popover-open) { display: none }` passa a valer
 *  sempre — e aí nem o Testing Library nem o leitor de tela simulado enxergam
 *  o conteúdo.
 *
 *  **Por que polyfill e não mudar o componente:** `docs/DESIGN.md` manda usar o
 *  elemento nativo antes de reimplementar padrão APG, e a Popover API é
 *  exatamente isso — ela dá light dismiss, ESC e devolução de foco de graça no
 *  navegador real. Trocar por um menu de mentira só para o jsdom enxergar seria
 *  deixar o teste ditar a arquitetura. O buraco é da ferramenta, então o
 *  remendo fica na ferramenta.
 *
 *  **Alcance, dito com honestidade:** isto reproduz o comportamento observável
 *  (abrir, fechar, alternar, evento `toggle`, ESC, clique fora), o suficiente
 *  para testar o que a interface faz. NÃO reproduz a top layer, a ordem de
 *  empilhamento nem o gerenciamento de foco do navegador. Essas três coisas se
 *  verificam no Playwright, em navegador de verdade — ver
 *  `e2e/` e a Fase 6 do ROADMAP. */

type Popoverish = HTMLElement & {
  showPopover?: () => void
  hidePopover?: () => void
  togglePopover?: (force?: boolean) => boolean
}

const OPEN = new WeakSet<HTMLElement>()

const SELETOR_ABERTO = ':popover-open'

function ehPopover(element: HTMLElement): boolean {
  const tipo = element.getAttribute('popover')
  return tipo !== null && tipo !== 'manual-hidden'
}

function ehAuto(element: HTMLElement): boolean {
  const tipo = (element.getAttribute('popover') ?? '').toLowerCase()
  return tipo === '' || tipo === 'auto'
}

/** O navegador esconde popover fechado pela folha de estilo do agente. Aqui o
 *  `display` inline faz esse papel: ele vence a regra de classe do CSS Module,
 *  que é o que mantém `.panel:not(:popover-open)` funcionando no jsdom. */
function aplicarVisibilidade(element: HTMLElement, aberto: boolean): void {
  element.style.display = aberto ? 'block' : 'none'
}

function abrir(element: HTMLElement): void {
  if (!ehPopover(element) || OPEN.has(element)) return
  if (ehAuto(element)) {
    // `auto` fecha os irmãos: dois menus abertos ao mesmo tempo não existem.
    for (const outro of document.querySelectorAll<HTMLElement>('[popover]')) {
      if (outro !== element && OPEN.has(outro) && ehAuto(outro)) fechar(outro)
    }
  }
  OPEN.add(element)
  aplicarVisibilidade(element, true)
  element.dispatchEvent(new Event('toggle', { bubbles: false }))
}

function fechar(element: HTMLElement): void {
  if (!OPEN.has(element)) return
  OPEN.delete(element)
  aplicarVisibilidade(element, false)
  element.dispatchEvent(new Event('toggle', { bubbles: false }))
}

export function instalarPopoverPolyfill(): void {
  if (typeof HTMLElement === 'undefined') return
  const proto = HTMLElement.prototype as Popoverish
  if (typeof proto.showPopover === 'function') return // navegador de verdade

  proto.showPopover = function showPopover(this: HTMLElement) {
    abrir(this)
  }
  proto.hidePopover = function hidePopover(this: HTMLElement) {
    fechar(this)
  }
  proto.togglePopover = function togglePopover(this: HTMLElement, force?: boolean) {
    const alvo = force ?? !OPEN.has(this)
    if (alvo) abrir(this)
    else fechar(this)
    return OPEN.has(this)
  }

  Object.defineProperty(HTMLElement.prototype, 'popover', {
    configurable: true,
    get(this: HTMLElement) {
      return this.getAttribute('popover')
    },
    set(this: HTMLElement, valor: string | null) {
      if (valor === null) this.removeAttribute('popover')
      else this.setAttribute('popover', valor)
    },
  })

  // `matches(':popover-open')` — o componente pergunta isso no evento `toggle`.
  // O nwsapi do jsdom não conhece a pseudoclasse, então respondemos por ela e
  // delegamos todo o resto.
  const matchesOriginal = Element.prototype.matches
  // O `as` é inevitável: a assinatura real de `matches` tem sobrecargas que são
  // type predicates (`selectors: K): this is HTMLElementTagNameMap[K]`), e uma
  // função que devolve `boolean` não é atribuível a elas. O comportamento é o
  // mesmo — só o compilador precisa ser convencido.
  Element.prototype.matches = function matches(this: Element, selectors: string): boolean {
    if (selectors.trim() === SELETOR_ABERTO) {
      return this instanceof HTMLElement && OPEN.has(this)
    }
    return matchesOriginal.call(this, selectors)
  } as typeof Element.prototype.matches

  // Invocador: <button popovertarget="id"> abre, fecha ou alterna.
  document.addEventListener(
    'click',
    (evento) => {
      const alvoEvento = evento.target
      if (!(alvoEvento instanceof Element)) return
      const gatilho = alvoEvento.closest<HTMLElement>('[popovertarget]')
      if (gatilho) {
        const id = gatilho.getAttribute('popovertarget')
        const painel = id ? document.getElementById(id) : null
        if (painel) {
          const acao = (gatilho.getAttribute('popovertargetaction') ?? 'toggle').toLowerCase()
          if (acao === 'show') abrir(painel)
          else if (acao === 'hide') fechar(painel)
          else if (OPEN.has(painel)) fechar(painel)
          else abrir(painel)
        }
        return
      }

      // Light dismiss: clique fora fecha os `auto` abertos.
      for (const painel of document.querySelectorAll<HTMLElement>('[popover]')) {
        if (OPEN.has(painel) && ehAuto(painel) && !painel.contains(alvoEvento)) fechar(painel)
      }
    },
    true,
  )

  document.addEventListener('keydown', (evento) => {
    if (evento.key !== 'Escape') return
    for (const painel of document.querySelectorAll<HTMLElement>('[popover]')) {
      if (OPEN.has(painel) && ehAuto(painel)) fechar(painel)
    }
  })
}
