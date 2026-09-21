import { useEffect, useState } from 'react'
import type { Transaction } from '@/api/types'
import { FormError } from '@/components/Alert/FormError'
import { Button } from '@/components/Button/Button'
import { Dialog } from '@/components/Dialog/Dialog'
import { dataCompleta } from '@/lib/civil'
import { formatarDinheiro } from '@/lib/money'
import styles from './ExcluirLancamentoDialog.module.css'

type Props = {
  /** O lançamento a excluir; `null` mantém o diálogo fechado. */
  lancamento: Transaction | null
  /** A outra perna da transferência, quando ela está entre as linhas
   *  carregadas. Ausente não impede a exclusão — só muda o texto, que passa a
   *  falar de "a outra conta" em vez de nomeá-la. */
  contraparte?: Transaction | undefined
  onCancel: () => void
  onConfirm: () => void
  excluindo: boolean
  /** Erro da exclusão. Fica DENTRO do diálogo, que continua aberto. */
  erro?: string | undefined
}

/** Confirmação de exclusão de lançamento.
 *
 *  Composição sobre o `Dialog` existente, e não um `ConfirmDialog` novo: o que
 *  muda entre os dois casos é o TEXTO, e texto não é motivo para criar
 *  componente.
 *
 *  A regra que este arquivo existe para cumprir: excluir uma perna de
 *  transferência apaga **o par inteiro** (ADR-016), e isso é dito em texto
 *  corrido no título e no corpo — não num ícone, não numa cor. O rótulo do
 *  botão destrutivo repete o escopo ("as duas pernas") porque é o último texto
 *  que a pessoa lê antes de clicar, e é o único que ela lê se estiver com
 *  pressa. */
export function ExcluirLancamentoDialog({
  lancamento,
  contraparte,
  onCancel,
  onConfirm,
  excluindo,
  erro,
}: Props) {
  // O `<dialog>` fecha com animação e o conteúdo continua montado durante ela.
  // Sem lembrar do último alvo, o texto piscaria vazio no caminho de saída.
  const [ultimo, setUltimo] = useState<Transaction | null>(lancamento)
  useEffect(() => {
    if (lancamento) setUltimo(lancamento)
  }, [lancamento])

  const alvo = lancamento ?? ultimo
  if (!alvo) return null

  const texto = textoDoDialogo(alvo, contraparte)

  return (
    <Dialog
      open={lancamento !== null}
      onClose={onCancel}
      title={texto.titulo}
      description={texto.descricao}
      footer={
        <>
          <Button variant="quiet" onClick={onCancel}>
            Cancelar
          </Button>
          <Button variant="danger" onClick={onConfirm} loading={excluindo}>
            {texto.botao}
          </Button>
        </>
      }
    >
      {/* Erro de exclusão não vira toast: o toast some, e a pessoa ficaria
          olhando um diálogo aberto sem saber se apagou ou não. Ele vira texto
          aqui, com os botões no lugar para tentar de novo. */}
      {erro ? (
        <FormError id="excluir-lancamento-erro" autoFocus={false}>
          {erro}
        </FormError>
      ) : null}
      <p className={styles.corpo}>{texto.corpo}</p>
    </Dialog>
  )
}

type TextoDoDialogo = {
  titulo: string
  descricao: string
  corpo: string
  botao: string
}

function textoDoDialogo(alvo: Transaction, contraparte: Transaction | undefined): TextoDoDialogo {
  const valor = formatarDinheiro(Math.abs(alvo.amountCents))
  const data = dataCompleta(alvo.occurredOn)

  if (alvo.kind !== 'transfer_out' && alvo.kind !== 'transfer_in') {
    const descricao = alvo.description.trim()
    return {
      titulo: 'Excluir este lançamento?',
      descricao: [descricao || 'Sem descrição', data, valor, alvo.accountName].join(' · '),
      corpo: 'O lançamento sai da lista e dos totais do mês, e o saldo da conta é recalculado.',
      botao: 'Excluir lançamento',
    }
  }

  // A direção do par vem do `kind`, não da ordem em que as linhas chegaram:
  // `transfer_out` é sempre a saída, `transfer_in` sempre a entrada.
  const origem = alvo.kind === 'transfer_out' ? alvo.accountName : contraparte?.accountName
  const destino = alvo.kind === 'transfer_in' ? alvo.accountName : contraparte?.accountName

  const temOsDoisNomes = origem !== undefined && destino !== undefined

  return {
    titulo: 'Excluir a transferência inteira?',
    descricao: temOsDoisNomes
      ? [data, valor, `${origem} → ${destino}`].join(' · ')
      : [data, valor, alvo.accountName].join(' · '),
    corpo: temOsDoisNomes
      ? `Este lançamento é uma das duas pernas de uma transferência. Excluir aqui apaga as duas: a saída de ${valor} da ${origem} e a entrada de ${valor} no ${destino}. Os saldos das duas contas são recalculados.`
      : `Este lançamento é uma das duas pernas de uma transferência. Excluir aqui apaga as duas: a saída de ${valor} e a entrada de ${valor} na outra conta. Os saldos das duas contas são recalculados.`,
    botao: 'Excluir as duas pernas',
  }
}
