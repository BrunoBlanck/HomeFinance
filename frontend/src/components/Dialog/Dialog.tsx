import { type ReactNode, useEffect, useId, useRef } from 'react'
import { Button } from '../Button/Button'
import { CloseIcon } from '../icons/CloseIcon'
import styles from './Dialog.module.css'

type DialogProps = {
  open: boolean
  onClose: () => void
  title: string
  description?: string | undefined
  /** Rodapé — normalmente as ações. Fica fora do `children` para que o corpo
   *  possa rolar sem levar os botões junto. */
  footer?: ReactNode | undefined
  children: ReactNode
}

/** Diálogo modal sobre o `<dialog>` NATIVO.
 *
 *  Escolha do design system (elementos nativos primeiro): `showModal()` já
 *  entrega, sem uma linha de JavaScript nossa, a armadilha de foco, o `inert`
 *  no resto da página, o fechamento por ESC e a top layer — quatro coisas que
 *  uma reimplementação erra com frequência e que ninguém nota até um usuário de
 *  teclado ficar preso.
 *
 *  O que continua sendo nosso: rotular (`aria-labelledby`), fechar no clique no
 *  backdrop, e devolver o foco ao elemento que abriu. */
export function Dialog({ open, onClose, title, description, footer, children }: DialogProps) {
  const ref = useRef<HTMLDialogElement>(null)
  const abridorRef = useRef<Element | null>(null)
  const rawId = useId()
  const tituloId = `dialog-title-${rawId.replace(/[^a-zA-Z0-9]/g, '')}`
  const descricaoId = `dialog-desc-${rawId.replace(/[^a-zA-Z0-9]/g, '')}`

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return

    if (open) {
      // Guarda quem abriu ANTES de mover o foco: é para lá que ele volta.
      abridorRef.current = document.activeElement
      if (!dialog.open) dialog.showModal()
      return
    }

    if (dialog.open) dialog.close()
  }, [open])

  // O ESC do navegador dispara `cancel`/`close` sem passar pelo nosso onClose;
  // sem escutar, o React continuaria achando que o diálogo está aberto.
  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return

    function handleClose() {
      const abridor = abridorRef.current
      if (abridor instanceof HTMLElement) abridor.focus()
      onClose()
    }
    dialog.addEventListener('close', handleClose)
    return () => dialog.removeEventListener('close', handleClose)
  }, [onClose])

  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: o equivalente de teclado existe e e NATIVO — o <dialog> modal fecha com ESC sem uma linha nossa. Um onKeyDown aqui so criaria um segundo caminho para manter, e nenhum leitor de tela chega a este onClick: ele so trata o clique no backdrop, que nao existe para teclado.
    <dialog
      ref={ref}
      className={styles.dialog}
      aria-labelledby={tituloId}
      aria-describedby={description ? descricaoId : undefined}
      onClick={(event) => {
        // Clique no backdrop fecha. O alvo é o próprio <dialog> apenas quando
        // o clique caiu na área de fundo — dentro do conteúdo, o alvo é outro.
        if (event.target === ref.current) ref.current?.close()
      }}
    >
      <div className={styles.painel}>
        <header className={styles.head}>
          <div className={styles.heading}>
            <h2 className={styles.title} id={tituloId}>
              {title}
            </h2>
            {description ? (
              <p className={styles.description} id={descricaoId}>
                {description}
              </p>
            ) : null}
          </div>
          <Button
            variant="quiet"
            size="sm"
            onClick={() => ref.current?.close()}
            aria-label="Fechar"
            iconStart={<CloseIcon size={18} />}
          >
            {''}
          </Button>
        </header>

        <div className={styles.body}>{children}</div>

        {footer ? <footer className={styles.footer}>{footer}</footer> : null}
      </div>
    </dialog>
  )
}
