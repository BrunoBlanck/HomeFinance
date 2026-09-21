/** Identificadores de recurso que chegam pela URL.
 *
 *  A URL é **entrada externa**: a pessoa edita a barra de endereço, cola um
 *  link de terceiro e o navegador restaura sessões antigas. Nada vindo de lá
 *  entra numa query da API sem passar por aqui.
 *
 *  Isto **não** é controle de acesso — a autoridade é sempre o backend, que
 *  escopa tudo pela casa do token e responde 404 para recurso de outra casa
 *  (regra nº 1 do `AGENTS.md`). É higiene de borda: um `?conta=<script>` vira
 *  requisição malformada, 400 e uma tela quebrada por nada. Filtrado aqui, ele
 *  simplesmente some da busca e a tela abre sem filtro, que é o comportamento
 *  útil — o mesmo tratamento que `mes` já recebe em `lib/month.ts`. */

/** Forma canônica de UUID, com a faixa de versão aberta de 1 a 8.
 *
 *  Aberta de propósito: o backend emite **v7** (prefixo temporal, chaves
 *  ordenadas) e cai para **v4** quando o relógio falha. Uma expressão que só
 *  aceitasse `[1-5]`, copiada de exemplos antigos de UUID, derrubaria todo id
 *  real do sistema. */
const FORMA = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

/** `true` quando o texto tem a forma de um UUID canônico. */
export function uuidValido(valor: string): boolean {
  return FORMA.test(valor)
}

/** O valor quando ele é um UUID, `undefined` quando não é.
 *
 *  Devolver `undefined` em vez de lançar é deliberado: id inválido na URL não
 *  é motivo para uma tela em branco — o filtro some e a lista abre inteira. */
export function uuidDaURL(bruto: unknown): string | undefined {
  return typeof bruto === 'string' && uuidValido(bruto) ? bruto : undefined
}
