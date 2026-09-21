import type { CategoryKind, Transaction } from '@/api/types'
import type { TipoDeLancamento } from '@/app/search'
import { dataCompleta } from '@/lib/civil'
import { formatarDinheiro } from '@/lib/money'

/** O léxico de `/lancamentos` — o que a tela e os componentes da feature dizem
 *  sobre uma linha, escrito uma vez.
 *
 *  Mora fora da tela porque o atalho de categorização (docs/DESIGN.md, E2c (h))
 *  nomeia o botão da célula, a legenda do editor e o `<select>` com o MESMO
 *  rótulo que o botão de excluir já usava — e um componente importando de uma
 *  tela seria a dependência ao contrário. */

/** Nome da linha para o leitor de tela: descrição, data e valor.
 *
 *  Nunca só "Excluir" ou "Sem categoria": numa tabela de 50 linhas, cinquenta
 *  botões com o mesmo nome deixam quem navega por lista de controles sem saber
 *  qual é qual — e aqui errar a linha apaga dinheiro. */
export function rotuloDaLinha(linha: Transaction): string {
  const descricao = linha.description.trim() || 'lançamento sem descrição'
  const valor = formatarDinheiro(Math.abs(linha.amountCents))
  return `${descricao}, ${dataCompleta(linha.occurredOn)}, ${valor}`
}

// ------------------------------------------- o filtro de tipo (E2d)

/** As palavras de cada opção do filtro de tipo, incluindo **Tudo**
 *  (docs/DESIGN.md, E2d (h)).
 *
 *  Uma tabela só, e não frases espalhadas pela tela, por um motivo de gênero: a
 *  mesma frase muda de artigo, de pronome e de particípio conforme a opção
 *  ("essas 3 receitas", "esses 12 lançamentos"), e a versão espalhada erra a
 *  concordância no primeiro descuido — o defeito que a T4a já corrigiu uma vez
 *  em `/investimentos`. **Tudo** entra na tabela porque as fórmulas funcionam
 *  para ele: é assim que o texto ratificado de hoje continua palavra por
 *  palavra, em vez de virar um caso especial que diverge na próxima mudança. */
type PalavrasDoTipo = {
  /** Como a opção se chama no `<select>` e no `document.title`. */
  rotulo: string
  /** "lançamento" · "receita" · "despesa" · "transferência" · "aporte ou resgate". */
  singular: string
  /** "aportes e resgates" é plural de leitura, não de contagem: em
   *  `investimentos` nunca há pendência para contar (a categoria é o que os
   *  define), então nenhuma frase escreve "12 aportes e resgates". */
  plural: string
  genero: 'm' | 'f'
  /** A linha de apoio sob o `<h1>`, emprestada da tela irmã de cada opção — é o
   *  que ensina a partição sem uma linha de prosa a mais. */
  apoio: (mes: string) => string
  /** O `caption` `sr-only` da tabela, com o particípio concordado. */
  caption: (mes: string) => string
  /** A segunda linha do estado vazio: para onde foi o que não está aqui. */
  vazio: string
}

