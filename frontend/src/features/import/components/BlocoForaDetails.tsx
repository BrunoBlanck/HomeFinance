import type { ImportRow } from '@/api/types'
import { type Column, DataTable } from '@/components/DataTable/DataTable'
import styles from './BlocoForaDetails.module.css'
import { CelulaData, CelulaDescricao, CelulaValor } from './CelulasDaRevisao'

/** Bloco 3 — **Ficam de fora**.
 *
 *  Duas decisões de desenho, e as duas são deliberadas:
 *
 *  1. **É o único bloco colapsado**, e com `<details>`/`<summary>` NATIVOS —
 *     zero JavaScript, teclado de graça, e o estado "fechado" já anunciado pelo
 *     navegador. Colapsa porque é o único bloco onde **não existe decisão a
 *     tomar**: é auditoria, não trabalho. Colapsar o bloco das prontas
 *     esconderia 59 linhas prestes a virar dinheiro registrado.
 *
 *  2. **Nenhum controle. Nem desabilitado.** Um checkbox `disabled` tem
 *     contraste ruim, é pulado pela navegação por teclado e some para parte das
 *     tecnologias assistivas — a pessoa ficaria tentando marcar algo que não
 *     marca, sem saber por quê. A ausência do controle, com o `<summary>`
 *     dizendo "Nada aqui pode entrar", comunica melhor do que um controle
 *     morto. */
export function BlocoForaDetails({ linhas }: { linhas: readonly ImportRow[] }) {
  if (linhas.length === 0) return null

  const jaImportadas = linhas.filter((linha) => linha.status === 'duplicado_exato').length
  const invalidas = linhas.filter((linha) => linha.status === 'rejeitado').length

  const colunas: readonly Column<ImportRow>[] = [
    {
      key: 'data',
      header: 'Data',
      width: 'min',
      render: (linha) => <CelulaData linha={linha} />,
    },
    {
      key: 'descricao',
      header: 'Descrição',
      render: (linha) => <CelulaDescricao linha={linha} />,
    },
    {
      key: 'valor',
      header: 'Valor',
      align: 'end',
      width: 'min',
      render: (linha) => <CelulaValor linha={linha} />,
    },
  ]

  return (
    <details className={styles.fora}>
      <summary className={styles.resumo}>
        Ficam de fora · {linhas.length} {linhas.length === 1 ? 'linha' : 'linhas'}
      </summary>
      <p className={styles.apoio}>{explicacao(jaImportadas, invalidas)}</p>
      <div className={styles.tabela}>
        <DataTable
          caption="Linhas que não podem entrar"
          columns={colunas}
          rows={linhas}
          rowKey={(linha) => linha.id}
        />
      </div>
    </details>
  )
}

/** "2 já importadas · 1 linha inválida. Nada aqui pode entrar: …"
 *
 *  A frase se adapta à composição real do bloco. Escrever sempre as duas
 *  metades produziria "e as inválidas não têm data" num bloco sem nenhuma linha
 *  inválida — um texto que descreve algo que não está na tela. */
function explicacao(jaImportadas: number, invalidas: number): string {
  const partes: string[] = []
  if (jaImportadas > 0) {
    partes.push(jaImportadas === 1 ? '1 já importada' : `${jaImportadas} já importadas`)
  }
  if (invalidas > 0) {
    partes.push(invalidas === 1 ? '1 linha inválida' : `${invalidas} linhas inválidas`)
  }

  const contagem = partes.join(' · ')

  if (jaImportadas > 0 && invalidas > 0) {
    return `${contagem}. Nada aqui pode entrar: as já importadas criariam lançamento repetido, e as inválidas não têm data ou valor que dê para ler.`
  }
  if (jaImportadas > 0) {
    return `${contagem}. Nada aqui pode entrar: elas criariam lançamento repetido.`
  }
  return `${contagem}. Nada aqui pode entrar: não têm data ou valor que dê para ler.`
}
