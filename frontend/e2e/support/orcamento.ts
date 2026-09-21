/** Leitura do log do backend: quantas requisições a API serviu e quantas ela
 *  RECUSOU por limite de abuso (429).
 *
 *  ## O pacer que morava aqui (histórico — leia antes de ressuscitá-lo)
 *
 *  Até 17/09/2026 este arquivo era um **pacer**: antes de cada teste ele
 *  reconstruía, a partir do log, o balde global da API (100 requisições por
 *  minuto por IP, `config.DefaultRateLimits().Global`) e dormia até haver
 *  folga. Existia porque a suíte inteira roda em série contra UMA API e UM IP,
 *  cada tela carrega de cinco a oito recursos, e o `StrictMode` do React no
 *  servidor de desenvolvimento do Vite faz cada consulta sair DUAS vezes — a
 *  primeira é abortada pelo cliente, mas o servidor já a serviu e já cobrou a
 *  ficha. A cadência passava dos 100/min e um `GET /transactions` qualquer
 *  respondia 429; o teste falhava por "Muitas tentativas", que não é defeito
 *  do app nem do teste.
 *
 *  **Ele foi aposentado, e não por capricho: ele não funcionava.** Medição de
 *  17/09/2026, suíte inteira (49 casos) com os limites de produção e o pacer
 *  ligado: **468 requisições, 2 recusadas com 429** mesmo assim
 *  (`GET /categories` e `POST /imports/{id}/confirm`) — 40 passaram, **2
 *  falharam**, 7 nem chegaram a rodar, em 4,8 min. O motivo é estrutural: o
 *  pacer só era chamado em 3 dos 7 arquivos de spec, e as requisições dos
 *  outros quatro esvaziavam o balde sem nunca passar por ele. Um pacer parcial
 *  é pior que nenhum, porque dá a impressão de que o problema está tratado.
 *
 *  A solução verdadeira veio do backend (decisão do usuário, 17/09/2026):
 *  `RATE_LIMITS_PROFILE=test` troca o **conjunto inteiro** de limites por
 *  tetos de robô — e só é **aceito com `APP_ENV=test`**
 *  (`docs/SEGURANCA.md` §5.2). O `global-setup.ts` liga esse perfil no
 *  processo da API de teste, e o balde global passa de 100/min para 3000/min —
 *  bem acima da cadência real da suíte. Mesma suíte, mesmo dia, com o perfil
 *  ligado e o pacer fora: **49 de 49 passaram em 1,9 min, 556 requisições,
 *  ZERO recusadas**. O pacer não só era desnecessário: ele custava 2,9 min de
 *  espera por corrida (4,8 → 1,9) para não resolver o problema.
 *
 *  **Como ressuscitar o pacer**, se algum dia a suíte crescer a ponto de
 *  estourar até o teto frouxo (ou se alguém precisar rodá-la com os limites de
 *  produção): `respeitarOrcamentoGlobal()` continua exportada e funcionando
 *  abaixo. Basta `test.beforeEach(() => respeitarOrcamentoGlobal())` — em
 *  TODOS os arquivos de spec, não em três — e ajustar `CAPACIDADE` para o
 *  balde que estiver valendo. Mas prefira descobrir por que a suíte precisa de
 *  tanta requisição: o limite do backend é defesa, não sintonia, e o número
 *  aqui nunca deve virar desculpa para mexer nele.
 *
 *  ## O que este arquivo faz hoje
 *
 *  `recusasPorLimite()` conta os 429 do log, e o `global-teardown.ts` usa essa
 *  contagem para falhar a execução quando aparece algum. É o guarda que
 *  faltava: se alguém remover o `RATE_LIMITS_PROFILE` do global setup, ou se a
 *  suíte crescer além do teto frouxo, a corrida diz isso em uma linha em vez
 *  de produzir uma falha misteriosa de "Muitas tentativas" no meio de um teste
 *  de importação. */

import { readFileSync } from 'node:fs'
import { ARQUIVO_SAIDA } from './ambiente'

/** O balde GLOBAL de produção, que o pacer histórico reproduzia. Só é usado
 *  por `respeitarOrcamentoGlobal`, que hoje ninguém chama — ver o cabeçalho. */
const CAPACIDADE = 100
const RECARGA_POR_SEGUNDO = CAPACIDADE / 60
/** Quantas fichas o balde precisa ter ANTES de um teste começar (idem). */
const FOLGA_POR_TESTE = 80

/** O backend loga TODA requisição servida (`msg=http`, com `time=` e
 *  `status=`) no arquivo que o global setup redireciona. */
const RE_LINHA = /^time=(\S+)\s.*msg=http\b.*\sstatus=(\d{3})\b/

type Servida = { em: number; recusada: boolean }

function requisicoes(): Servida[] {
  let texto = ''
  try {
    texto = readFileSync(ARQUIVO_SAIDA, 'utf8')
  } catch {
    return []
  }
  const lista: Servida[] = []
  for (const linha of texto.split('\n')) {
    const achado = RE_LINHA.exec(linha)
    if (!achado?.[1] || !achado[2]) continue
    const em = Date.parse(achado[1])
    if (Number.isNaN(em)) continue
    lista.push({ em, recusada: achado[2] === '429' })
  }
  return lista
}

/** Quantas requisições a API serviu e quantas ela recusou com 429.
 *
 *  Uma recusa é sempre defeito de ORQUESTRAÇÃO da suíte, nunca do produto:
 *  nenhum teste daqui exercita o limitador de propósito (isso é assunto dos
 *  testes de unidade do backend, que o medem sem navegador). */
export function recusasPorLimite(): { total: number; recusadas: number } {
  const lista = requisicoes()
  return { total: lista.length, recusadas: lista.filter((r) => r.recusada).length }
}

/** O balde reproduzido a partir do log: quantas fichas sobram AGORA.
 *
 *  Peça do pacer aposentado — ver o cabeçalho antes de usar. */
export function fichasRestantes(agora: number): number {
  let fichas = CAPACIDADE
  let ultimo = Number.NEGATIVE_INFINITY
  for (const r of requisicoes()) {
    if (ultimo !== Number.NEGATIVE_INFINITY) {
      // O log sai na ordem de CONCLUSÃO; um par fora de ordem por milissegundos
      // não pode "descarregar" o balde.
      const decorrido = Math.max(0, r.em - ultimo) / 1000
      fichas = Math.min(CAPACIDADE, fichas + decorrido * RECARGA_POR_SEGUNDO)
    }
    ultimo = Math.max(ultimo, r.em)
    // Uma recusa não gasta ficha — foi justamente a falta dela.
    if (!r.recusada) fichas = Math.max(0, fichas - 1)
  }
  if (ultimo !== Number.NEGATIVE_INFINITY) {
    fichas = Math.min(CAPACIDADE, fichas + ((agora - ultimo) / 1000) * RECARGA_POR_SEGUNDO)
  }
  return fichas
}

/** Espera até haver folga para o próximo teste.
 *
 *  O pacer aposentado — ver o cabeçalho. Ninguém chama isto hoje; continua
 *  aqui, funcionando, para o dia em que a suíte precisar rodar contra os
 *  limites de produção. */
export async function respeitarOrcamentoGlobal(folga = FOLGA_POR_TESTE): Promise<void> {
  const restantes = fichasRestantes(Date.now())
  if (restantes >= folga) return
  const segundos = (folga - restantes) / RECARGA_POR_SEGUNDO
  await new Promise((r) => setTimeout(r, Math.ceil(segundos * 1000) + 250))
}
