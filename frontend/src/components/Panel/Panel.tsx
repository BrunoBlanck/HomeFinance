import { type ReactNode, useId } from 'react'
import styles from './Panel.module.css'

type PanelProps = {
  as?: 'section' | 'article' | 'div' | undefined
  title?: string | undefined
  /** Id externo do título, quando outro elemento já o rotula. */
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
      aria-labelledby={as === 'section' && title ? headingId : undefined}
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
