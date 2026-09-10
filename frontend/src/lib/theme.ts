/** Preferência de aparência. É preferência de interface, não credencial — por
 *  isso pode viver em `localStorage` (docs/SEGURANCA.md permite; token, nunca). */

export type ThemePreference = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'hf.theme'

function isPreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

export function readThemePreference(): ThemePreference {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    return isPreference(stored) ? stored : 'system'
  } catch {
    return 'system'
  }
}

/** `system` remove o atributo e devolve a decisão ao `prefers-color-scheme`. */
export function applyThemePreference(preference: ThemePreference): void {
  const root = document.documentElement
  if (preference === 'system') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', preference)
}

export function storeThemePreference(preference: ThemePreference): void {
  try {
    localStorage.setItem(STORAGE_KEY, preference)
  } catch {
    // Modo privado sem storage: a escolha vale só para esta sessão.
  }
}
