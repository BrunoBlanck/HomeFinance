# Segurança — HomeFinance

Este documento é o **checklist obrigatório** do agente `revisor-seguranca` e a referência de implementação dos devs. Nenhuma entrega é concluída sem revisão aprovada contra este documento.

## Modelo de ameaças

**O que protegemos:** dados financeiros de famílias (transações, saldos, contas, hábitos de consumo), credenciais e a integridade dos registros.

**De quem:**
1. Atacante externo não autenticado (internet).
2. Usuário autenticado tentando acessar dados de **outra casa** (a ameaça mais provável — IDOR).
3. Membro malicioso da própria casa excedendo seu papel.
4. Vazamento indireto: logs, mensagens de erro, backups, dependências comprometidas.

## 1. Autenticação

- Senhas: **Argon2id** (mínimo: memory 64 MiB, iterations 3, parallelism 2, salt 16B, key 32B). Nunca MD5/SHA/bcrypt-custo-baixo.
- Login: mensagem de erro idêntica para "usuário não existe" e "senha errada"; comparações em tempo constante; rate limit por IP + por conta.
- Sessão: JWT **access curto (10–15 min)** + **refresh opaco (14 dias)** — token aleatório ≥ 128 bits, **não JWT**, armazenado somente como hash — com **rotação obrigatória**: refresh usado é invalidado; reuso de refresh revogado ⇒ derruba a família inteira de tokens (detecção de roubo).
- Tokens no cliente: **cookies `HttpOnly; Secure; SameSite=Strict`** — nunca `localStorage`/`sessionStorage`.
- **Exceção única e fechada (09/09/2026): o `registrationToken` do ADR-014.** Vale **só** para ele; qualquer token de sessão, access ou refresh continua proibido no storage do navegador, e citar este precedente para outro token é violação. Por que a exceção se sustenta: o `registrationToken` **não é credencial de sessão** — sozinho não autentica ninguém nem abre dado algum, só serve junto do código de 6 dígitos que foi para a caixa de entrada; o servidor o guarda apenas como hash SHA-256; é de uso único e morre na verificação. Fica em `sessionStorage` (chave `hf.registration`), **amarrado ao endereço que o pediu** — o par `{token, email}`, porque o servidor busca a tentativa por `(email, hash do token)` e um token de outro endereço tem de valer o mesmo que token nenhum. Apagado na verificação concluída e no login bem-sucedido. **Trade-off registrado:** um cookie `HttpOnly; Path=/api/v1/auth` seria imune à leitura *e* à plantação por XSS, mas perderia o isolamento por aba (dois cadastros em duas abas se atropelariam) e sobreviveria ao fechamento da aba; sob XSS nesta origem o código digitado no `CodeInput` já é legível, então guardar o token não amplia a superfície deste fluxo. Implementação e justificativa completa: `frontend/src/features/auth/storage/registrationToken.ts` e spec 0002 §3.1.
- JWT: biblioteca **`golang-jwt/jwt/v5`** (⚠️ `dgrijalva/jwt-go` está abandonada — proibida); algoritmo fixo no validador (rejeitar `none`/mudança de alg), `exp`/`iat`/`iss`/`aud` verificados, segredo ≥ 256 bits vindo de env.
- Evolução planejada: **passkeys (WebAuthn)** via `go-webauthn/webauthn` como método preferencial de login (ver roadmap).

## 1.1 Verificação de e-mail e códigos de 6 dígitos (OTP) — ADR-009

Decisão do usuário (09/09/2026): **nenhuma conta existe sem e-mail verificado**, e todo fluxo sensível de conta (cadastro, recuperação de senha) se confirma com um **código numérico de 6 dígitos** enviado por e-mail. Um código de 6 dígitos tem só ~20 bits de entropia — a segurança vem inteira das regras abaixo, que são **obrigatórias, não opcionais**:

