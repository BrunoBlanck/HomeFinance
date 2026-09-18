/** Sobe a API Go de verdade para os testes de ponta a ponta.
 *
 *  Por que backend real e não `page.route` com respostas de mentira: o que este
 *  fluxo precisa provar mora justamente no que um mock apagaria — cookie
 *  `HttpOnly` indo e voltando, `SameSite=Strict`, checagem de `Origin`, rotação
 *  de refresh, e o código de 6 dígitos que só existe porque o servidor o gerou.
 *  Um E2E que finge o servidor testa o desenho da tela, não o produto.
 *
 *  Como o código chega ao teste: com `MAILER=console` o backend imprime o
 *  e-mail inteiro no stdout (é uma decisão registrada no próprio
 *  `mailer/console.go`, e a configuração recusa esse mailer em produção).
 *  Redirecionamos o stdout para um arquivo e o teste lê o código dali. Nenhum
 *  endpoint de teste é adicionado ao servidor — backdoor que só existe para
 *  teste tem o hábito de sobreviver até produção. */

import { spawn } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { mkdirSync, openSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import {
  API_URL,
  ARQUIVO_ESTADO,
  ARQUIVO_SAIDA,
  BASE_URL,
  DIR_EXECUCAO,
  PORTA_API,
} from './ambiente'

const RAIZ_BACKEND = resolve(process.cwd(), '..', 'backend')

async function esperarPronto(url: string, tentativas = 120): Promise<void> {
  for (let i = 0; i < tentativas; i++) {
    try {
      const resposta = await fetch(url)
      if (resposta.ok) return
    } catch {
      // Ainda subindo.
    }
    await new Promise((r) => setTimeout(r, 500))
  }
  throw new Error(`API não respondeu em ${url} depois de ${tentativas} tentativas`)
}

export default async function globalSetup(): Promise<void> {
  // Cada execução começa do zero: banco de uma corrida não pode contaminar a
  // seguinte, e o log de e-mails precisa estar vazio para o teste não achar o
  // código de um cadastro antigo.
  rmSync(DIR_EXECUCAO, { recursive: true, force: true })
  mkdirSync(DIR_EXECUCAO, { recursive: true })

  const binario = join(DIR_EXECUCAO, process.platform === 'win32' ? 'api.exe' : 'api')

  // Compilar antes e rodar o binário — em vez de `go run` — para que exista um
  // único processo para matar no teardown. Com `go run`, o servidor é neto do
  // processo que iniciamos e sobrevive ao kill do pai no Windows.
  await new Promise<void>((resolver, rejeitar) => {
    const build = spawn('go', ['build', '-o', binario, './cmd/api'], {
      cwd: RAIZ_BACKEND,
      stdio: 'inherit',
      shell: process.platform === 'win32',
    })
    build.on('error', rejeitar)
    build.on('exit', (codigo) =>
      codigo === 0 ? resolver() : rejeitar(new Error(`go build falhou (exit ${codigo})`)),
    )
  })

  const saida = openSync(ARQUIVO_SAIDA, 'a')

  const servidor = spawn(binario, {
    cwd: DIR_EXECUCAO,
    stdio: ['ignore', saida, saida],
    env: {
      ...process.env,
      // APP_ENV=test é EXIGÊNCIA do perfil frouxo abaixo, não preferência: a
      // config só aceita RATE_LIMITS_PROFILE=test neste ambiente, e recusa o
      // boot em qualquer outro (docs/SEGURANCA.md §5.2). Foi assim que o
      // revisor fechou o caminho "esqueci APP_ENV=production e subi com os
      // tetos de teste".
      APP_ENV: 'test',
      // Perfil FROUXO de limites de abuso — existe só para a suíte.
      RATE_LIMITS_PROFILE: 'test',
      HTTP_ADDR: `127.0.0.1:${PORTA_API}`,
      // Segredos novos a cada execução: nenhum valor de teste fica no
      // repositório e nenhuma sessão sobrevive entre corridas.
      JWT_SECRET: randomBytes(48).toString('base64'),
      OTP_PEPPER: randomBytes(48).toString('base64'),
      DB_DRIVER: 'sqlite',
      DB_DSN: join(DIR_EXECUCAO, 'e2e.db'),
      DB_AUTOMIGRATE: 'true',
      MAILER: 'console',
      // O navegador fala com o Vite, que faz proxy para cá mantendo a origem;
      // a allowlist precisa conter essa origem para o CORS e para a checagem
      // de `Origin` que sustenta a defesa de CSRF (ADR-013).
      CORS_ORIGIN: BASE_URL,
      COOKIE_SECURE: 'false',
      LOG_LEVEL: 'info',
      LOG_FORMAT: 'text',
      // Os parâmetros do Argon2 ficam nos padrões de propósito. A primeira
      // versão deste arquivo os rebaixava para a suíte correr mais rápido, e a
      // validação de boot recusou ("ARGON2_MEMORY_KIB deve ser >= 65536") —
      // corretamente. O piso é um mínimo de segurança, não uma sugestão, e o
      // E2E não é lugar de abrir exceção para ele: um ambiente de teste que
      // roda com hash mais fraco que produção deixa de testar produção.
      //
      // A normalização do tempo de resposta é o único ajuste, e ela é
      // permitida pela configuração (0 a 5 s). Ela existe para fechar o
      // oráculo de enumeração por cronômetro, que é uma propriedade do
      // servidor exercitada nos testes de unidade do backend — aqui ela só
      // somaria 300 ms a cada chamada de autenticação.
      AUTH_MIN_RESPONSE_TIME: '0s',
    },
  })

  servidor.unref()

  writeFileSync(
    ARQUIVO_ESTADO,
    JSON.stringify({ pid: servidor.pid, binario, iniciadoEm: new Date().toISOString() }, null, 2),
  )

  await esperarPronto(`${API_URL}/api/v1/health/ready`)

  // O perfil pedido acima precisa ter sido de fato ADOTADO, e não só pedido.
  // Sem esta conferência, apagar a variável (ou o backend deixar de lê-la)
  // devolve a suíte aos tetos de produção e a falha aparece minutos depois,
  // como um "Muitas tentativas" no meio de um teste de importação — que é
  // exatamente o defeito que este arquivo existe para não ter. O backend loga
  // o perfil em toda linha que loga a configuração (`config.LogValue`), então
  // a prova está no log que já redirecionamos.
  const log = readFileSync(ARQUIVO_SAIDA, 'utf8')
  if (!/\brate_limits_profile=test\b/.test(log)) {
    throw new Error(
      'A API de teste NÃO subiu com RATE_LIMITS_PROFILE=test. Com os tetos de ' +
        `produção a suíte perde requisições para o limitador. Log em ${ARQUIVO_SAIDA}`,
    )
  }
}
