/** Endereços e caminhos compartilhados entre a configuração do Playwright, o
 *  global setup/teardown e os testes.
 *
 *  As portas são fixas e diferentes das de desenvolvimento (backend 8085,
 *  frontend 5173) de propósito: rodar a suíte E2E não pode derrubar nem
 *  conversar com o ambiente que você deixou aberto na outra janela. */

import { join, resolve } from 'node:path'

/** Lê uma porta do ambiente, caindo no padrão quando ausente ou inválida.
 *
 *  As portas continuam FIXAS por padrão (8099/5199), que é o que separa a
 *  suíte E2E do ambiente de desenvolvimento. O override por env existe por um
 *  motivo concreto de QA: `reuseExistingServer: false` + `--strictPort` fazem
 *  duas execuções simultâneas se matarem — e o `globalTeardown` de uma derruba
 *  o servidor da outra no meio do teste, com `ERR_CONNECTION_REFUSED`. Com
 *  `E2E_PORTA_WEB`/`E2E_PORTA_API`/`E2E_DIR_EXECUCAO` é possível rodar uma
 *  segunda suíte em paralelo sem tocar na primeira. */
function porta(nome: string, padrao: number): number {
  const bruto = process.env[nome]
  if (!bruto) return padrao
  const n = Number.parseInt(bruto, 10)
  return Number.isInteger(n) && n > 0 && n < 65_536 ? n : padrao
}

export const PORTA_API = porta('E2E_PORTA_API', 8099)
export const PORTA_WEB = porta('E2E_PORTA_WEB', 5199)

export const BASE_URL = `http://127.0.0.1:${PORTA_WEB}`
export const API_URL = `http://127.0.0.1:${PORTA_API}`

/** Diretório de trabalho da execução: banco SQLite descartável, binário
 *  compilado e a caixa de entrada do mailer de console.
 *
 *  Fica **dentro do projeto** (`frontend/.playwright/`, já no `.gitignore`), e
 *  não em `os.tmpdir()`. O motivo é concreto: um caminho fixo e previsível
 *  dentro de um diretório temporário compartilhado — `/tmp/homefinance-e2e` no
 *  Linux — é criado pelo primeiro que chegar. Outro usuário da máquina pode
 *  deixar ali um link simbólico ou um diretório que ele consiga ler, e a
 *  execução seguinte escreve o log de e-mails (com os códigos de 6 dígitos) e o
 *  banco SQLite dentro dele. O diretório do projeto herda a permissão do
 *  projeto e não tem esse problema. */
export const DIR_EXECUCAO = resolve(
  process.cwd(),
  '.playwright',
  process.env.E2E_DIR_EXECUCAO || 'execucao',
)

/** Onde a saída do backend é gravada. O mailer de console imprime o e-mail
 *  inteiro no stdout — é dali que o teste tira o código de 6 dígitos, sem
 *  precisar de nenhum endpoint de teste no servidor. */
export const ARQUIVO_SAIDA = join(DIR_EXECUCAO, 'backend.log')

/** Estado que o global setup grava e o teardown lê (o PID a matar). */
export const ARQUIVO_ESTADO = join(DIR_EXECUCAO, 'estado.json')

/** Sessão compartilhada pelos testes de aplicação (cookies gravados pelo
 *  projeto `setup`). Ver `e2e/sessao.setup.ts` para o porquê de ser
 *  compartilhada em vez de uma casa por teste. */
export const ARQUIVO_SESSAO = join(DIR_EXECUCAO, 'sessao.json')