const PALAVRAS_DO_TIPO: Record<TipoDeLancamento | 'tudo', PalavrasDoTipo> = {
  tudo: {
    rotulo: 'Tudo',
    singular: 'lançamento',
    plural: 'lançamentos',
    genero: 'm',
    apoio: (mes) => `Tudo o que entrou e saiu em ${mes}.`,
    caption: (mes) => `Lançamentos de ${mes}, agrupados por dia`,
    vazio:
      'Traga o extrato do banco — o app confere o que já existe antes de importar qualquer coisa.',
  },
  receitas: {
    rotulo: 'Receitas',
    singular: 'receita',
    plural: 'receitas',
    genero: 'f',
    apoio: (mes) => `O que entrou em ${mes}.`,
    caption: (mes) => `Receitas de ${mes}, agrupadas por dia`,
    vazio:
      'O que entra por resgate de investimento está em Investimentos, e o que vem de outra conta sua é transferência.',
  },
  despesas: {
    rotulo: 'Despesas',
    singular: 'despesa',
    plural: 'despesas',
    genero: 'f',
    apoio: (mes) => `O que saiu em ${mes}.`,
    caption: (mes) => `Despesas de ${mes}, agrupadas por dia`,
    vazio:
      'Aporte em investimento está em Investimentos, e o que foi para outra conta sua é transferência.',
  },
  transferencias: {
    rotulo: 'Transferências',
    singular: 'transferência',
    plural: 'transferências',
    genero: 'f',
    apoio: (mes) => `O que mudou de conta dentro da casa em ${mes}.`,
    caption: (mes) => `Transferências de ${mes}, agrupadas por dia`,
    vazio:
      'Transferência é o dinheiro que muda de conta dentro da casa — na importação, o app a detecta pelas palavras-chave das contas.',
  },
  investimentos: {
    rotulo: 'Investimentos',
    singular: 'aporte ou resgate',
    plural: 'aportes e resgates',
    genero: 'm',
    apoio: (mes) => `O que saiu para investir e o que voltou em ${mes}.`,
    caption: (mes) => `Aportes e resgates de ${mes}, agrupados por dia`,
    vazio: 'Um lançamento entra aqui quando recebe uma categoria de investimento ou de resgate.',
  },
}

export function palavrasDoTipo(tipo: TipoDeLancamento | undefined): PalavrasDoTipo {
  return PALAVRAS_DO_TIPO[tipo ?? 'tudo']
}

/** As opções do `<select>` **Tipo**, na ordem da faixa que elas filtram:
 *  `Entrou` vem antes de `Saiu`, e por isso Receitas vem antes de Despesas —
 *  apesar de `/relatorios/categorias` abrir em Despesas. "Tudo" não está aqui:
 *  é o `placeholder`, que é a ausência da chave na URL. */
export const OPCOES_DE_TIPO: readonly { value: TipoDeLancamento; label: string }[] = [
  { value: 'receitas', label: PALAVRAS_DO_TIPO.receitas.rotulo },
  { value: 'despesas', label: PALAVRAS_DO_TIPO.despesas.rotulo },
  { value: 'transferencias', label: PALAVRAS_DO_TIPO.transferencias.rotulo },
  { value: 'investimentos', label: PALAVRAS_DO_TIPO.investimentos.rotulo },
]

/** O prefixo do `document.title`: `Despesas · Lançamentos · HomeFinance`.
 *
 *  Em Tudo continua `Lançamentos · HomeFinance`. O sufixo `· Lançamentos` é o
 *  que distingue este título de `Transferências · HomeFinance`, que é a outra
 *  rota. */
export function tituloDoDocumento(tipo: TipoDeLancamento | undefined): string {
  if (tipo === undefined) return 'Lançamentos · HomeFinance'
  return `${palavrasDoTipo(tipo).rotulo} · Lançamentos · HomeFinance`
}

/** "Nenhum aporte ou resgate" · "Nenhuma despesa" — sem o mês, porque o vazio
 *  de dois filtros compõe com o nome da conta ("Nenhuma despesa na Nubank"). */
export function nenhumDoTipo(tipo: TipoDeLancamento | undefined): string {
  const { singular, genero } = palavrasDoTipo(tipo)
  return `${genero === 'f' ? 'Nenhuma' : 'Nenhum'} ${singular}`
}

/** "Mostrar todos os lançamentos" · "Mostrar todas as receitas".
 *
 *  Usado onde o botão limpa **só** o `semCategoria`: prometer "todos os
 *  lançamentos" numa tela que continua mostrando só despesas seria prometer
 *  mais do que o clique faz. */
export function rotuloMostrarTodos(tipo: TipoDeLancamento | undefined): string {
  const { plural, genero } = palavrasDoTipo(tipo)
  return `Mostrar ${genero === 'f' ? 'todas as' : 'todos os'} ${plural}`
}

/** A frase da faixa de pendência, que **nomeia o tipo** contado.
 *
 *  `uncategorizedCount` conta o filtro (spec 0004 §12.2) — então "3 lançamentos
 *  de setembro estão sem categoria", numa tela que mostra só receitas, é
 *  mentira sobre o mês. O fecho é o de hoje, palavra por palavra. */
