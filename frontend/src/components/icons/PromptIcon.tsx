import type { IconProps } from './types'

/** IA — **um bloco de texto entre dois colchetes**.
 *
 *  O desenho é o modelo mental da feature inteira: o app entrega TEXTO e recebe
 *  TEXTO, e a viagem até a IA acontece fora do aplicativo, pela mão da pessoa.
 *  Os dois colchetes são espelhados de propósito — eles são a fronteira do app
 *  nos dois lados da viagem —, as três linhas formam um parágrafo (a última
 *  curta, como parágrafo de verdade) e nada atravessa os colchetes.
 *
 *  Recusados, e o motivo importa porque é o que separa este conjunto do clip-art
 *  genérico: robô, varinha mágica, faíscas/sparkles, cérebro e balão de fala
 *  (clichê de "IA", e nenhum deles diz o que a tela faz); duas setas opostas
 *  (colidiria com o `TransfersIcon`); engrenagem e raio (dizem "automático", e
 *  aqui nada é automático); `<>` e `{}` (dizem "código" a quem só vai copiar
 *  texto). */
export function PromptIcon({ size = 20, title }: IconProps) {
  return (
    // biome-ignore lint/a11y/noSvgWithoutTitle: sem `title` o icone e decorativo e sai com aria-hidden; o contrato esta em icons/types.ts
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
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
      <path d="M9.25 4.75H5.75a1 1 0 0 0-1 1v12.5a1 1 0 0 0 1 1h3.5" />
      <path d="M14.75 4.75h3.5a1 1 0 0 1 1 1v12.5a1 1 0 0 1-1 1h-3.5" />
      <path d="M8.75 9.25h6.5" />
      <path d="M8.75 12.25h6.5" />
      <path d="M8.75 15.25h3.75" />
    </svg>
  )
}
