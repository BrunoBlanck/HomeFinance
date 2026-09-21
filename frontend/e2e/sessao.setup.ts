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
 *  isolamento ARITMÉTICO — afirmar o total de um mês ou de um ano sem somar o
 *  que o vizinho deixou — cadastra a sua (ver `investimentos.spec.ts` e
 *  `filtro-de-tipo.spec.ts`).
 *
 *  ## ⚠️ Esta casa nasce COM a semente de categorias (ADR-033, 18/09/2026)
 *
 *  Casa nova — esta e qualquer outra — nasce com **15 grupos, 41 subcategorias
 *  e as palavras-chave de fábrica**. (A contagem de palavras não é repetida
 *  aqui nem em spec nenhum: ela muda com a lista de
 *  `backend/internal/category/seed.go` — foram 483, 445, 441 e 440 em três
 *  dias — e quem a trava é o teste de tabela do pacote `category`. O que a
 *  suíte E2E afirma é a ESTRUTURA, que é estável.) Isso muda duas premissas
 *  que vários
 *  specs traziam do tempo em que a semente era só de grupos vazios, e as duas
 *  valem para TODO spec deste diretório, com casa própria ou não:
 *
 *  1. **Não existe mais casa sem palavra-chave.** Cadastrar uma conta nova não
 *     isola nada nesse eixo: a semente vem junto. Uma descrição de fixture que
 *     precise entrar SEM categoria tem de ser um nome que a semente não
 *     reconheça — inventado, não uma loja real. É por isso que as fixtures
 *     dizem `ZUMBRA`, `KREVOL`, `PLINTAQ`, `VRANDIX`, `TARVIN`, `GLIMPO`,
 *     `DORNEK` e `MULFAZ` em vez de `MERCADO`, `PADARIA` ou `POSTO`: cada um
 *     desses nomes foi conferido contra o motor real (`internal/textmatch` com
 *     a lista de `internal/category/seed.go`) e não alcança o limiar de 80 em
 *     nenhum dos dois lados do dinheiro. Nome novo de fixture passa pela mesma
 *     conferência antes de entrar.
 *  2. **Grupo da semente não é folha nem destino selecionável.** Os 14 grupos
 *     com filhas viram `<optgroup>` no `<select>` de categoria (só as folhas
 *     são opção) e escondem o campo de palavras-chave no diálogo (spec 0005
 *     §12/§13). Spec que precise de um destino selecionável, ou de um dono de
 *     palavra-chave, **cria um grupo próprio sem filhas** (`QA Feira`,
 *     `QA Trajeto`…) em vez de usar `Alimentação`, `Saúde` ou `Moradia`.
 *
 *  Desligar a semente em teste foi proposto e **recusado** (plano da semente,
 *  §15): um interruptor de ambiente faria o E2E provar coisas sobre uma casa
 *  que não existe em produção, e apagaria justamente a cobertura do maior
 *  risco da feature — uma palavra de fábrica categorizando em silêncio.
 *  `semente-de-categorias.spec.ts` é o spec que guarda esse comportamento.
 *
 *  A consequência é que os testes de aplicação **compartilham a mesma casa** e
 *  rodam em série, cada um usando nomes próprios. Está dito no cabeçalho de
 *  cada spec que depende disso. */
setup('cria a casa compartilhada dos testes de aplicação', async ({ page }) => {
  await entrarComContaNova(page, 'Bruno Blanck')
  await page.context().storageState({ path: ARQUIVO_SESSAO })
})
