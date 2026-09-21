import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useEffect, useId, useRef, useState } from 'react'
import { CheckIcon } from '@/components/icons/CheckIcon'
import { ChevronDownIcon } from '@/components/icons/ChevronDownIcon'
import { LogOutIcon } from '@/components/icons/LogOutIcon'
import { PromptIcon } from '@/components/icons/PromptIcon'
import { logout } from '@/lib/session'
import {
  applyThemePreference,
  readThemePreference,
  storeThemePreference,
  type ThemePreference,
} from '@/lib/theme'
import styles from './UserMenu.module.css'

const THEME_OPTIONS: ReadonlyArray<{ value: ThemePreference; label: string }> = [
  { value: 'light', label: 'Claro' },
  { value: 'dark', label: 'Escuro' },
  { value: 'system', label: 'Sistema' },
]

function initialsOf(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  const first = parts[0]?.charAt(0) ?? '?'
  const last = parts.length > 1 ? (parts[parts.length - 1]?.charAt(0) ?? '') : ''
  return (first + last).toUpperCase()
}

type UserMenuProps = {
  name: string
  email: string
  /** O mês da casca (`?mes=`). Viaja junto no link de `/ia` — abaixo de 52rem
   *  este menu é a ÚNICA porta para a tela, e uma porta que perde o mês
   *  quebraria o eixo do app (ADR-019). */
  mes: string
}

/** Painel flutuante com a Popover API nativa: light dismiss, ESC e devolução do
 *  foco ao gatilho vêm de graça.
 *
 *  A escolha de aparência é um grupo de rádios NATIVO — e não um `role="radio"`
 *  escrito à mão: com `<fieldset>` + `<input type="radio">` o navegador já dá
 *  roving tabindex, navegação por setas e seleção que segue o foco, exatamente
 *  o que o APG pede, sem uma linha de JavaScript de teclado. */
export function UserMenu({ name, email, mes }: UserMenuProps) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const rawId = useId()
  const panelId = `user-menu-${rawId.replace(/[^a-zA-Z0-9]/g, '')}`
  const panelRef = useRef<HTMLDivElement>(null)
  const radioRefs = useRef<Array<HTMLInputElement | null>>([])
  const [preference, setPreference] = useState<ThemePreference>(() => readThemePreference())

  // O painel abre pela Popover API; levar o foco para dentro continua sendo nosso.
  useEffect(() => {
    const panel = panelRef.current
    if (!panel) return
    function handleToggle(event: Event) {
      if (!(event.target instanceof HTMLElement) || !event.target.matches(':popover-open')) return
      const index = THEME_OPTIONS.findIndex((option) => option.value === preference)
      radioRefs.current[index === -1 ? 0 : index]?.focus()
    }
    panel.addEventListener('toggle', handleToggle)
    return () => panel.removeEventListener('toggle', handleToggle)
  }, [preference])

  function choose(value: ThemePreference) {
    setPreference(value)
    storeThemePreference(value)
    applyThemePreference(value)
  }

  const signOut = useMutation({
    mutationFn: logout,
    // O logout é idempotente no backend; falhando ou não, o estado local morre
    // aqui e o usuário volta para a tela de entrada.
    onSettled() {
      queryClient.clear()
      navigate({ to: '/entrar', replace: true })
    },
  })

  return (
    <div className={styles.wrap}>
      <button
        type="button"
        className={styles.trigger}
        popoverTarget={panelId}
        aria-label={`Menu de ${name}`}
      >
        <span className={styles.avatar} aria-hidden="true">
          {initialsOf(name)}
        </span>
        <ChevronDownIcon size={16} />
      </button>

      <div id={panelId} popover="auto" className={styles.panel} ref={panelRef}>
        <div className={styles.identity}>
          <strong className={styles.name}>{name}</strong>
          <span className={styles.email}>{email}</span>
        </div>

        {/* Abaixo de 52rem a barra inferior fecha em SETE células e a
            ferramenta não ocupa nenhuma (docs/DESIGN.md, spec 0010 §10.6): o
            destino mora aqui. Acima de 52rem estes dois nós saem da tela pelo
            CSS — o item continua na lateral, e duas portas visíveis para o
            mesmo lugar seriam duas marcas de "página atual". */}
        <hr className={`${styles.divider} ${styles.soNoCelular}`} />

        <Link
          to="/ia"
          search={{ mes }}
          className={`${styles.item} ${styles.soNoCelular}`}
          activeOptions={{ exact: false, includeSearch: false }}
          activeProps={{ 'aria-current': 'page' }}
          // `popover="auto"` fecha por light dismiss e por ESC, mas NÃO por
          // clique DENTRO do painel: sem esta linha o menu ficaria aberto por
          // cima da tela recém-aberta, tapando justamente o <h1> que acabou de
          // receber o foco.
          onClick={() => panelRef.current?.hidePopover()}
        >
          <span className={styles.mark} aria-hidden="true">
            <PromptIcon size={16} />
          </span>
          <span>IA</span>
        </Link>

        <hr className={styles.divider} />

        <fieldset className={styles.group}>
          <legend className={styles.groupLabel}>Aparência</legend>
          {THEME_OPTIONS.map((option, index) => (
            <label className={styles.item} key={option.value}>
              <input
                type="radio"
                className={styles.radio}
                name={`${panelId}-theme`}
                value={option.value}
                checked={preference === option.value}
                onChange={() => choose(option.value)}
                ref={(element) => {
                  radioRefs.current[index] = element
                }}
              />
              <span className={styles.mark} aria-hidden="true">
                {preference === option.value ? <CheckIcon size={16} /> : null}
              </span>
              <span>{option.label}</span>
            </label>
          ))}
        </fieldset>

        <hr className={styles.divider} />

        <button
          type="button"
          className={styles.item}
          onClick={() => signOut.mutate()}
          aria-busy={signOut.isPending || undefined}
        >
          <span className={styles.mark} aria-hidden="true">
            <LogOutIcon size={16} />
          </span>
          <span>Sair</span>
        </button>
      </div>
    </div>
  )
}