export function fraseDaPendencia(
  tipo: TipoDeLancamento | undefined,
  pendentes: number,
  mes: string,
): string {
  const { singular, plural, genero } = palavrasDoTipo(tipo)
  if (pendentes === 1) {
    const ela = genero === 'f' ? 'Ela' : 'Ele'
    return `1 ${singular} de ${mes} está sem categoria. ${ela} não entra em nenhum orçamento, e nos relatórios aparece como "Sem categoria".`
  }
  const elas = genero === 'f' ? 'Elas' : 'Eles'
  return `${pendentes} ${plural} de ${mes} estão sem categoria. ${elas} não entram em nenhum orçamento, e nos relatórios aparecem como "Sem categoria".`
}

/** "Ver só essas 3" · "Ver esse lançamento" — com a concordância do tipo. */
export function rotuloVerPendentes(tipo: TipoDeLancamento | undefined, pendentes: number): string {
  const { singular, genero } = palavrasDoTipo(tipo)
  if (pendentes === 1) return `Ver ${genero === 'f' ? 'essa' : 'esse'} ${singular}`
  return `Ver só ${genero === 'f' ? 'essas' : 'esses'} ${pendentes}`
}

/** "Mostrando só as despesas sem categoria de setembro." */
export function fraseDoFiltroDePendencia(tipo: TipoDeLancamento | undefined, mes: string): string {
  const { plural, genero } = palavrasDoTipo(tipo)
  return `Mostrando só ${genero === 'f' ? 'as' : 'os'} ${plural} sem categoria de ${mes}.`
}

/** O vazio de `semCategoria`: "Todas as receitas de setembro estão
 *  categorizadas." Em Tudo continua a frase ratificada, que não tem sujeito no
 *  plural ("Tudo categorizado em setembro."). */
export function tituloTudoCategorizado(tipo: TipoDeLancamento | undefined, mes: string): string {
  if (tipo === undefined) return `Tudo categorizado em ${mes}.`
  const { plural, genero } = palavrasDoTipo(tipo)
  const todas = genero === 'f' ? 'Todas as' : 'Todos os'
  return `${todas} ${plural} de ${mes} estão categorizad${genero === 'f' ? 'a' : 'o'}s.`
}

/** A segunda frase do toast do atalho de categoria, quando a linha **sai da
 *  lista** por contradizer o filtro ativo (docs/DESIGN.md, E2d (g)).
 *
 *  Com `tipo=despesas`, categorizar uma linha como investimento a tira da tela
 *  na hora: a pessoa acabou de aprender, sem querer, que a natureza da
 *  categoria decide o tipo do lançamento. Sumir em silêncio é inaceitável — o
 *  toast continua de sucesso (nada falhou; ela fez o que quis) e ganha a
 *  explicação.
 *
 *  Vazia quando não há contradição: em Tudo, e com categoria do mesmo lado do
 *  dinheiro, o toast é o de hoje, sem acréscimo.
 *
 *  **`investimentos` entrou com a emenda §19 da spec 0005** (18/09/2026): ali
 *  toda linha é categorizada, e desde que trocar a categoria virou possível,
 *  levar um aporte para uma categoria de despesa — ou um resgate para uma de
 *  receita — também tira a linha da lista. É o mesmo molde, com o mesmo
 *  motivo; por isso ele continua sendo **um só**, sem parâmetro de modo.
 *  `transferencias` não aparece porque lá o editor não abre: perna de
 *  transferência não tem categoria por desenho. */
export function fraseDaLinhaQueSaiu(
  tipo: TipoDeLancamento | undefined,
  natureza: CategoryKind,
  sujeito: 'Ele' | 'Este lançamento',
): string {
  const movimento =
    tipo === 'despesas' && natureza === 'investment'
      ? 'um aporte'
      : tipo === 'receitas' && natureza === 'redemption'
        ? 'um resgate'
        : tipo === 'investimentos' && natureza === 'expense'
          ? 'uma despesa'
          : tipo === 'investimentos' && natureza === 'income'
            ? 'uma receita'
            : null
  if (movimento === null) return ''
  return ` ${sujeito} saiu da lista: é ${movimento}, e a lista mostra só ${palavrasDoTipo(tipo).plural}.`
}
