import styles from './Skeleton.module.css'

type SkeletonProps = {
  /** Medidas em unidades CSS — o Skeleton só existe no lugar de conteúdo real. */
  width: string
  height: string
}

/** `aria-hidden` de propósito: quem anuncia o carregamento é o `aria-busy` da região. */
export function Skeleton({ width, height }: SkeletonProps) {
  return (
    <span
      className={styles.skeleton}
      style={{ inlineSize: width, blockSize: height }}
      aria-hidden="true"
    />
  )
}
