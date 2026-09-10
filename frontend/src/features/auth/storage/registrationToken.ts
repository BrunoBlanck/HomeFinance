import { storedRegistrationSchema } from '../schemas/auth'

/** Onde vive o `registrationToken` da tentativa de cadastro em curso (ADR-014),
 *  **amarrado ao endereço que o pediu**.
 *
 *  POR QUE O E-MAIL VAI JUNTO. Para o servidor, a tentativa é procurada pelo par
 *  `(email, hash do token)` — um token de OUTRO endereço é indistinguível de
 *  token nenhum. Guardar só o token faria a interface confundir "tenho **um**
 *  token" com "tenho **o** token desta tentativa", e essa confusão é
 *  explorável: com um token velho na aba e um endereço diferente no formulário,
 *  o cliente pediria reenvio para um endereço que o seu token não governa. Ler
 *  o token pelo endereço (`readRegistrationTokenFor`) transforma esse caso no
 *  que ele já é do lado do servidor — token nenhum — e faz a tela oferecer a
 *  recuperação em vez de um beco sem saída.
 *
 *  O custo de privacidade é nulo na prática: o e-mail já viaja em
 *  `history.state`, que o navegador persiste em disco para restaurar sessão
 *  (verificado no Chromium: o state sobrevive ao F5), e está na tela enquanto a
 *  pessoa confirma. O ganho é fechar uma confusão de identidade. Se um dia o
 *  e-mail sair do `history.state`, esta decisão se reavalia.
 *
 *  POR QUE ISTO NÃO FURA docs/SEGURANCA.md §1. A regra de lá proíbe **token de
 *  sessão** em `localStorage`/`sessionStorage`, e continua valendo inteira: a
 *  sessão do HomeFinance vive em cookies `HttpOnly` que o frontend nunca lê nem
 *  grava. O `registrationToken` é outra classe de segredo — sozinho não
 *  autentica ninguém e não abre dado algum, só serve junto do código de 6
 *  dígitos que foi para a caixa de entrada, o backend o guarda apenas como hash
 *  SHA-256, e ele morre na verificação. A exceção está registrada, com escopo,
 *  em docs/SEGURANCA.md §1.
 *
 *  Sob XSS nesta origem o token é legível — mas sob XSS o código que a pessoa
 *  digita no `CodeInput` também é, então guardá-lo aqui não amplia a superfície
 *  DESTE fluxo. O que um cookie `HttpOnly; Path=/api/v1/auth` daria a mais é
 *  imunidade à **plantação** por XSS; perde-se em troca o isolamento por aba
 *  (dois cadastros em duas abas se atropelariam) e o token passaria a
 *  sobreviver ao fechamento da aba. É um trade-off, não uma vitória clara.
 *
 *  POR QUE `sessionStorage` e não outro lugar:
 *
 *  - **memória apenas**: morre no F5, e perder o token obriga a refazer o
 *    cadastro. Aqui a memória é só rede de proteção (ver `emMemoria`);
 *  - **`localStorage`**: sobrevive demais — fica no disco depois de o fluxo
 *    acabar e é visível a todas as abas do perfil;
 *  - **query string**: proibida para dado sensível (docs/SEGURANCA.md §6) —
 *    vaza em log de servidor, histórico e cabeçalho `Referer`. */
const KEY = 'hf.registration'

/** Rede de proteção para navegador com storage bloqueado (modo restrito, iframe
 *  de terceiro, cota estourada). Sem ela, `setItem` falhando em silêncio deixava
 *  a pessoa num laço: cadastra, cai em "refaça o cadastro", cadastra de novo,
 *  para sempre — gastando uma mensagem da cota por endereço a cada volta. Como a
 *  navegação é SPA, esta variável cobre o fluxo inteiro na mesma aba; só o F5
 *  fica de fora, e aí o `sessionStorage` (quando existe) assume. */
let emMemoria: { token: string; email: string } | null = null

/** O endereço é comparado normalizado, como o backend faz antes de consultar. */
function normalizeEmail(email: string): string {
  return email.trim().toLowerCase()
}

function readRecord(): { token: string; email: string } | null {
  if (emMemoria !== null) return emMemoria
  try {
    const raw = sessionStorage.getItem(KEY)
    if (raw === null) return null
    const parsed = storedRegistrationSchema.safeParse(JSON.parse(raw))
    return parsed.success ? parsed.data : null
  } catch {
    // Storage bloqueado ou JSON corrompido: vale como se não houvesse nada.
    return null
  }
}

/** Devolve o token **se** ele for o desta tentativa. Token de outro endereço vale
 *  o mesmo que token nenhum — é assim que o servidor o trata. */
export function readRegistrationTokenFor(email: string): string | null {
  const record = readRecord()
  if (record === null) return null
  return record.email === normalizeEmail(email) ? record.token : null
}

/** Grava o par e devolve o token guardado — `null` se a resposta veio malformada,
 *  caso em que a tela cai no caminho de recuperação em vez de mandar sujeira
 *  para a API. */
export function saveRegistration(token: unknown, email: string): string | null {
  const parsed = storedRegistrationSchema.safeParse({ token, email: normalizeEmail(email) })
  if (!parsed.success) {
    clearRegistration()
    return null
  }
  emMemoria = parsed.data
  try {
    sessionStorage.setItem(KEY, JSON.stringify(parsed.data))
  } catch {
    // `emMemoria` cobre o fluxo nesta aba; só o F5 fica sem rede.
  }
  return parsed.data.token
}

/** Apaga o par. Chamado quando o fluxo termina — verificação concluída ou sessão
 *  criada por login —, porque a capacidade é de uso único. */
export function clearRegistration(): void {
  emMemoria = null
  try {
    sessionStorage.removeItem(KEY)
  } catch {
    // Sem storage não há o que limpar.
  }
}
