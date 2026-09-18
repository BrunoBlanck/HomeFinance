/** Data civil (`AAAA-MM-DD`) no cliente.
 *
 *  Este módulo existe por causa de UM bug, e ele é fácil de reintroduzir:
 *
 *  ```ts
 *  new Date('2026-08-31')                     // meia-noite UTC
 *  new Intl.DateTimeFormat('pt-BR').format(…) // em São Paulo (UTC−3): 30/08
 *  ```
 *
 *  Uma data civil não tem hora nem fuso — "31 de agosto" é 31 de agosto em
 *  Recife e em Lisboa. Passá-la pelo construtor de `Date` com string a
 *  transforma num INSTANTE, e formatar esse instante no fuso da casa devolve o
 *  dia anterior. Num extrato isso não é cosmético: o cabeçalho de dia mostra a
 *  data errada, a linha cai no grupo errado e o subtotal do dia passa a somar
 *  lançamentos de dois dias diferentes.
 *
 *  A regra, sem exceção: monte a partir das PARTES (`Date.UTC`) e formate em
 *  `timeZone: 'UTC'`. Os dois lados precisam casar — montar em UTC e formatar
 *  no fuso local tem exatamente o mesmo defeito.
 *
 *  Nada aqui depende do fuso da casa de propósito: o fuso da casa decide qual é
 *  o MÊS corrente (`lib/month.ts`), não como se escreve uma data que o servidor
 *  já mandou pronta. */

const FORMATO = /^(\d{4})-(\d{2})-(\d{2})$/

/** Partes de uma data civil, ou `null` quando o texto não é uma data real.
 *
 *  A volta pelo `Date.UTC` não é paranoia: `2026-02-31` casa com a expressão
 *  regular, e sem a conferência viraria 3 de março em silêncio. */
export function partesCivis(civil: string): [number, number, number] | null {
  const casou = FORMATO.exec(civil)
  if (!casou) return null

  const ano = Number(casou[1])
  const mes = Number(casou[2])
  const dia = Number(casou[3])

  const instante = new Date(Date.UTC(ano, mes - 1, dia))
  if (
    instante.getUTCFullYear() !== ano ||
    instante.getUTCMonth() !== mes - 1 ||
    instante.getUTCDate() !== dia
  ) {
    return null
  }
  return [ano, mes, dia]
}

/** `true` quando o texto é uma data civil que existe no calendário. */
export function civilValida(civil: string): boolean {
  return partesCivis(civil) !== null
}

/** O instante de meia-noite UTC daquele dia — o único `Date` que este módulo
 *  produz, e ele só serve para alimentar o `Intl` com `timeZone: 'UTC'`. */
function meioDiaUTC(civil: string): Date | null {
  const partes = partesCivis(civil)
  if (!partes) return null
  const [ano, mes, dia] = partes
  return new Date(Date.UTC(ano, mes - 1, dia))
}

// Formatadores no topo do módulo: construir um `Intl.DateTimeFormat` custa
// caro, e numa tabela de 200 linhas isso aconteceria uma vez por célula.
const porExtenso = new Intl.DateTimeFormat('pt-BR', {
  weekday: 'long',
  day: 'numeric',
  month: 'long',
  timeZone: 'UTC',
})
const curta = new Intl.DateTimeFormat('pt-BR', {
  day: '2-digit',
  month: '2-digit',
  timeZone: 'UTC',
})
const completa = new Intl.DateTimeFormat('pt-BR', {
  day: '2-digit',
  month: '2-digit',
  year: 'numeric',
  timeZone: 'UTC',
})

/** "segunda-feira, 31 de agosto" — cabeçalho de dia da tabela de lançamentos. */
export function diaPorExtenso(civil: string): string {
  const instante = meioDiaUTC(civil)
  if (!instante) return civil
  return porExtenso.format(instante)
}

/** "31/08" — coluna Data da revisão de importação, onde o ano é o mesmo em
 *  todas as linhas e repeti-lo 68 vezes seria ruído. */
export function dataCurta(civil: string): string {
  const instante = meioDiaUTC(civil)
  if (!instante) return civil
  return curta.format(instante)
}

/** "31/08/2026" — descrição de diálogo e período do arquivo, onde a data
 *  aparece sozinha e precisa carregar o ano. */
export function dataCompleta(civil: string): string {
  const instante = meioDiaUTC(civil)
  if (!instante) return civil
  return completa.format(instante)
}

/** O mês (`AAAA-MM`) de uma data civil, por fatia de texto.
 *
 *  Sem `Date` nenhum: o mês de "2026-08-31" são os sete primeiros caracteres,
 *  e qualquer caminho que passe por instante reabre a porta do deslocamento. */
export function mesDaDataCivil(civil: string): string | null {
  const partes = partesCivis(civil)
  if (!partes) return null
  const [ano, mes] = partes
  return `${String(ano).padStart(4, '0')}-${String(mes).padStart(2, '0')}`
}
