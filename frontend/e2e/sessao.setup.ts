import { test as setup } from '@playwright/test'
import { ARQUIVO_SESSAO } from './support/ambiente'
import { entrarComContaNova } from './support/sessao'

/** Cria UMA casa e grava a sessão em disco, para os testes de aplicação
 *  reaproveitarem.
 *
 *  **Por que compartilhar em vez de uma casa por teste.** Até 17/09/2026 era
 *  imposição: `POST /auth/register` é limitado a **5 por hora por IP** e a
 *  suíte gastava exatamente esses cinco — três nos testes de autenticação
 *  (onde cadastrar É o assunto), um aqui e um na casa própria de
 *  `atalho-de-categoria.spec.ts`. Não sobrava nenhum.
 *
 *  Hoje é ESCOLHA. O `global-setup.ts` sobe a API de teste com
 *  `RATE_LIMITS_PROFILE=test` (decisão do usuário, 17/09/2026 —
 *  `docs/SEGURANCA.md` §5.2), um perfil que troca o conjunto inteiro de
 *  limites por tetos de robô, e o teto de cadastro deixou de apertar.
 *
 *  **Atenção ao que isso NÃO significa.** Os limites continuam valendo em
 *  produção, byte a byte: o perfil é um interruptor único, de conjunto, e a
 *  configuração só o ACEITA com `APP_ENV=test` e recusa o boot em qualquer
 *  outro ambiente, produção incluída. Não existe
 *  variável por limite, e afrouxar produção continua exigindo mudança de
 *  código revisada.
 *
 *  A casa segue compartilhada porque isso é bom para o teste, não porque é
 *  obrigatório: uma casa só mantém a suíte rápida e obriga cada spec a usar
 *  nomes próprios em vez de depender do estado inicial. Quem precisa de
 *  isolamento de verdade — uma casa SEM palavra-chave nenhuma, por exemplo —
 *  cadastra a sua (ver `atalho-de-categoria.spec.ts`).
 *
 *  A consequência é que os testes de aplicação **compartilham a mesma casa** e
 *  rodam em série, cada um usando nomes próprios. Está dito no cabeçalho de
 *  cada spec que depende disso. */
setup('cria a casa compartilhada dos testes de aplicação', async ({ page }) => {
  await entrarComContaNova(page, 'Bruno Blanck')
  await page.context().storageState({ path: ARQUIVO_SESSAO })
})
