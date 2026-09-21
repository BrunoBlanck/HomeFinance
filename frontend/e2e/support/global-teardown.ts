/** Derruba a API que o global setup subiu — e presta contas do que ela serviu.
 *
 *  Deixar processo órfão segurando a porta 8099 faz a próxima execução falhar
 *  com uma mensagem que não explica nada, então matar o processo é
 *  obrigatório. O banco e o log ficam no diretório temporário de propósito:
 *  quando um teste falha, eles são a prova do que aconteceu.
 *
 *  Antes de matar, o teardown lê o log e reporta quantas requisições a API
 *  serviu e quantas recusou com 429 — e **falha a corrida** se recusou alguma.
 *  Ver `orcamento.ts` para a história dessa conta. */

import { readFileSync } from 'node:fs'
import { ARQUIVO_ESTADO, ARQUIVO_SAIDA } from './ambiente'
import { recusasPorLimite } from './orcamento'

export default async function globalTeardown(): Promise<void> {
  // Quantas requisições a API serviu e quantas perdeu para o limitador.
  //
  // Lido ANTES de matar o processo, e reportado sempre: "quantos 429 tiveram"
  // é a pergunta que se faz depois de toda mudança na orquestração da suíte, e
  // ela não deveria exigir que alguém abra o log e escreva um `grep`.
  const { total, recusadas } = recusasPorLimite()
  console.error(
    `API de teste: ${total} requisições servidas, ${recusadas} recusadas por limite (429).`,
  )

  let pid: number | undefined
  try {
    pid = (JSON.parse(readFileSync(ARQUIVO_ESTADO, 'utf8')) as { pid?: number }).pid
  } catch {
    return // Setup não chegou a gravar: não há o que matar.
  }

  if (!pid) return

  try {
    process.kill(pid)
  } catch {
    // Já morreu (ou o SO reciclou o PID). Nada a fazer.
  }

  console.error(`API de teste encerrada. Log do backend em ${ARQUIVO_SAIDA}`)

  // Um 429 aqui é sempre defeito de ORQUESTRAÇÃO da suíte, nunca do produto:
  // nenhum teste daqui exercita o limitador de propósito (isso é assunto dos
  // testes de unidade do backend, que o medem sem navegador). Se aparecer,
  // alguma requisição foi perdida e algum resultado desta corrida é ruído —
  // então a corrida falha dizendo isso, em vez de deixar o efeito reaparecer
  // amanhã como "Muitas tentativas" no meio de um teste de importação.
  if (recusadas > 0) {
    throw new Error(
      `A API recusou ${recusadas} requisição(ões) com 429 durante a suíte. ` +
        'Confira se a API subiu com RATE_LIMITS_PROFILE=test (global-setup.ts) e, ' +
        `se subiu, se a suíte não cresceu além do teto frouxo. Log em ${ARQUIVO_SAIDA}`,
    )
  }
}
