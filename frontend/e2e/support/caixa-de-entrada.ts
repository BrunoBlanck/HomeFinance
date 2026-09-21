/** Leitura da "caixa de entrada" do mailer de console.
 *
 *  O backend imprime cada e-mail inteiro no stdout, que o global setup
 *  redireciona para um arquivo. Daqui saem os códigos de 6 dígitos sem que o
 *  servidor precise expor nenhuma rota de teste.
 *
 *  O envio é **assíncrono** (fila com drenagem no shutdown), então o e-mail não
 *  está no arquivo no instante em que a resposta HTTP chega: por isso a espera
 *  é ativa, com prazo. */

import { readFileSync } from 'node:fs'
import { ARQUIVO_SAIDA } from './ambiente'

function conteudo(): string {
  try {
    return readFileSync(ARQUIVO_SAIDA, 'utf8')
  } catch {
    return ''
  }
}

/** Todos os blocos de e-mail já impressos, na ordem em que saíram. */
function blocos(texto: string): string[] {
  return texto
    .split('=========== E-MAIL (mailer de desenvolvimento) ===========')
    .slice(1)
    .map((bloco) => bloco.split('==========================================================')[0] ?? '')
}

/** Espera o e-mail mais recente para `email` e devolve o código de 6 dígitos.
 *
 *  `depoisDe` permite ignorar mensagens anteriores: num reenvio, o arquivo tem
 *  dois e-mails para o mesmo endereço e usar o antigo faria o teste falhar de
 *  um jeito confuso. Passe o total devolvido por `totalDeEmails()` antes da
 *  ação que dispara o novo envio. */
export async function esperarCodigo(
  email: string,
  opcoes: { depoisDe?: number; prazoMs?: number } = {},
): Promise<string> {
  const depoisDe = opcoes.depoisDe ?? 0
  const prazoMs = opcoes.prazoMs ?? 15_000
  const limite = Date.now() + prazoMs

  while (Date.now() < limite) {
    const paraOEmail = blocos(conteudo()).filter((bloco) => bloco.includes(`<${email}>`))
    if (paraOEmail.length > depoisDe) {
      const bloco = paraOEmail[paraOEmail.length - 1] ?? ''
      // O corpo traz o código sozinho numa linha indentada; a âncora de linha
      // evita casar com qualquer outro número que apareça no texto.
      const achado = bloco.match(/^\s*(\d{6})\s*$/m)
      if (achado?.[1]) return achado[1]
    }
    await new Promise((r) => setTimeout(r, 200))
  }

  throw new Error(
    `Nenhum código de 6 dígitos para ${email} apareceu em ${prazoMs} ms. ` +
      `Confira ${ARQUIVO_SAIDA}.`,
  )
}

/** Quantos e-mails já foram enviados para o endereço. Use antes de disparar um
 *  reenvio, e passe o resultado como `depoisDe`. */
export function totalDeEmails(email: string): number {
  return blocos(conteudo()).filter((bloco) => bloco.includes(`<${email}>`)).length
}
