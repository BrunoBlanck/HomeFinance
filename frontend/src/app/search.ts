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
  /** Natureza do relatório por categoria (`/relatorios/categorias`).
   *
   *  Allowlist de duas palavras em pt-BR — a URL é interface. Ausente ou fora
   *  da lista, a tela cai em despesas: um `?natureza=tudo` colado no chat abre
   *  o relatório de gastos, não uma tela de erro. A tradução para o `kind` da
   *  API (`expense`/`income`) é da tela, nunca da URL. */
  natureza?: Natureza
}

export type Natureza = 'despesas' | 'receitas'

const NATUREZAS: readonly Natureza[] = ['despesas', 'receitas']

/** O único portão. Tudo o que não for reconhecido é descartado em silêncio. */
export function validarBusca(entrada: Record<string, unknown>): BuscaDoApp {
  const busca: BuscaDoApp = {}

  const mes = entrada.mes
  if (typeof mes === 'string' && mesValido(mes)) busca.mes = mes

  const natureza = entrada.natureza
  if (typeof natureza === 'string' && (NATUREZAS as readonly string[]).includes(natureza)) {
    busca.natureza = natureza as Natureza
  }

  const conta = entrada.conta
  if (typeof conta === 'string' && uuidValido(conta)) busca.conta = conta

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
  if (semCategoria === 1 || semCategoria === '1') busca.semCategoria = 1

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
  if (bruta.semCategoria !== undefined) proxima.semCategoria = bruta.semCategoria
  if (bruta.natureza !== undefined) proxima.natureza = bruta.natureza

  return proxima
}
