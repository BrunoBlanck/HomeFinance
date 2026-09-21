import { type ReactNode, useId } from 'react'
import styles from './Panel.module.css'

type PanelProps = {
  as?: 'section' | 'article' | 'div' | undefined
  title?: string | undefined
  /** Id do título, quando ele NÃO é o `title` deste componente.
   *
   *  Duas leituras, e as duas valem: com `title`, ele apenas fixa o id do `<h2>`
   *  gerado; **sem** `title`, ele aponta para um cabeçalho que o próprio
   *  conteúdo renderiza, e é assim que uma `<section>` de `padding="none"`
   *  ganha nome acessível. Esse caso existe porque `padding="none"` zera
   *  `--panel-pad` — o cabeçalho do `Panel` ficaria colado na borda —, e é o que
   *  a tela `/ia` precisa: o `<h2>` numerado mora no conteúdo, com o recuo
   *  dele, e a `<section>` continua sendo uma região com nome. */
  titleId?: string | undefined
  subtitle?: string | undefined
  actions?: ReactNode | undefined
  footer?: ReactNode | undefined
  tone?: 'default' | 'sunken' | undefined
  padding?: 'none' | 'md' | 'lg' | undefined
  children: ReactNode
}

/** Painel do HomeFinance. `box-shadow: none` é regra dura: superfícies se
 *  separam por borda de 1px. Sombra é privilégio de camada flutuante. */
export function Panel({
  as = 'div',
  title,
  titleId,
  subtitle,
  actions,
  footer,
  tone = 'default',
  padding = 'md',
  children,
}: PanelProps) {
  const generatedId = useId()
  const headingId = titleId ?? `${generatedId}-title`
  const Tag = as

  return (
    <Tag
      className={styles.panel}
      data-tone={tone}
      data-padding={padding}
      aria-labelledby={as === 'section' && (title || titleId) ? headingId : undefined}
    >
      {title || actions ? (
        <div className={styles.head}>
          <div className={styles.heading}>
            {title ? (
              <h2 className={styles.title} id={headingId}>
                {title}
              </h2>
            ) : null}
            {subtitle ? <p className={styles.subtitle}>{subtitle}</p> : null}
          </div>
          {actions ? <div className={styles.actions}>{actions}</div> : null}
        </div>
      ) : null}
      <div className={styles.body}>{children}</div>
      {footer ? <div className={styles.footer}>{footer}</div> : null}
    </Tag>
  )
}
