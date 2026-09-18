import { z } from 'zod'

/** Palavras-chave de categoria e de conta (spec 0005 §3 e §4.1).
 *
 *  Tudo aqui é **conforto de tela**: deduplicar antes de mandar, recusar na
 *  hora o que o servidor recusaria, e sugerir as palavras de uma descrição.
 *  A autoridade continua sendo o backend (`internal/textmatch`), que aplica as
 *  mesmas regras — e é ele quem decide o 400 e o 409. Se as duas
 *  implementações divergirem, o servidor está certo e este arquivo está
 *  errado. */

/** Máximo de palavras por categoria ou conta (§4.1.3). */
export const MAX_PALAVRAS_CHAVE = 20

/** Lista FECHADA de palavras vazias em português, idêntica à do backend
 *  (`textmatch.Stopwords`). Não é configurável: é regra do algoritmo, e uma
 *  lista diferente do lado da tela faria a sugestão de palavras oferecer o que
 *  o servidor descarta. */
export const STOPWORDS: ReadonlySet<string> = new Set([
  'de',
  'do',
  'da',
  'dos',
  'das',
  'e',
  'o',
  'a',
  'os',
  'as',
  'em',
  'no',
  'na',
  'nos',
  'nas',
  'um',
  'uma',
  'por',
  'para',
  'com',
  'sem',
  'seu',
  'sua',
  'ltda',
  'me',
  'sa',
  'eireli',
  'epp',
])

/** Allowlist de forma do contrato (`Keyword.pattern`): letras, dígitos, espaço
 *  e `& . - / '`. */
