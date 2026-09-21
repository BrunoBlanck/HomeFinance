import { uuidValido } from '@/lib/id'
import { mesValido } from '@/lib/month'

/** A busca (query string) compartilhada pelas telas autenticadas.
 *
 *  Validada **uma vez**, aqui, e não em cada tela: a URL é editável pela
 *  pessoa, chega por link colado e é restaurada pelo navegador. Nada que não
 *  passe por este arquivo alcança uma query da API.
 *
 *  Chave inválida não derruba a tela — ela **some da busca**. Mês inválido cai
 *  no corrente (o fuso da casa resolve), conta inválida abre a lista inteira,
 *  filtro inválido abre sem filtro. Esse é o comportamento útil: um link
 *  truncado no WhatsApp abre o app, não uma página de erro. */
export type BuscaDoApp = {
  /** `AAAA-MM`. O eixo do app inteiro (ADR-019). */
  mes?: string
  /** Conta, em forma de UUID. */
  conta?: string
  /** A OUTRA conta do par, em `/transferencias`.
   *
   *  Só faz sentido junto de `conta`: "as transferências entre X e Y" precisa
   *  do X. Sem `conta`, ou igual a ela, é descartada — o servidor responderia
   *  400 nos dois casos, e uma URL editada à mão não pode virar tela de erro
   *  quando pode virar a lista inteira. */
  contraparte?: string
  /** Filtro "só os sem categoria" de `/lancamentos`.
   *
   *  É `1` numérico, e não `'1'`, porque o roteador interpreta o valor da busca
   *  como JSON: `?semCategoria=1` chega aqui como número. Tipar como string
   *  faria a URL escrita à mão pela pessoa ser descartada, e a que o app gera
   *  sair como `?semCategoria=%221%22`. Só o `1` liga o filtro — `0`, `true` e
   *  qualquer outra coisa somem. */
  semCategoria?: 1
  /** Filtro por CATEGORIA de `/lancamentos` — o atalho "Ver lançamentos" do
   *  relatório por categoria.
   *
   *  UUID, como `conta`. Um grupo traz as subcategorias dele junto (quem
   *  expande é o servidor, ver `categoryId` no contrato): é o mesmo conjunto
   *  que a linha do grupo soma no relatório, e é o que faz o atalho abrir no
   *  mesmo dinheiro em que a pessoa clicou.
   *
   *  Não convive com `semCategoria`: "só os sem categoria" DENTRO de uma
   *  categoria é conjunto vazio por construção. A categoria vence, pelo mesmo
   *  motivo que `contraparte` morre sem `conta` — a combinação impossível
   *  some nas DUAS pontas em vez de virar uma lista vazia sem saída. */
  categoria?: string
  /** Natureza do relatório por categoria (`/relatorios/categorias`).
   *
   *  Allowlist de quatro palavras em pt-BR — a URL é interface. Ausente ou
   *  fora da lista, a tela cai em despesas: um `?natureza=tudo` colado no chat
   *  abre o relatório de gastos, não uma tela de erro. Despesas é o padrão e a
   *  URL canônica não escreve a chave.
   *
   *  `despesas-credito` e `despesas-debito` NÃO são naturezas da API — são
   *  recortes de conta da mesma natureza `expense` (ADR-032). A tradução da
   *  palavra para o par `{kind, accountGroup}` da API é da tela, nunca da URL:
   *  `?natureza=credit` é o erro de quem copia o parâmetro errado, e some. */
  natureza?: Natureza
  /** Filtro de TIPO de `/lancamentos` (spec 0004 §1.6, emenda E2d).
   *
   *  Allowlist de quatro palavras em pt-BR, como `natureza` — a URL é
   *  interface. "Tudo" é a **ausência** da chave: `?tipo=tudo` não existe, e a
   *  URL canônica não escreve o padrão. Fora da lista, a chave some e a tela
   *  abre em Tudo — inclusive quando o valor é o da API (`?tipo=expense`), que
   *  é justamente o erro de quem copia o parâmetro errado.
   *
   *  A tradução para o `kindGroup` da API é da tela, nunca da URL. */
  tipo?: TipoDeLancamento
  /** Tamanho da janela de trabalho de `/ia`, em MESES de competência (spec
   *  0010 §2.1, emenda §10 achado A2).
   *
   *  `1`, `2` ou `3`, e nada mais — o servidor recusa janela de mais de 3 meses
   *  com 400, então valor fora da lista some aqui em vez de virar tela de erro.
   *  A janela termina no `mes` da casca e anda para trás; o par
   *  `fromMonth`/`toMonth` sai de `features/ai/janela.ts`, nunca da URL.
   *
   *  `3` é o padrão e a **URL canônica não escreve a chave**, como em
   *  `natureza` e `tipo`. O número chega como número (o roteador lê o valor da
   *  busca como JSON); a string é aceita na ENTRADA porque `?meses=2` digitado
   *  à mão ou vindo de um link antigo não pode ser descartado em silêncio. */
  meses?: TamanhoDaJanela
}

