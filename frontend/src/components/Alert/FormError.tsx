import type { ReactNode } from 'react'
import { Alert } from './Alert'

type FormErrorProps = {
  /** O `<form>` aponta para este id em `aria-describedby`. */
  id: string
  title?: string | undefined
  /** Desligue quando a tela tiver um alvo de foco melhor (o campo do código).
   *  O `role="alert"` continua anunciando a mensagem sem mover o foco. */
  autoFocus?: boolean | undefined
  children: ReactNode
}

/** Erro de formulário vindo do servidor. Vive acima dos campos, recebe o foco e
 *  é o único lugar que anuncia a falha — os campos só marcam `aria-invalid`. */
export function FormError({ id, title, autoFocus = true, children }: FormErrorProps) {
  return (
    <Alert tone="error" autoFocus={autoFocus} id={id} title={title}>
      {children}
    </Alert>
  )
}