const FORMA = /^[\p{L}\p{N} &./'-]+$/u

/** Forma normalizada: sem acento, minúscula, espaços colapsados. É por ela que
 *  duas palavras são "a mesma" — «Padaria» e «padaria» não podem coexistir na
 *  lista, e é ela que o 409 devolve em `fields.keyword`. */
export function normalizarPalavra(texto: string): string {
  return texto.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase().replace(/\s+/g, ' ').trim()
}

/** Contagem em RUNAS (code points), como o servidor conta — `length` contaria
 *  «ç» decomposto como dois e um emoji como dois. */
function runas(texto: string): number {
  return Array.from(texto).length
}

/** As palavras úteis de uma descrição, na mesma regra da §3: separa por
 *  qualquer coisa que não seja letra ou dígito, descarta o que tem menos de 2
 *  runas e as palavras vazias. «Mercado do seu José» → `mercado`, `jose`.
 *
 *  Serve a duas coisas: as fichas de "da próxima vez, reconhecer por" na
 *  revisão da importação, e a validação de que uma palavra-chave produz ao
 *  menos uma palavra útil (emenda §10.3). */
export function tokenizar(descricao: string): string[] {
  return normalizarPalavra(descricao)
    .split(/[^\p{L}\p{N}]+/u)
    .filter((palavra) => runas(palavra) >= 2 && !STOPWORDS.has(palavra))
}

export type PalavraValidada =
  | {
      ok: true
      /** Como será exibida e enviada: só as pontas e as repetições de espaço
       *  foram removidas; caixa e acento ficam como a pessoa digitou. */
      palavra: string
      /** A forma pela qual ela é comparada. */
      normalizada: string
    }
  | { ok: false; motivo: string }

/** Copy da tabela (g) de `docs/DESIGN.md` — as frases são as do designer. */
export const MOTIVO = {
  curta: 'Use ao menos 2 letras ou números.',
  longa: 'No máximo 40 caracteres.',
  caractere: "Só letras, números, espaço e & . - / '",
  vazia:
    'Essa palavra é comum demais para reconhecer um lançamento — use o nome do estabelecimento.',
} as const

/** Valida uma palavra-chave na hora de ADICIONAR: 2–40 runas na forma
 *  exibível e na normalizada, allowlist de caracteres e ao menos uma palavra
 *  útil depois da tokenização. A ordem das checagens é a ordem em que a
 *  pessoa consegue agir: primeiro o tamanho, depois o caractere estranho, por
 *  fim o "é comum demais". */
export function validarPalavra(bruto: string): PalavraValidada {
  const palavra = bruto.replace(/\s+/g, ' ').trim()
  const normalizada = normalizarPalavra(palavra)

  if (runas(palavra) < 2 || runas(normalizada) < 2) return { ok: false, motivo: MOTIVO.curta }
  if (runas(palavra) > 40 || runas(normalizada) > 40) return { ok: false, motivo: MOTIVO.longa }
  if (!FORMA.test(palavra)) return { ok: false, motivo: MOTIVO.caractere }
  if (tokenizar(normalizada).length === 0) return { ok: false, motivo: MOTIVO.vazia }

  return { ok: true, palavra, normalizada }
}

/** Índice da palavra da lista cuja forma normalizada é `normalizada`, ou
 *  `-1`. É assim que o 409 (`fields.keyword`) e a duplicata local encontram a
 *  ficha a marcar. */
export function indiceDaPalavra(lista: readonly string[], normalizada: string): number {
  return lista.findIndex((palavra) => normalizarPalavra(palavra) === normalizada)
}

const palavraChaveSchema = z.string().superRefine((valor, ctx) => {
  const resultado = validarPalavra(valor)
  if (!resultado.ok) ctx.addIssue({ code: 'custom', message: resultado.motivo })
})

/** A lista inteira, como vai no corpo de `POST`/`PATCH`: até 20, cada uma
 *  válida. A repetição não é checada aqui porque o `KeywordsField` já não
 *  deixa entrar — e o servidor, se receber, responde `fields.keywords[i]`. */
export const keywordsSchema = z
  .array(palavraChaveSchema)
  .max(MAX_PALAVRAS_CHAVE, 'Limite de 20 palavras-chave. Remova uma para incluir outra.')

// ------------------------------------------------------ fichas de aprender

/** A palavra-chave citada, sempre entre aspas angulares: «nubank». É a única
 *  forma em que uma palavra aparece numa frase do app (docs/DESIGN.md, léxico
 *  da E2c) — toast, dica, rótulo de botão. */
export function citarPalavra(palavra: string): string {
  return `«${palavra}»`
}

/** Máximo de fichas por linha. Cinco cabe numa célula sem virar uma segunda
 *  tabela; quem quer mais edita a categoria. */
export const MAX_FICHAS = 5

/** Tokens acima disto nem viram ficha: o servidor recusa palavra-chave com
 *  mais de 40 runas, e oferecer o que vai dar 400 é oferecer um erro. */
const MAX_RUNAS = 40

/** As palavras que valem uma ficha "da próxima vez, reconhecer por": os
 *  tokens da descrição, na ordem em que aparecem, sem repetição, sem os só de
 *  dígitos (número de pedido não reconhece nada), sem os longos demais para o
 *  contrato e sem os que a categoria já tem. No máximo cinco; nenhuma quando a
 *  categoria já está no limite de vinte. As já aprendidas nesta linha ficam,
 *  como texto — mesmo depois de a categoria passar a tê-las.
 *
 *  Mora em `lib/` porque duas features a usam: a revisão da importação
 *  (`features/import/`) e o atalho de categorização em `/lancamentos`
 *  (`features/transactions/`), e import entre features é proibido. */
export function palavrasParaAprender(
  descricao: string,
  categoria: { keywords: readonly string[] },
  aprendidas: readonly string[] = [],
): string[] {
  const lotada = categoria.keywords.length >= MAX_PALAVRAS_CHAVE
  const existentes = new Set(categoria.keywords.map(normalizarPalavra))
  const vistas = new Set<string>()
  const fichas: string[] = []

  for (const token of tokenizar(descricao)) {
    if (fichas.length >= MAX_FICHAS) break
    if (vistas.has(token)) continue
    vistas.add(token)
    if (/^\p{N}+$/u.test(token)) continue
    if (runas(token) > MAX_RUNAS) continue
    if (aprendidas.includes(token)) {
      fichas.push(token)
      continue
    }
    // Com a categoria no limite, nenhuma ficha NOVA: o servidor recusaria a
    // vigésima primeira, e uma ficha que só dá erro não é uma oferta.
    if (lotada || existentes.has(token)) continue
    fichas.push(token)
  }
  return fichas
}
