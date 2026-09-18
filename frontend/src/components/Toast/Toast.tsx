import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { AlertIcon } from '../icons/AlertIcon'
import { CheckIcon } from '../icons/CheckIcon'
import styles from './Toast.module.css'

export type ToastTone = 'success' | 'error'

type Toast = {
  id: number
  tone: ToastTone
  message: string
}

type ToastAPI = {
  /** Confirma uma ação concluída. Tom de SISTEMA, não de dinheiro. */
  sucesso: (message: string) => void
  erro: (message: string) => void
}

const ToastContext = createContext<ToastAPI | null>(null)

/** Quanto tempo o aviso fica. Oito segundos, não três: a WCAG 2.2.1 pede tempo
 *  suficiente para ler, e quem usa leitor de tela precisa ouvir a frase inteira
 *  antes de ela sumir. */
const DURACAO_MS = 8000

/** Avisos efêmeros de confirmação.
 *
 *  Duas decisões que valem explicar:
 *
 *  1. **Nunca é o único lugar onde a informação aparece.** Toast some, e quem
 *     não estava olhando perde. Ele confirma o que a tela já mostra (a linha
 *     nova apareceu na tabela) — não substitui a mudança visível.
 *  2. **Erro que pede ação NÃO vem por aqui.** Um 422 "categoria em uso" vira
 *     texto no diálogo, junto do botão de arquivar. Toast é para o que já
 *     aconteceu e não precisa de resposta. */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const proximoId = useRef(0)

  const remover = useCallback((id: number) => {
    setToasts((atuais) => atuais.filter((t) => t.id !== id))
  }, [])

  const empilhar = useCallback((tone: ToastTone, message: string) => {
    proximoId.current += 1
    const id = proximoId.current
    // Um por vez: pilha de avisos numa live region vira tagarelice.
    setToasts([{ id, tone, message }])
  }, [])

  const api = useMemo<ToastAPI>(
    () => ({
      sucesso: (message: string) => empilhar('success', message),
      erro: (message: string) => empilhar('error', message),
    }),
    [empilhar],
  )

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className={styles.regiao}>
        {/* `polite` e não `assertive`: o aviso confirma algo que a pessoa
            acabou de fazer, então pode esperar a leitura em curso terminar. */}
        <div aria-live="polite" aria-atomic="true" className={styles.live}>
          {toasts.map((toast) => (
            <ToastItem key={toast.id} toast={toast} onExpire={() => remover(toast.id)} />
          ))}
        </div>
      </div>
    </ToastContext.Provider>
  )
}

function ToastItem({ toast, onExpire }: { toast: Toast; onExpire: () => void }) {
  useEffect(() => {
    const id = window.setTimeout(onExpire, DURACAO_MS)
    return () => window.clearTimeout(id)
  }, [onExpire])

  return (
    <div className={styles.toast} data-tone={toast.tone}>
      <span className={styles.icone} aria-hidden="true">
        {toast.tone === 'success' ? <CheckIcon size={18} /> : <AlertIcon size={18} />}
      </span>
      <p className={styles.texto}>{toast.message}</p>
    </div>
  )
}

/** Acesso aos avisos. Fora do provider devolve um objeto silencioso em vez de
 *  quebrar: um aviso que não aparece é um defeito pequeno; uma tela que não
 *  renderiza é um grande. */
export function useToast(): ToastAPI {
  const contexto = useContext(ToastContext)
  return contexto ?? SILENCIOSO
}

const SILENCIOSO: ToastAPI = { sucesso: () => {}, erro: () => {} }
