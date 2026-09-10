import styles from './Logo.module.css'

type LogoProps = {
  /** `mark` mostra só o símbolo; `full` acrescenta o wordmark. */
  variant?: 'full' | 'mark' | undefined
  size?: 'sm' | 'md' | 'lg' | undefined
  /** Sem `title` o conjunto é decorativo — o link ao redor carrega o nome acessível. */
  title?: string | undefined
}

/** Casa pautada: telhado, corpo e duas linhas de livro-caixa (a segunda é o total). */
export function Logo({ variant = 'full', size = 'md', title }: LogoProps) {
  return (
    <span className={styles.logo} data-size={size}>
      {/* biome-ignore lint/a11y/noSvgWithoutTitle: sem `title` o icone e decorativo e sai com aria-hidden; o contrato esta em icons/types.ts */}
      <svg
        xmlns="http://www.w3.org/2000/svg"
        className={styles.mark}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        role={title ? 'img' : undefined}
        aria-hidden={title ? undefined : true}
        focusable="false"
      >
        {title ? <title>{title}</title> : null}
        <path d="M3.5 10.75 11.25 4.4a1.2 1.2 0 0 1 1.5 0l7.75 6.35" />
        <path d="M5.75 12.6v6.15a.85.85 0 0 0 .85.85h10.8a.85.85 0 0 0 .85-.85V12.6" />
        <path d="M8.75 15.4h6.5" />
        <path d="M8.75 18h4" />
      </svg>
      {variant === 'full' ? <span className={styles.word}>HomeFinance</span> : null}
    </span>
  )
}
