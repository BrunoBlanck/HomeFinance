import { CheckIcon } from '@/components/icons/CheckIcon'
import styles from './ImportStepper.module.css'

export type PassoDaImportacao = 'enviar' | 'revisar' | 'resultado'

const PASSOS: readonly { chave: PassoDaImportacao; rotulo: string }[] = [
  { chave: 'enviar', rotulo: 'Enviar' },
  { chave: 'revisar', rotulo: 'Revisar' },
  { chave: 'resultado', rotulo: 'Resultado' },
]

/** Indicador de passo da importação.
 *
 *  É **status, não navegação**: uma `<ol>` sem um único elemento clicável. A
 *  tentação de transformar os passos em links é forte e está errada aqui —
 *  "voltar" no meio de uma importação não é ir para a tela anterior, é
 *  *cancelar* (e descartar o arquivo do servidor). Um link prometeria uma volta
 *  barata que não existe. Quem quer voltar usa "Cancelar importação", que diz a
 *  consequência antes.
 *
 *  `<ol>` e não `<div>`: a ordem é a informação. Um leitor de tela anuncia
 *  "lista de 3 itens, item 2", que é exatamente o que a caixinha numerada
 *  mostra para quem enxerga. */
export function ImportStepper({ atual }: { atual: PassoDaImportacao }) {
  const indiceAtual = PASSOS.findIndex((passo) => passo.chave === atual)

  return (
    <ol className={styles.passos} aria-label="Etapas da importação">
      {PASSOS.map((passo, indice) => {
        const concluido = indice < indiceAtual
        const ativo = indice === indiceAtual
        const estado = concluido ? 'concluido' : ativo ? 'ativo' : 'futuro'

        return (
          <li
            key={passo.chave}
            className={styles.passo}
            data-estado={estado}
            {...(ativo ? { 'aria-current': 'step' as const } : {})}
          >
            <span className={styles.marca} aria-hidden="true">
              {concluido ? <CheckIcon size={14} /> : indice + 1}
            </span>
            <span className={styles.rotulo}>{passo.rotulo}</span>
            {/* O estado do passo é dado por cor e por ícone para quem enxerga;
                para quem ouve, ele precisa virar palavra. */}
            {concluido ? <span className="sr-only"> (concluído)</span> : null}
          </li>
        )
      })}
    </ol>
  )
}
