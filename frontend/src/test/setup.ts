import '@testing-library/jest-dom/vitest'
import { instalarDialogPolyfill } from './dialog-polyfill'
import { instalarPopoverPolyfill } from './popover-polyfill'

// jsdom não implementa scrollTo; o roteador chama na restauração de scroll.
window.scrollTo = () => {}

// jsdom também não implementa a Popover API nem o <dialog>. O design system
// manda usar esses dois elementos nativos (docs/DESIGN.md: nativo antes de
// reimplementar padrão APG), então o remendo fica na ferramenta, não na
// arquitetura. O alcance e o limite de cada polyfill estão no cabeçalho deles.
instalarPopoverPolyfill()
instalarDialogPolyfill()