- **Geração:** `crypto/rand` **sempre** (`math/rand` é proibido), com distribuição uniforme sobre `000000–999999` (rejeitar o viés de módulo). O código pode ter zeros à esquerda — trate como **string de 6 caracteres**, nunca como inteiro.
- **Armazenamento:** guarde **apenas o hash HMAC-SHA-256** do código, com um segredo de servidor (pepper) vindo de env. Nunca o código em texto, nem em banco, nem em log, nem em resposta de API. Sem o pepper, um vazamento do banco não permite força bruta offline dos 10⁶ valores.
- **Comparação:** tempo constante (`hmac.Equal`/`subtle.ConstantTimeCompare`).
- **Expiração curta:** 10–15 minutos. Código expirado é indistinguível de código errado na resposta.
- **Uso único:** consumido na primeira validação bem-sucedida (`consumed_at`). Emitir um código novo **invalida todos os anteriores** do mesmo propósito.
- **Limite de tentativas:** máximo 5 tentativas por código; ao estourar, o código é queimado e o usuário precisa pedir outro. Isso é o que impede a força bruta online dos 10⁶ valores.
- **Rate limit** no envio e na validação, por **IP e por conta** — inclusive para impedir uso do endpoint de reenvio como amplificador de e-mail (bombing).
- **Não revelar existência de conta:** `POST /auth/forgot-password` responde **sempre** igual ("se o e-mail estiver cadastrado, enviamos um código"), com o mesmo status e tempo de resposta aproximado, exista o e-mail ou não. Vale também para o reenvio.
- **Escopo da tentativa (ADR-014, obrigatório):** no cadastro, o código pertence à **tentativa** que o pediu, nunca ao endereço. `POST /auth/register` devolve um `registrationToken` opaco (≥128 bits de `crypto/rand`, guardado **só como hash SHA-256**), e `POST /auth/verify-email` **exige** esse token junto do código: a busca é escopada pelo token, e um código de outra tentativa **não existe** para efeito de validação. É o que fecha o *pre-hijacking* de conta — o código de quem tenta se antecipar chega à caixa do titular, mas é inútil sem o token de quem o pediu. A confirmação ativa as credenciais **da tentativa dona do código** e destrói todas as outras tentativas pendentes daquele endereço. Corolário: "emitir um código novo invalida os anteriores" vale **por tentativa**, e nenhum pedido de cadastro pode destruir ou bloquear o de outra pessoa (senão a defesa vira negação de cadastro por procuração).
- **Só é validável o código EFETIVAMENTE ENVIADO (ADR-014, revisão de 09/09/2026):** a tentativa de cadastro é gravada com código mesmo quando o cooldown ou a cota seguram a mensagem — é o que preserva o token de quem pediu o cadastro —, e nesse estado o código **não valida nada** (`code_issued_at IS NOT NULL` entra no `WHERE` da consulta **e** é reconferido no serviço). Sem isso, um endpoint sem teto por endereço (`register`, que não pode ter um, senão vira *lockout* por procuração) fabrica códigos chutáveis **sem gastar mensagem**, e a cota por endereço deixa de limitar quantos códigos existem para adivinhar — que é metade da defesa dos 20 bits do código de 6 dígitos. Regra geral: **todo alvo de força bruta tem de custar um slot de cota** — o literal é a **cota**, não a mensagem, porque a fila pode descartar uma mensagem já contabilizada. Orçamento resultante: **50 palpites/hora/endereço** (10 códigos × 5 tentativas por código) contra o espaço de 10⁶ — da ordem de 20 mil horas para varrer.

    A sentinela de "nunca emitido" é **NULO** (`code_issued_at`), nunca o zero de `time.Time`: o driver do MySQL serializa esse zero como `'0000-00-00 00:00:00'` e o `sql_mode` padrão do MySQL 8 (`STRICT_TRANS_TABLES` + `NO_ZERO_DATE`) recusa o INSERT com erro 1292 — a tentativa não nasceria, o `register` responderia 500 só nos caminhos "e-mail livre"/"e-mail pendente" (oráculo de enumeração) e o invariante do ADR-014 cairia. **Regra: estado ausente é NULL em toda coluna de data; data mágica não é portátil.**
