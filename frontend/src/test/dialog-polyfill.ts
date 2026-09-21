/** Polyfill mínimo do `<dialog>` para o jsdom.
 *
 *  **Por que existe:** o jsdom 30 não implementa `HTMLDialogElement` —
 *  verificado em 12/09/2026: `showModal` é `undefined` e o elemento não recebe
 *  o `display: none` da folha de estilo do agente quando fechado. Sem isto,
 *  abrir um diálogo lança "dialog.showModal is not a function", e o conteúdo do
 *  diálogo FECHADO ficaria visível para o Testing Library — que é pior, porque
 *  faria passar um teste que deveria falhar.
 *
 *  **Por que polyfill e não trocar o elemento:** `docs/DESIGN.md` manda usar o
 *  nativo antes de reimplementar padrão APG, e é o `<dialog>` modal que entrega
 *  de graça a armadilha de foco, o `inert` no resto da página, o ESC e a top
 *  layer. Reescrever isso à mão para o jsdom enxergar seria deixar a ferramenta
 *  ditar a arquitetura — e reimplementações desse padrão erram o foco com
 *  frequência.
 *
 *  **Alcance, dito com honestidade:** reproduz o observável (abrir, fechar,
 *  ESC, evento `close`, ocultar quando fechado). NÃO reproduz a armadilha de
 *  foco, o `inert` no restante da página nem a top layer. Essas três se
 *  verificam no Playwright, em navegador de verdade. */

const ABERTOS = new WeakSet<HTMLElement>()

type Dialogish = HTMLElement & {
  showModal?: () => void
  show?: () => void
  close?: (returnValue?: string) => void
}

function ehDialog(element: Element): element is HTMLElement {
  return element.tagName === 'DIALOG'
}

/** O navegador esconde `dialog:not([open])` pela folha do agente. Aqui o
 *  `display` inline faz esse papel — e é o que impede um teste de "encontrar"
 *  o conteúdo de um diálogo que nunca foi aberto. */
function aplicarVisibilidade(element: HTMLElement, aberto: boolean): void {
  element.style.display = aberto ? 'block' : 'none'
}

function abrir(element: HTMLElement, modal: boolean): void {
  if (ABERTOS.has(element)) return
  ABERTOS.add(element)
  element.setAttribute('open', '')
  if (modal) element.setAttribute('data-modal', 'true')
  aplicarVisibilidade(element, true)
}

function fechar(element: HTMLElement, returnValue?: string): void {
  if (!ABERTOS.has(element)) return
  ABERTOS.delete(element)
  element.removeAttribute('open')
  element.removeAttribute('data-modal')
  aplicarVisibilidade(element, false)
  if (returnValue !== undefined) {
    ;(element as HTMLElement & { returnValue?: string }).returnValue = returnValue
  }
  element.dispatchEvent(new Event('close'))
}

export function instalarDialogPolyfill(): void {
  if (typeof HTMLElement === 'undefined') return

  const globalComDialog = globalThis as { HTMLDialogElement?: { prototype: Dialogish } }
  const proto: Dialogish = globalComDialog.HTMLDialogElement
    ? globalComDialog.HTMLDialogElement.prototype
    : (HTMLElement.prototype as Dialogish)

  if (typeof proto.showModal === 'function') return // navegador de verdade

  proto.showModal = function showModal(this: HTMLElement) {
    if (ehDialog(this)) abrir(this, true)
  }
  proto.show = function show(this: HTMLElement) {
    if (ehDialog(this)) abrir(this, false)
  }
  proto.close = function close(this: HTMLElement, returnValue?: string) {
    if (ehDialog(this)) fechar(this, returnValue)
  }

  // `open` como propriedade IDL, refletindo o atributo — é assim que o React e
  // o nosso código consultam o estado.
  if (!Object.getOwnPropertyDescriptor(proto, 'open')) {
    Object.defineProperty(proto, 'open', {
      configurable: true,
      get(this: HTMLElement) {
        return this.hasAttribute('open')
      },
      set(this: HTMLElement, valor: boolean) {
        if (valor) abrir(this, false)
        else fechar(this)
      },
    })
  }

  // ESC fecha o diálogo modal mais recente. No navegador isso vem de graça;
  // aqui é o que permite testar a saída por teclado.
  document.addEventListener('keydown', (evento) => {
    if (evento.key !== 'Escape') return
    const modais = Array.from(document.querySelectorAll<HTMLElement>('dialog[open]'))
    const ultimo = modais[modais.length - 1]
    if (!ultimo) return
    // O navegador emite `cancel` antes de `close`; quem quiser barrar a saída
    // chama preventDefault ali.
    const cancelavel = new Event('cancel', { cancelable: true })
    if (ultimo.dispatchEvent(cancelavel)) fechar(ultimo)
  })
}
