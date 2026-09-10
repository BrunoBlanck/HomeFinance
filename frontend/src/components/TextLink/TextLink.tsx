import { createLink } from '@tanstack/react-router'
import type { ComponentPropsWithoutRef, Ref } from 'react'
import styles from './TextLink.module.css'

type BaseAnchorProps = Omit<ComponentPropsWithoutRef<'a'>, 'className' | 'style'> & {
  ref?: Ref<HTMLAnchorElement>
}

function BaseTextLink(props: BaseAnchorProps) {
  return <a {...props} className={styles.link} />
}

/** Navegar é trabalho de link. Botão que navega quebra o menu de contexto,
 *  o "abrir em nova aba" e o anúncio do leitor de tela. */
export const TextLink = createLink(BaseTextLink)