- **Reenvio exige prova de posse da tentativa:** `POST /auth/resend-code` só emite para quem apresenta o `registrationToken`. Quem pede reenvio **não fornece credenciais**, então qualquer código emitido sem o token herdaria as credenciais de outra tentativa — reabrindo o *pre-hijacking* pela porta dos fundos. Não vale restringir a "endereço com uma única tentativa viva": o atacante fabrica essa condição sozinho (basta a tentativa da vítima expirar e a dele estar viva). A recuperação de quem perdeu o token é **refazer o cadastro**. O mesmo princípio vale para a reemissão do login não verificado, que só rotaciona a tentativa cujas credenciais correspondem à senha recém-provada.
- **Negação de cadastro por procuração — mitigação por FOLGA de cota (decisão do usuário, 09/09/2026):** a cota de mensagens por endereço governa o **envio** e não pode recusar a requisição (o e-mail vem do corpo, sem prova de posse), então um terceiro consegue **queimá-la só registrando**. Com 3 mensagens/h por endereço contra um teto de 5 cadastros/h por IP, **um único IP** drenava o balde (~3,7 cadastros/h bastavam) e o dono do endereço não recebia mais nenhum código. A mitigação escolhida foi subir `RegisterMailPerAccount` de 3/h para **10/h**, dando mais folga ao dono no caso comum.

    ⚠️ **CORREÇÃO DE 09/09/2026 — a versão anterior deste parágrafo afirmava que "um IP sozinho já não drena o balde e sobram ≥ 5 slots para o dono". Isso é FALSO e foi refutado com PoC executado.** O raciocínio original comparou a cota por endereço só com o teto de `Register` por IP (5/h), mas **três rotas gastam o mesmo balde** (é a regra "teto por mensagem, não por rota", logo abaixo), e uma delas — a reemissão do `/auth/login` não verificado — é limitada por `LoginPerAccount`, que é **5 por 15 min = 20/h e por CONTA, não por IP**. Somando o que um único IP consegue gastar da cota: `Register` 5/h + `ResendCode` 3/h + login-reissue 20/h ≈ **28/h contra 10 slots**. Um IP ainda drena.
    PoC: o atacante registra o endereço primeiro (a linha de `users` nasce com a senha dele), depois só faz `POST /auth/login` com a própria senha a cada ~3,5 min; cada login queima um slot. Resultado medido: **33 mensagens disparadas, dono não recebeu nenhum código**, nem pelo cadastro nem pelo reenvio com o token dele. Na ordem inversa (dono registra primeiro) o atacante ainda queima **8 dos 10 slots**, sobrando 2 — não os "≥ 5" prometidos.
    **Invariante correto, ainda NÃO satisfeito:** cota por endereço **>** soma de tudo que um único IP consegue gastar dela por hora. Caminhos para fechar, em backlog: (a) parar de deixar o login não verificado reemitir mensagem — é conveniência, não recuperação, já que quem tem o token usa `resend-code`; ou (b) reservar um sub-balde de envio para pedidos que não sejam login-reissue. Enquanto isso não acontece, **a folga de 10/h é uma atenuação parcial, não uma garantia**. O custo colateral está registrado acima: o orçamento de palpites por endereço subiu de 15/h para 50/h.
- **Teto de envio é por MENSAGEM, não por rota:** toda mensagem de verificação — cadastro, reenvio e reemissão do login não verificado — passa pelo **mesmo** cooldown e pela **mesma** cota por endereço, com a mesma chave. Um teto por endpoint é contornável trocando de rota.
- **Janela de rate limit vs. TTL do balde:** o limitador varre chaves ociosas, e um balde apagado **renasce cheio**. O TTL efetivo tem de ser `max(idleTTL, window)` — senão uma cota de "1 por 24 h" vira "1 por hora". Garantido em `httpserver.NewLimiter` e coberto por teste de invariante sobre `config.DefaultRateLimits()`.
- **Escopo do código:** o `purpose` (`email_verification` | `password_reset`) faz parte do que é validado — um código de verificação de e-mail **nunca** pode ser usado para trocar senha.
- **Após redefinir a senha:** revogar **todos** os refresh tokens do usuário (todas as famílias) — troca de senha derruba todas as sessões.
- **Envio:** pela interface `Mailer`. A implementação de desenvolvimento escreve o código no log e **não pode existir em produção** — a config falha no boot se `APP_ENV=production` com o mailer de console.

## 2. Autorização — BOLA, o risco nº 1 do OWASP API Security Top 10 (regra de ouro do projeto)

- Todo dado pertence a uma **household**. O `household_id` da operação vem **sempre do token/sessão — nunca do corpo, query ou path da request**.
- Toda query de leitura/escrita filtra por `household_id`: `WHERE id = ? AND household_id = ?`. Buscar por ID e checar depois é proibido (janela de TOCTOU e risco de esquecer).
- Recurso de outra casa ⇒ **404** (não 403 — não vazar existência).
- Papéis: `owner` gerencia membros e configurações; `member` opera lançamentos. Checagem de papel no service, não no handler.

## 3. Entrada e injeção

