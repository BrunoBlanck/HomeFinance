import type { StatementConfirmation } from '@/api/types'
import { Panel } from '@/components/Panel/Panel'
import { Select } from '@/components/Select/Select'
import { TextField } from '@/components/TextField/TextField'
import { mesPorExtensoCapitalizado, somarMeses } from '@/lib/month'
import styles from './FaturaFields.module.css'

type Props = {
  /** O nome da conta do cartão, para o título dizer de qual fatura se trata. */
  nomeDaConta: string
  valor: StatementConfirmation
  onChange: (valor: StatementConfirmation) => void
  erro?: string | undefined
}

/** Competência, fechamento e vencimento da fatura — só quando o arquivo é uma
 *  fatura de cartão.
 *
 *  Os três campos chegam **pré-preenchidos com a sugestão do servidor**, e
 *  sugestão não é decisão: fechamento e vencimento vieram do arquivo, mas a
 *  competência é uma escolha da pessoa — é o mês em que ela espera ver aquele
 *  gasto no app. Errar a competência joga dezenas de linhas para o mês errado,
 *  e é por isso que ela aparece aqui em vez de ser deduzida em silêncio.
 *
 *  A janela do seletor é curta de propósito (3 meses atrás, o sugerido, 1 à
 *  frente): uma fatura arquivada em 2019 não é um caso de uso, é um dedo
 *  escorregando. */
export function FaturaFields({ nomeDaConta, valor, onChange, erro }: Props) {
  const opcoesDeCompetencia = [-3, -2, -1, 0, 1].map((delta) => {
    const mes = somarMeses(valor.competenceMonth, delta)
    return { value: mes, label: mesPorExtensoCapitalizado(mes) }
  })

  // Se a competência gravada saiu da janela (um link antigo, um valor digitado
  // à mão), ela é acrescentada em vez de desaparecer do seletor — um <select>
  // cujo `value` não existe entre as opções mostra a primeira opção e mente
  // sobre o que está selecionado.
  if (!opcoesDeCompetencia.some((opcao) => opcao.value === valor.competenceMonth)) {
    opcoesDeCompetencia.unshift({
      value: valor.competenceMonth,
      label: mesPorExtensoCapitalizado(valor.competenceMonth),
    })
  }

  return (
    <Panel tone="sunken" title={`Fatura do ${nomeDaConta}`}>
      <div className={styles.campos}>
        <Select
          label="Competência"
          options={opcoesDeCompetencia}
          value={valor.competenceMonth}
          onChange={(evento) => onChange({ ...valor, competenceMonth: evento.target.value })}
        />
        <TextField
          label="Fechamento"
          type="date"
          value={valor.closingDate}
          onChange={(evento) => onChange({ ...valor, closingDate: evento.target.value })}
        />
        <TextField
          label="Vencimento"
          type="date"
          value={valor.dueDate}
          onChange={(evento) => onChange({ ...valor, dueDate: evento.target.value })}
          {...(erro ? { error: erro } : {})}
        />
      </div>
      <p className={styles.apoio}>
        Fechamento e vencimento vieram do arquivo. A competência é o mês em que esta fatura aparece
        no app.
      </p>
    </Panel>
  )
}

/** "O vencimento não pode ser antes do fechamento."
 *
 *  Comparação de **texto**, e é o certo: datas civis em `AAAA-MM-DD` ordenam
 *  lexicograficamente igual a cronologicamente, e comparar strings não passa
 *  por `Date` nenhum — logo não há fuso para deslocar o dia e transformar
 *  "mesmo dia" em "um dia antes". */
export function erroDaFatura(valor: StatementConfirmation): string | undefined {
  if (!valor.closingDate || !valor.dueDate) return undefined
  if (valor.dueDate < valor.closingDate) return 'O vencimento não pode ser antes do fechamento.'
  return undefined
}
