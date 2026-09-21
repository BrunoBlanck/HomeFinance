import type { TamanhoDaJanela } from '@/app/search'
import { nomeDoMes, somarMeses } from '@/lib/month'

/** A janela de trabalho de `/ia` — **uma função só**, e é esta.
 *
 *  A spec 0010 §2.1 exige um conceito único de período para as TRÊS seções
 *  (exportar, importar e reprocessar): é o período exportado, o período medido
 *  na prévia do impacto e o período reprocessado. Nenhuma seção deriva datas
 *  por conta própria — três derivações seriam três janelas que se contradizem
 *  no dia em que uma delas esquecer a virada de ano.
 *
 *  A janela é de **competência**, em meses civis (`YearMonth`), nunca em datas:
 *  as rotas de reprocessamento só falam mês (emenda §10, achado A2), e "3 meses
 *  a partir do dia 15" não tem resposta. Ela **termina** no mês escolhido no
 *  alto da página e anda para trás. */
export type JanelaDeTrabalho = {
  /** Primeiro mês, inclusive — `AAAA-MM`. */
  fromMonth: string
  /** Último mês, inclusive. É o mês da casca (`?mes=`). */
  toMonth: string
  /** Todos os meses da janela, do mais antigo ao mais novo. É esta lista que a
   *  seção Reprocessar vai percorrer, mês a mês (spec 0010 §5.2). */
  meses: readonly string[]
}

/** Deriva a janela a partir do mês da casca e do tamanho escolhido.
 *
 *  A aritmética de mês é a de `lib/month` (`somarMeses`), que soma sobre os
 *  próprios números — sem `Date`, sem fuso e sem horário de verão no caminho.
 *  É o que faz a virada de ano ser uma identidade e não um caso especial:
 *  `janelaDeTrabalho('2026-01', 3)` é novembro, dezembro e janeiro. */
export function janelaDeTrabalho(mes: string, meses: TamanhoDaJanela): JanelaDeTrabalho {
  const lista: string[] = []
  for (let recuo = meses - 1; recuo >= 0; recuo -= 1) lista.push(somarMeses(mes, -recuo))
  return { fromMonth: lista[0] ?? mes, toMonth: mes, meses: lista }
}

/** `julho a setembro` · `agosto e setembro` · `setembro`.
 *
 *  Uma frase só, usada na opção do seletor, no aviso de carregamento e em
 *  qualquer lugar que precise dizer o período por extenso. Escrita duas vezes,
 *  ela divergiria — e o seletor passaria a prometer um período diferente do que
 *  o painel está montando. */
export function rotuloDaJanela(janela: JanelaDeTrabalho): string {
  const nomes = janela.meses.map(nomeDoMes)
  const primeiro = nomes[0] ?? ''
  const ultimo = nomes[nomes.length - 1] ?? ''
  if (nomes.length === 1) return primeiro
  if (nomes.length === 2) return `${primeiro} e ${ultimo}`
  return `${primeiro} a ${ultimo}`
}

/** `julho, agosto e setembro` · `agosto e setembro` · `setembro`.
 *
 *  A ENUMERAÇÃO dos meses, e não o intervalo de `rotuloDaJanela`: a seção
 *  Reprocessar percorre os meses um a um, e a frase precisa nomear cada um —
 *  "Reprocessar julho, agosto e setembro" diz o que vai ser feito; "julho a
 *  setembro" diria só o período. Sai em minúsculas, como `nomeDoMes`; quem
 *  começa frase com ela usa `capitalizar`. */
export function enumerarMeses(meses: readonly string[]): string {
  const nomes = meses.map(nomeDoMes)
  if (nomes.length <= 1) return nomes[0] ?? ''
  return `${nomes.slice(0, -1).join(', ')} e ${nomes[nomes.length - 1] ?? ''}`
}

/** `julho, agosto e setembro` → `Julho, agosto e setembro`. Só a primeira
 *  letra: os nomes de mês seguem minúsculos no meio da frase, como em todo o
 *  app. */
export function capitalizar(texto: string): string {
  return texto.charAt(0).toUpperCase() + texto.slice(1)
}

/** `homefinance-prompt-2026-07-a-2026-09.md`.
 *
 *  Só mês, dígito e hífen entram no nome: ele vem da janela derivada aqui, não
 *  de texto do servidor nem da URL, então não há como um nome de arquivo
 *  carregar caminho, aspas ou quebra de linha. */
export function nomeDoArquivoDoPrompt(janela: JanelaDeTrabalho): string {
  return `homefinance-prompt-${janela.fromMonth}-a-${janela.toMonth}.md`
}

/** Quantas linhas o texto tem — o número que a tela mostra duas vezes (na linha
 *  de estatísticas e no `<summary>` do `<details>`), e que por isso é contado
 *  **uma** vez.
 *
 *  A quebra final não conta como linha: um arquivo que termina em `\n` tem o
 *  mesmo número de linhas que o mesmo arquivo sem ela, e `split` diria uma a
 *  mais. */
export function contarLinhas(texto: string): number {
  if (texto === '') return 0
  const semQuebraFinal = texto.endsWith('\n') ? texto.slice(0, -1) : texto
  return semQuebraFinal.split('\n').length
}
