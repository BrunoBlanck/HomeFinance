import { useQuery } from '@tanstack/react-query'
import { useNavigate, useParams, useRouterState } from '@tanstack/react-router'
import { useEffect } from 'react'
import type { ImportBatch, ImportResult } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { EmptyState } from '@/components/EmptyState/EmptyState'
import { Panel } from '@/components/Panel/Panel'
import { contaPorId, contasQueryOptions } from '@/lib/accounts'
import { dataCurta, mesDaDataCivil } from '@/lib/civil'
import { useFocoNoTitulo } from '@/lib/focus'
import { uuidValido } from '@/lib/id'
import { formatarDinheiro } from '@/lib/money'
import { nomeDoMes } from '@/lib/month'
import { loteDaImportacaoQueryOptions } from '../api/imports'
import { ImportStepper } from '../components/ImportStepper'
import styles from './ImportResultScreen.module.css'

/** Passo 3 da importação — **o que aconteceu de fato**.
 *
 *  Os números são os do servidor, nunca uma repetição do que a tela anterior
 *  prometeu: o confirm **recalcula tudo dentro da transação**, porque entre o
 *  envio e a confirmação o outro morador da casa pode ter importado o mesmo
 *  arquivo. Se a tela mostrasse a promessa do passo 2, ela mentiria exatamente
 *  no caso em que a verdade importa.
 *
 *  Dois avisos carregam o peso desta tela, e o primeiro é o motivo de ela
 *  existir:
 *
 *  - **pagamento de fatura deixado de fora** → enquanto ele não for registrado
 *    como transferência, a dívida do cartão **não volta a zero**: o dinheiro
 *    saiu da conta e a fatura continua cheia. Sem este aviso, a pessoa descobre
 *    sozinha, meses depois, que a dívida do cartão só cresce;
 *  - **linhas sem categoria** → elas não entram em orçamento nem em relatório
 *    enquanto ficarem assim.
 *
 *  O botão "Registrar a transferência" **navega** para `/lancamentos` levando os
 *  dados no state, em vez de importar qualquer coisa da outra feature: feature
 *  não importa de feature (`AGENTS.md`). */
export function ImportResultScreen() {
  const navigate = useNavigate()
  // Foco no <h1> na entrada da rota (docs/DESIGN.md).
  const tituloRef = useFocoNoTitulo()
  const parametros = useParams({ strict: false })
  const importId = typeof parametros.importId === 'string' ? parametros.importId : ''

  const resumo = useRouterState({ select: (estado) => estado.location.state.resumoDaImportacao })

  // O lote é buscado mesmo quando o state veio cheio: é ele que sobrevive a um
  // F5, e sem ele recarregar a tela do resultado mostraria uma página vazia.
  const lote = useQuery({
    ...loteDaImportacaoQueryOptions(importId),
    enabled: uuidValido(importId),
  })
  const contas = useQuery(contasQueryOptions(true))

  useEffect(() => {
    document.title = 'Importação concluída · HomeFinance'
  }, [])

  const faturaIgnorada = resumo?.faturaIgnorada
  const batch = lote.data?.batch
  const numeros = numerosDoResultado(resumo?.resultado, batch?.outcome)
  const conta = contaPorId(contas.data?.items ?? [], batch?.accountId)

  const mes = resumo?.mes || (batch ? (mesDaDataCivil(batch.maxDate) ?? '') : '')
  const nomeDoMesAfetado = mes ? nomeDoMes(mes) : 'do arquivo'
  const buscaDoMes = mes ? { mes } : {}

  const linhas = [
    { rotulo: 'Lançamentos importados', valor: numeros.imported },
    { rotulo: 'Lançamentos restaurados', valor: numeros.restored },
    { rotulo: 'Transferências registradas', valor: numeros.transfersCreated },
    { rotulo: 'Vinculadas a transferências já registradas', valor: numeros.linked },
    { rotulo: 'Linhas que você ignorou', valor: numeros.skipped },
    { rotulo: 'Linhas bloqueadas', valor: numeros.blocked + numeros.rejected },
    // Linha com valor 0 não renderiza: uma lista de zeros é ruído, e o que a
    // pessoa procura aqui é o que ACONTECEU.
  ].filter((linha) => linha.valor > 0)

  // Vincular não cria lançamento, mas ACONTECEU: um lote só de vínculos não é
  // "nada foi importado".
  const nadaImportado =
    numeros.imported + numeros.restored + numeros.transfersCreated + numeros.linked === 0

  return (
    <div className={styles.pagina}>
      <div>
        <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
          Importação concluída
        </h1>
        {batch ? (
          <p className={styles.contexto}>
            {conta?.name ?? 'Conta do lote'} ·{' '}
            {batch.docKind === 'card_statement' ? 'fatura' : 'extrato'} de{' '}
            {dataCurta(batch.minDate)} a {dataCurta(batch.maxDate)}
          </p>
        ) : null}
        {/* Visível E `role="status"`, sem duplicata `sr-only`. */}
        <p className={styles.frase} role="status">
          {fraseDoResultado(numeros)}
        </p>
      </div>

      <ImportStepper atual="resultado" />

      {nadaImportado ? (
        <EmptyState
          title="Nada foi importado."
          description="Todas as linhas ficaram de fora. O arquivo continua com você — se foi engano, envie de novo e revise as decisões."
          action={
            <Button variant="primary" onClick={() => void navigate({ to: '/importar' })}>
              Importar outro arquivo
            </Button>
          }
        />
      ) : (
        <Panel title="O que aconteceu">
          <dl className={styles.lista}>
            {linhas.map((linha) => (
              <div key={linha.rotulo} className={styles.linha}>
                <dt>{linha.rotulo}</dt>
                <dd>{linha.valor}</dd>
              </div>
            ))}
          </dl>
        </Panel>
      )}

      {nadaImportado ? null : (
        <div className={styles.acoes}>
          <Button
            variant="primary"
            onClick={() => void navigate({ to: '/lancamentos', search: buscaDoMes })}
          >
            Ver os lançamentos de {nomeDoMesAfetado}
          </Button>
          <Button variant="secondary" onClick={() => void navigate({ to: '/importar' })}>
            Importar outro arquivo
          </Button>
        </div>
      )}

      {faturaIgnorada ? (
        <Alert
          tone="warning"
          action={
            <Button
              size="sm"
              onClick={() =>
                void navigate({
                  to: '/lancamentos',
                  search: buscaDoMes,
                  // Os dados vão no STATE, nunca na query string: valor e data
                  // de um lançamento vazariam em log de servidor, histórico do
                  // navegador e cabeçalho Referer.
                  state: { novaTransferencia: faturaIgnorada },
                })
              }
            >
              Registrar a transferência
            </Button>
          }
        >
          Você deixou de fora o pagamento da fatura de {formatarDinheiro(faturaIgnorada.valorCents)}
          . Enquanto ele não for registrado como transferência da {faturaIgnorada.contaOrigemNome}{' '}
          para o cartão, a fatura do cartão continua com o valor cheio: o dinheiro já saiu da conta,
          mas a dívida do cartão não foi abatida.
        </Alert>
      ) : null}

      {resumo && resumo.semCategoria > 0 ? (
        <Alert
          tone="info"
          action={
            <Button
              size="sm"
              onClick={() =>
                void navigate({
                  to: '/lancamentos',
                  search: { ...buscaDoMes, semCategoria: 1 },
                })
              }
            >
              Categorizar agora
            </Button>
          }
        >
          {resumo.semCategoria === 1
            ? '1 lançamento entrou sem categoria. Ele não entra em orçamento nem em relatório enquanto ficar assim.'
            : `${resumo.semCategoria} lançamentos entraram sem categoria. Eles não entram em orçamento nem em relatório enquanto ficarem assim.`}
        </Alert>
      ) : null}
    </div>
  )
}