/** Os três tamanhos da janela de `/ia`. Mora aqui, com o portão da URL, porque
 *  é a allowlist — a derivação dos meses mora em `features/ai/janela.ts`. */
export type TamanhoDaJanela = 1 | 2 | 3

const TAMANHOS: readonly TamanhoDaJanela[] = [1, 2, 3]

/** A allowlist do lado de fora, pelo mesmo motivo de `tipoValido`: o `<select>`
 *  da tela valida o valor escolhido por AQUI, e não por um `includes` próprio
 *  que divergiria do que a próxima leitura da URL aceita. */
export function tamanhoDaJanelaValido(valor: unknown): TamanhoDaJanela | undefined {
  const numero = typeof valor === 'string' ? Number(valor) : valor
  return typeof numero === 'number' && (TAMANHOS as readonly number[]).includes(numero)
    ? (numero as TamanhoDaJanela)
    : undefined
}

/** As quatro opções do filtro de natureza, na ordem em que a tela as oferece. */
export type Natureza = 'despesas' | 'despesas-credito' | 'despesas-debito' | 'receitas'

/** Exportada porque é a ORDEM do `<select>`: a tela monta as opções daqui, e
 *  uma lista escrita de novo lá seria a cópia que diverge. */
export const NATUREZAS: readonly Natureza[] = [
  'despesas',
  'despesas-credito',
  'despesas-debito',
  'receitas',
]

/** A allowlist, do lado de fora: `undefined` para tudo o que não for uma das
 *  quatro palavras — inclusive caixa diferente, espaço sobrando e array
 *  (`?natureza=a&natureza=b` chega como array e some inteiro).
 *
 *  Exportada pelo mesmo motivo de `tipoValido`: o `<select>` da tela valida o
 *  valor escolhido por AQUI, e não por um `includes` próprio. */
export function naturezaValida(valor: unknown): Natureza | undefined {
  return typeof valor === 'string' && (NATUREZAS as readonly string[]).includes(valor)
    ? (valor as Natureza)
    : undefined
}

/** As quatro opções do filtro de tipo, na ordem em que a tela as oferece. */
export type TipoDeLancamento = 'receitas' | 'despesas' | 'transferencias' | 'investimentos'

const TIPOS: readonly TipoDeLancamento[] = [
  'receitas',
  'despesas',
  'transferencias',
  'investimentos',
]

/** A allowlist, do lado de fora: `undefined` para tudo o que não for uma das
 *  quatro palavras.
 *
 *  Exportada porque o `<select>` da tela precisa da MESMA lista — um segundo
 *  `includes` escrito na tela seria a cópia que diverge, e é dela que sai o
 *  `?tipo=` que o portão descartaria em seguida. */
export function tipoValido(valor: unknown): TipoDeLancamento | undefined {
  return typeof valor === 'string' && (TIPOS as readonly string[]).includes(valor)
    ? (valor as TipoDeLancamento)
    : undefined
}

/** `false` nos tipos em que "só os sem categoria" não tem resultado possível.
 *
 *  Transferência não tem categoria por desenho (ADR-016) e investimento **é**
 *  definido pela categoria (ADR-029d): nos dois casos a contagem de pendência é
 *  0 por construção, e a combinação viraria uma lista vazia sem saída para
 *  quem chega por link colado. Mesma mecânica que descarta `contraparte` sem
 *  `conta` — e vale nas DUAS pontas (`validarBusca` e `aplicarNaBusca`), senão
 *  a tela escreveria na URL o que a leitura seguinte descartaria. */
function aceitaSemCategoria(tipo: TipoDeLancamento | undefined): boolean {
  return tipo !== 'transferencias' && tipo !== 'investimentos'
}