- SQL **100% parametrizado**. Concatenação de entrada em SQL é proibida em qualquer circunstância, inclusive `ORDER BY`, `LIMIT`, nomes de coluna — para ordenação/filtro dinâmico, **allowlist** de colunas mapeada em código.
- **Com GORM (ADR-008):** o construtor de queries já parametriza — mas `Raw`, `Exec`, `Where` com string interpolada, `Order`/`Select`/`Table` com entrada do usuário **continuam sendo injeção**. Use sempre placeholders `?`; ordenação dinâmica só por allowlist. `Raw`/`Exec` exigem justificativa na revisão.
- Validação na borda (handler): tipo, tamanho máximo, faixa, formato. Corpos com `http.MaxBytesReader` (padrão 1 MiB). Decoder JSON com `DisallowUnknownFields`.
- Dinheiro: `int64` centavos; rejeitar valores absurdos (> R$ 1 bilhão) e negativos onde não fazem sentido.
- Uploads (futuros: comprovantes): validar tipo real (magic bytes), tamanho, nome gerado pelo servidor, armazenar fora da árvore servida.

## 4. Vazamento de informação

- Cliente recebe só o erro padrão da API com mensagem genérica; stack trace/detalhe interno **nunca** sai na resposta.
- Logs (`log/slog`, estruturado): **nunca** senha, token, hash, cookie, corpo completo de request. IDs sim, conteúdo sensível não.
- Respostas serializam structs de resposta dedicadas — nunca a entidade do banco direto (evita vazar campo novo por acidente).

## 5. Transporte e headers

