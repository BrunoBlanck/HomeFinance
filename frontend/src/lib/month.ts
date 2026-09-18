/** O mês da aplicação — `YYYY-MM`.
 *
 *  Regra que este módulo existe para concentrar (ADR-019 e risco R6 do
 *  PLANOS.md): **"hoje" é no fuso da CASA**, não no do navegador nem no do
 *  servidor. É o fuso da casa que decide em que mês a pessoa está — e, a partir
 *  da E3, se uma conta está atrasada.
 *
 *  O bug que isso impede: às 21h de 30 de setembro em São Paulo, o navegador de
 *  alguém em Lisboa já está em 1º de outubro. Sem este módulo, o app abriria em
 *  outubro para essa pessoa, e o mês de setembro pareceria vazio.
 *
 *  Um lugar só, testado com o processo em UTC e em `America/Sao_Paulo`. */

const FORMATO = /^\d{4}-(0[1-9]|1[0-2])$/

/** Fuso usado quando a casa não informou um (linha antiga, resposta parcial).
 *  Mesmo default do backend. */
export const FUSO_PADRAO = 'America/Sao_Paulo'

/** `true` se o texto é um mês bem formado. */
export function mesValido(valor: string): boolean {
  return FORMATO.test(valor)
}

/** O mês de um instante, no fuso informado.
 *
 *  Usa `Intl.DateTimeFormat` em vez de aritmética de `Date` porque só ele sabe
 *  o deslocamento correto de um fuso IANA numa data específica — inclusive
 *  horário de verão, que já mudou de regra no Brasil e pode mudar de novo. */
export function mesDe(instante: Date, fuso: string): string {
  const partes = formatador(fuso).formatToParts(instante)
  const ano = partes.find((p) => p.type === 'year')?.value ?? ''
  const mes = partes.find((p) => p.type === 'month')?.value ?? ''
  return `${ano}-${mes}`
}

/** O mês corrente no fuso da casa. */
export function mesCorrente(fuso: string, agora: Date = new Date()): string {
  return mesDe(agora, fuso)
}

/** Hoje no fuso da casa, em `AAAA-MM-DD`.
 *
 *  `Intl` com `en-CA` (que já formata `AAAA-MM-DD`) em vez de
 *  `toISOString().slice(0, 10)`: aquele devolve a data em UTC, e às 21h em São
 *  Paulo já seria o dia seguinte — um formulário nasceria com data de amanhã. É
 *  a mesma razão de `mesDe`. Fuso desconhecido cai no padrão em vez de derrubar
 *  a tela. */
export function hojeNoFuso(fuso: string, agora: Date = new Date()): string {
  const opcoes = { year: 'numeric', month: '2-digit', day: '2-digit' } as const
  try {
    return new Intl.DateTimeFormat('en-CA', { timeZone: fuso || FUSO_PADRAO, ...opcoes }).format(
      agora,
    )
  } catch {
    return new Intl.DateTimeFormat('en-CA', { timeZone: FUSO_PADRAO, ...opcoes }).format(agora)
  }
}

/** Soma (ou subtrai) meses, sem passar por `Date` — o cálculo é sobre os
 *  próprios números, então não há fuso nem horário de verão para atrapalhar, e
 *  "31 de janeiro + 1 mês" não vira 3 de março. */
export function somarMeses(mes: string, delta: number): string {
  const [ano, numero] = separar(mes)
  const total = ano * 12 + (numero - 1) + delta
  const novoAno = Math.floor(total / 12)
  const novoMes = total - novoAno * 12 + 1
  return `${String(novoAno).padStart(4, '0')}-${String(novoMes).padStart(2, '0')}`
}

const NOMES = [
  'janeiro',
  'fevereiro',
  'março',
  'abril',
  'maio',
  'junho',
  'julho',
  'agosto',
  'setembro',
  'outubro',
  'novembro',
  'dezembro',
]

/** "setembro de 2026" — rótulo por extenso, para leitor de tela e para o
 *  cabeçalho. Sem abreviação: "set/26" economiza espaço que não falta. */
export function mesPorExtenso(mes: string): string {
  const [ano, numero] = separar(mes)
  const nome = NOMES[numero - 1] ?? ''
  return `${nome} de ${ano}`
}

const NOMES_CURTOS = [
  'jan',
  'fev',
  'mar',
  'abr',
  'mai',
  'jun',
  'jul',
  'ago',
  'set',
  'out',
  'nov',
  'dez',
]

/** "set" — o mês abreviado, em minúsculas e sem ponto.
 *
 *  Serve a **um** lugar só: o eixo decorativo do gráfico de 12 meses
 *  (docs/DESIGN.md, E7 (c)), que é `aria-hidden` e tem a tabela ao lado com o
 *  mês por extenso. Em qualquer texto lido — `caption`, célula, frase — o mês
 *  continua sendo `mesPorExtenso`. */
export function mesCurto(mes: string): string {
  const [, numero] = separar(mes)
  return NOMES_CURTOS[numero - 1] ?? ''
}

/** "agosto" — só o nome do mês, sem o ano.
 *
 *  Para frases em que o ano já está dito ao lado (o seletor de mês do
 *  cabeçalho, sempre visível): "12 lançamentos de agosto estão sem categoria"
 *  lê melhor que "de agosto de 2026", e a ambiguidade não existe porque o ano
 *  está na tela. Em texto que viaja sozinho, use `mesPorExtenso`. */
export function nomeDoMes(mes: string): string {
  const [, numero] = separar(mes)
  return NOMES[numero - 1] ?? ''
}

/** "Setembro de 2026" — a mesma coisa, com inicial maiúscula. */
export function mesPorExtensoCapitalizado(mes: string): string {
  const texto = mesPorExtenso(mes)
  return texto.charAt(0).toUpperCase() + texto.slice(1)
}

/** Normaliza o que veio da URL: mês inválido cai no corrente, em vez de
 *  quebrar a tela. A URL é editável pelo usuário e é entrada externa — o
 *  backend também valida, mas a interface não pode explodir antes disso. */
export function mesDaURL(bruto: string | undefined, fuso: string, agora?: Date): string {
  if (bruto && mesValido(bruto)) return bruto
  return mesCorrente(fuso, agora)
}

function separar(mes: string): [number, number] {
  if (!mesValido(mes)) return [1970, 1]
  const ano = Number(mes.slice(0, 4))
  const numero = Number(mes.slice(5, 7))
  return [ano, numero]
}

const cacheDeFormatadores = new Map<string, Intl.DateTimeFormat>()

function formatador(fuso: string): Intl.DateTimeFormat {
  const chave = fuso || FUSO_PADRAO
  const existente = cacheDeFormatadores.get(chave)
  if (existente) return existente

  let criado: Intl.DateTimeFormat
  try {
    criado = new Intl.DateTimeFormat('pt-BR', {
      timeZone: chave,
      year: 'numeric',
      month: '2-digit',
    })
  } catch {
    // Fuso desconhecido (dado velho, ou navegador sem a base completa) cai no
    // padrão em vez de derrubar a tela. Errar o mês por um dia é ruim; não
    // renderizar nada é pior.
    criado = new Intl.DateTimeFormat('pt-BR', {
      timeZone: FUSO_PADRAO,
      year: 'numeric',
      month: '2-digit',
    })
  }
  cacheDeFormatadores.set(chave, criado)
  return criado
}