/** O único portão. Tudo o que não for reconhecido é descartado em silêncio. */
export function validarBusca(entrada: Record<string, unknown>): BuscaDoApp {
  const busca: BuscaDoApp = {}

  const mes = entrada.mes
  if (typeof mes === 'string' && mesValido(mes)) busca.mes = mes

  const natureza = naturezaValida(entrada.natureza)
  if (natureza !== undefined) busca.natureza = natureza

  const tipo = tipoValido(entrada.tipo)
  if (tipo !== undefined) busca.tipo = tipo

  const meses = tamanhoDaJanelaValido(entrada.meses)
  if (meses !== undefined) busca.meses = meses

  const conta = entrada.conta
  if (typeof conta === 'string' && uuidValido(conta)) busca.conta = conta

  const categoria = entrada.categoria
  if (typeof categoria === 'string' && uuidValido(categoria)) busca.categoria = categoria

  const contraparte = entrada.contraparte
  if (
    busca.conta !== undefined &&
    typeof contraparte === 'string' &&
    uuidValido(contraparte) &&
    contraparte !== busca.conta
  ) {
    busca.contraparte = contraparte
  }

  // Aceita as duas formas na ENTRADA porque `?semCategoria=1` digitado à mão
  // chega como número e um link antigo pode trazer a string. A saída é sempre
  // normalizada para o número.
  const semCategoria = entrada.semCategoria
  if (
    (semCategoria === 1 || semCategoria === '1') &&
    aceitaSemCategoria(busca.tipo) &&
    // "Sem categoria" dentro de UMA categoria não tem resultado possível: a
    // categoria vence e o filtro de pendência some.
    busca.categoria === undefined
  ) {
    busca.semCategoria = 1
  }

  return busca
}

/** Uma mudança na busca da URL.
 *
 *  `undefined` explícito faz parte do vocabulário: é assim que se **apaga** uma
 *  chave (desligar o filtro, voltar para todas as contas). `Partial<BuscaDoApp>`
 *  não serve com `exactOptionalPropertyTypes`, porque ali "ausente" e
 *  "`undefined`" são coisas diferentes. */
export type MudancaDeBusca = { [K in keyof BuscaDoApp]?: BuscaDoApp[K] | undefined }

/** Aplica a mudança e **omite** as chaves apagadas.
 *
 *  Materializa o `undefined` em ausência, que é o que o roteador espera: uma
 *  chave presente com valor `undefined` viraria `?conta=` vazio na URL — um
 *  filtro que não filtra nada, mas que a próxima leitura ainda tenta validar. */
export function aplicarNaBusca(anterior: BuscaDoApp, mudanca: MudancaDeBusca): BuscaDoApp {
  const proxima: BuscaDoApp = {}
  const bruta = { ...anterior, ...mudanca }

  if (bruta.mes !== undefined) proxima.mes = bruta.mes
  if (bruta.conta !== undefined) proxima.conta = bruta.conta
  // A contraparte só sobrevive com a conta: apagar `conta` apaga as duas, e
  // trocar `conta` pela própria contraparte desfaz o par em vez de mandar
  // `?conta=X&contraparte=X` para a validação descartar.
  if (
    bruta.contraparte !== undefined &&
    proxima.conta !== undefined &&
    bruta.contraparte !== proxima.conta
  ) {
    proxima.contraparte = bruta.contraparte
  }
  if (bruta.categoria !== undefined) proxima.categoria = bruta.categoria
  if (bruta.tipo !== undefined) proxima.tipo = bruta.tipo
  // O mesmo descarte da validação, do outro lado do portão: trocar o tipo para
  // transferências com o filtro de pendência ligado desliga o filtro, em vez de
  // gerar uma URL que a próxima leitura limparia. A categoria desliga pelo
  // mesmo motivo — as duas pontas combinam, senão a tela escreveria na URL o
  // que a leitura seguinte apagaria.
  if (
    bruta.semCategoria !== undefined &&
    aceitaSemCategoria(proxima.tipo) &&
    proxima.categoria === undefined
  ) {
    proxima.semCategoria = bruta.semCategoria
  }
  if (bruta.natureza !== undefined) proxima.natureza = bruta.natureza
  if (bruta.meses !== undefined) proxima.meses = bruta.meses

  return proxima
}