/** O `outcome` do lote, como o contrato o define — derivado do `ImportBatch`
 *  para não haver uma segunda definição dos contadores. */
type ImportOutcome = NonNullable<ImportBatch['outcome']>

type NumerosDoResultado = {
  imported: number
  restored: number
  skipped: number
  blocked: number
  rejected: number
  linked: number
  transfersCreated: number
}

/** Os números vêm do `ImportResult` da confirmação quando a pessoa acabou de
 *  confirmar, e do `outcome` do lote quando ela recarregou a página.
 *
 *  O `outcome` não guarda `transfersCreated` — ele é do resultado do confirm.
 *  Depois de um F5 esse número some, e mostrar 0 seria pior do que omitir a
 *  linha: a lista já esconde linhas com valor 0, então ela simplesmente não
 *  aparece em vez de afirmar que nenhuma transferência foi criada. `linked`,
 *  ao contrário, sobrevive no `outcome`. */
function numerosDoResultado(
  resultado: ImportResult | undefined,
  outcome: ImportOutcome | null | undefined,
): NumerosDoResultado {
  if (resultado) {
    return {
      imported: resultado.imported,
      restored: resultado.restored,
      skipped: resultado.skipped,
      blocked: resultado.blocked,
      rejected: resultado.rejected,
      linked: resultado.linked,
      transfersCreated: resultado.transfersCreated,
    }
  }
  if (outcome) {
    return { ...outcome, transfersCreated: 0 }
  }
  return {
    imported: 0,
    restored: 0,
    skipped: 0,
    blocked: 0,
    rejected: 0,
    linked: 0,
    transfersCreated: 0,
  }
}

/** "42 lançamentos importados, 1 restaurado, 1 transferência registrada, 2
 *  vinculadas a transferências que já existiam, 6 ignorados por você e 3
 *  bloqueados."
 *
 *  Monta só as partes que existem: uma frase que enumera cinco zeros não
 *  informa nada e ainda faz a pessoa procurar o que deu errado. */
function fraseDoResultado(numeros: NumerosDoResultado): string {
  const partes: string[] = []

  partes.push(
    numeros.imported === 1
      ? '1 lançamento importado'
      : `${numeros.imported} lançamentos importados`,
  )
  if (numeros.restored > 0) partes.push(`${numeros.restored} restaurado${plural(numeros.restored)}`)
  if (numeros.transfersCreated > 0) {
    partes.push(
      numeros.transfersCreated === 1
        ? '1 transferência registrada'
        : `${numeros.transfersCreated} transferências registradas`,
    )
  }
  if (numeros.linked > 0) {
    partes.push(
      numeros.linked === 1
        ? '1 vinculada a uma transferência que já existia'
        : `${numeros.linked} vinculadas a transferências que já existiam`,
    )
  }
  if (numeros.skipped > 0) {
    partes.push(`${numeros.skipped} ignorado${plural(numeros.skipped)} por você`)
  }

  const bloqueadas = numeros.blocked + numeros.rejected
  if (bloqueadas > 0) partes.push(`${bloqueadas} bloqueado${plural(bloqueadas)}`)

  if (partes.length === 1) return `${partes[0]}.`
  const ultima = partes.pop()
  return `${partes.join(', ')} e ${ultima}.`
}

function plural(n: number): string {
  return n === 1 ? '' : 's'
}
