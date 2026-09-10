import '@testing-library/jest-dom/vitest'

// jsdom não implementa scrollTo; o roteador chama na restauração de scroll.
window.scrollTo = () => {}
