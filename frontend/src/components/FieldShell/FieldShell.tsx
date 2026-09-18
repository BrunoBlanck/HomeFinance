import type { ReactNode } from 'react'
import { AlertIcon } from '../icons/AlertIcon'
import styles from './FieldShell.module.css'

/** `form` é o campo de formulário: rótulo visível, altura cheia e slot de
 *  mensagem reservado mesmo vazio, para validar não empurrar o resto da tela.
 *
 *  `compact` é o campo que mora dentro de outra coisa — célula de tabela, barra
 *  de filtro. Ali o slot reservado e o rótulo pintado não cabem: cada célula de
 *  decisão ficaria 40px mais alta e a faixa de filtro passaria de 82px para
 *  114px. O campo encolhe, mas não perde nada que a acessibilidade precise: o
 *  `<label>` continua existindo e associado, e a mensagem de erro continua no
 *  `aria-describedby` — ela só deixa de reservar espaço quando não há erro. */
export type FieldDensity = 'form' | 'compact'

type FieldShellProps = {
  id: string
  messageId: string
  /** Obrigatório, e é assim de propósito: com `labelHidden` o rótulo deixa de
   *  ser pintado, mas o `<label for>` continua no DOM e continua associado ao
   *  controle. Não existe caminho neste componente que produza um controle sem
   *  nome acessível — o tipo não deixa. */
  label: string
  /** Ação no canto do rótulo — ex.: "Esqueci minha senha". */
  labelAction?: ReactNode | undefined
  hint?: string | undefined
  error?: string | undefined
  density?: FieldDensity | undefined
  /** Manda o rótulo para `.sr-only`. Use quando o contexto ao redor já diz o
   *  que o campo é — o cabeçalho da coluna, a linha da tabela — e repetir o
   *  rótulo em cada célula seria ruído visual. */
  labelHidden?: boolean | undefined
  children: ReactNode
  /** Conteúdo que vem DEPOIS da mensagem — hoje só a linha de requisito de senha. */
  extra?: ReactNode
}

/** Esqueleto interno de campo. Não é exportado como UI: quem monta campo é o
 *  TextField, o PasswordField ou o CodeInput.
 *
 *  O slot de mensagem existe sempre, com altura reservada — validar não pode
 *  empurrar o resto do formulário para baixo. E mostra hint OU erro, nunca os
 *  dois: dois textos concorrentes sob o mesmo campo é ruído. */
export function FieldShell({
  id,
  messageId,
  label,
  labelAction,
  hint,
  error,
  density = 'form',
  labelHidden = false,
  children,
  extra,
}: FieldShellProps) {
  const rotulo = (
    <label className={labelHidden ? 'sr-only' : styles.label} htmlFor={id}>
      {label}
    </label>
  )

  return (
    <div className={styles.field} data-density={density}>
      {/* Sem ação no canto e com o rótulo escondido, a linha do rótulo não tem
          o que fazer: ela some junto, senão sobraria a margem dela empurrando o
          controle para baixo. */}
      {labelHidden && !labelAction ? (
        rotulo
      ) : (
        <div className={styles.labelRow}>
          {rotulo}
          {labelAction}
        </div>
      )}
      {children}
      {/* Sem role="alert": a validação acontece no submit e no blur, e um live
          region aqui tagarelaria a cada tecla. O anúncio é do FormError. */}
      <p className={styles.message} id={messageId} data-tone={error ? 'error' : 'hint'}>
        {error ? (
          <>
            <AlertIcon size={14} />
            <span>{error}</span>
          </>
        ) : (
          hint
        )}
      </p>
      {extra}
    </div>
  )
}