- TLS obrigatório em produção; HSTS.
- Headers em toda resposta: `X-Content-Type-Options: nosniff`, `Content-Security-Policy` restritiva, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`.
- CORS: origem exata do frontend via config — nunca `*` com credenciais.
- Rate limiting global e por rota sensível (login, refresh, criação de conta) — `golang.org/x/time/rate`, por IP e por conta.

### 5.1 Limites de abuso — baldes por rota

Todo teto vive em `config.DefaultRateLimits()` e é aplicado como middleware na **tabela de rotas** (`cmd/api/routes.go`), nunca dentro de um handler: limitador escondido no handler não aparece na tabela, não é revisável e não é testável junto com os outros. Todo 429 devolve `Retry-After` e corpo genérico — sem casa, sem id, sem nome de rota.

A chave de um balde **por casa** é o **HMAC do `household_id`** com o pepper da aplicação, pelo mesmo motivo do e-mail: o id em claro nunca entra no mapa do limitador.

**`PATCH /transactions/{id}` tem balde próprio desde 17/09/2026** (decisão do usuário; emenda §11 da spec 0005): **120/h por casa**. Antes, o único teto sobre ele era o global de 100/min por IP — que não é por casa. A rota grava uma linha **e escreve uma linha de `audit_log` a cada chamada**, então sem teto por casa quem tivesse endereços sobrando enchia a auditoria de uma casa de graça. O número é generoso de propósito (categorizar a fatura recém-importada é uma **rajada legítima**: a pessoa varre a lista clicando categoria em lançamento após lançamento) e ainda assim **finito**.

### 5.2 Perfil de limites por ambiente — `RATE_LIMITS_PROFILE`

Decisão do usuário (17/09/2026). A suíte de ponta a ponta bate nos tetos reais — 5 cadastros/h por IP e 10 importações/h por casa foram atingidos **exatamente** (5/5 e 10/10) em 09/2026, e o próximo cenário de importação não cabe. A saída é um **perfil**, não um afrouxamento:

| Valor | Efeito |
|---|---|
| `default` (padrão, e o que vale sem a variável) | `config.DefaultRateLimits()` — os limites de produção, byte a byte. |
| `test` | `ProfileRateLimits("test")` — o conjunto **inteiro** substituído por tetos altos para robô. |

**Desenho, e por que é este:**

- **Um interruptor só, de conjunto.** Não existe — e não pode passar a existir — override por regra (`RATE_LIMIT_IMPORT_UPLOAD=9999`). Uma variável solta por limite é indistinguível de configuração legítima num manifesto e é exatamente o caminho pelo qual um teto frouxo vaza para produção sem aparecer em revisão nenhuma. Teste que trava isso: `TestSemOInterruptorOAmbienteNaoMudaNenhumLimite`.
- **Produção recusa.** Com `APP_ENV=production` e `RATE_LIMITS_PROFILE=test` a configuração **falha no boot**, com a mesma força de `MAILER=console` em produção. O valor é normalizado (caixa e espaços) antes da comparação, então `TEST`, ` test ` e `Test` caem na mesma trava.
- **Perfil desconhecido é falha, não silêncio.** `RATE_LIMITS_PROFILE=testing` não "cai no padrão": o boot morre. Quem digitou errado quis afrouxar e precisa descobrir que não afrouxou.
- **O boot avisa em `Warn`.** Com o perfil frouxo ativo, a API loga `"perfil de limites de abuso FROUXO ativo — use apenas em teste automatizado, NUNCA em produção"`, e `rate_limits_profile` passa a sair em toda linha que loga a configuração.
- **Frouxo não é "sem limite".** O perfil de teste **eleva tetos** mantendo a forma das regras: mesmas janelas, todo limite finito, `RegisterMailPerAccount > Register` preservado (é o invariante da negação de cadastro por procuração) e o estouro da rota cara continua menor que a cota (achado A2). Coberto por `TestPerfilDeTesteMantemAFormaDasRegras`.
- **Os valores de produção não mudaram.** `TestSemVariavelDePerfilOsLimitesSaoOsPadroes` compara o conjunto inteiro com `DefaultRateLimits()` em `development` e em `production`.

**Uso:** só a suíte automatizada, exportando `RATE_LIMITS_PROFILE=test` no processo da API de teste (o E2E sobe a API em `frontend/e2e/support/global-setup.ts`, com `APP_ENV=development`). Nenhum manifesto de produção contém essa variável — e se contiver, o serviço não sobe.

## 6. Frontend

- Nunca `dangerouslySetInnerHTML` com dado de usuário; nunca montar HTML/URL por concatenação de entrada.
- Dados sensíveis nunca em query string (vazam em logs/histórico).
- Dependências mínimas; `npm audit` limpo antes de entregar.

## 7. Segredos e configuração

- Segredos **somente** via variáveis de ambiente; `.env` no `.gitignore`; `.env.example` só com chaves e placeholders.
- Nenhuma credencial em código, teste, fixture, log ou histórico git.
- Config falha rápido: segredo obrigatório ausente ⇒ app não sobe.

## 8. Dependências e build

- Toda dependência nova: justificativa (por que a stdlib não basta) + checagem de manutenção/reputação.
- `govulncheck ./...` (vulnerabilidades alcançáveis) + `gosec ./...` (SAST) no Go e `npm audit` no front, limpos antes de cada entrega; rodar na revisão de segurança e no CI.
- Versões pinadas (`go.sum`, `package-lock.json` versionados).

## 8.1 Achados aceitos (revisitar a cada auditoria)

Registro de achados de ferramenta que foram analisados e **conscientemente aceitos**, para não serem relitigados a cada revisão. Um achado só entra aqui com justificativa verificada; sair daqui exige nova análise.

| Achado | Módulo | Análise | Situação |
|---|---|---|---|
| **GO-2026-5932** | `golang.org/x/crypto` | Afeta `golang.org/x/crypto/openpgp` e subpacotes. **Não usamos nenhum deles** — deste módulo o projeto importa apenas `argon2`. O `govulncheck` confirma que não é alcançável ("your code doesn't appear to call these"). Verificado em 09/09/2026 na base oficial: **não existe versão corrigida e nunca existirá** — o pacote foi declarado "unsafe by design, not maintained" pelos próprios mantenedores, que recomendam migrar para `github.com/ProtonMail/go-crypto/openpgp`. Atualizar `x/crypto` **não** zera este achado. | **Aceito.** Reavaliar apenas se o projeto passar a importar OpenPGP — e nesse caso usar o fork da ProtonMail, nunca o pacote da x/crypto. |

## 9. Auditoria

- Tabela `audit_log`: quem, o quê, quando, de qual IP — para login, mudança de senha, gestão de membros e exclusões. Sem dados sensíveis no detalhe.

## Severidade dos achados

| Severidade | Exemplos | Efeito |
|------------|----------|--------|
| **Crítica** | IDOR entre casas, SQL injection, segredo commitado, bypass de auth | BLOQUEIA entrega |
| **Alta** | Token em localStorage, senha em log, CORS `*`, sem rate limit no login | BLOQUEIA entrega |
| **Média** | Header de segurança ausente, validação frouxa sem exploração direta | Corrigir no ciclo |
| **Baixa** | Melhoria defensiva, hardening adicional | Backlog |
