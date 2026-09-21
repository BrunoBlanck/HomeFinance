package config

import "time"

// Rule descreve um limite de taxa: Requests requisições por Window, com
// estouro (burst) igual a Requests.
type Rule struct {
	Requests int
	Window   time.Duration

	// Burst é o estouro instantâneo. Zero (o caso de quase toda regra) usa
	// Requests, que é o comportamento histórico: quem tem 60/h pode gastar os
	// 60 de uma vez.
	//
	// Ele existe para a rota CARA, em que o estouro é questão de
	// disponibilidade e não de cota: 60 execuções simultâneas de
	// POST /transfers/detect esgotariam o pool de conexões e derrubariam a API
	// inteira sem passar do teto horário (achado A2 da revisão de segurança).
	Burst int
}

// RateLimits reúne os limites da §7 da spec 0001. Todo 429 devolve
// Retry-After (docs/SEGURANCA.md §5).
type RateLimits struct {
	// Por IP.
	Global         Rule
	Login          Rule
	Register       Rule
	ResendCode     Rule
	ForgotPassword Rule
	VerifyEmail    Rule
	ResetPassword  Rule
	Refresh        Rule
	Health         Rule

	// Por conta. A chave é o HMAC do e-mail com o pepper: o e-mail em claro
	// nunca entra no mapa do limitador (§7 da spec 0001).
	LoginPerAccount          Rule
	ForgotPasswordPerAccount Rule
	// RegisterMailPerAccount é o teto de MENSAGENS de verificação disparadas
	// pelo registro, por endereço (§1.1: rate limit por IP e por conta).
	//
	// Ele limita o ENVIO, não a requisição: o e-mail do cadastro não tem
	// prova de posse, e um 429 por endereço deixaria qualquer um trancar o
	// cadastro alheio.
	//
	// INVARIANTE: Requests tem de ser MAIOR que Register.Requests (o teto por
	// IP). Ver DefaultRateLimits para o porquê — e o teste que sustenta.
	RegisterMailPerAccount Rule
	// AccountExistsNoticePerAccount é o teto do aviso "alguém tentou criar
	// conta com o seu e-mail". Conteúdo fixo: uma vez por dia basta, e mais
	// do que isso é só barulho que um terceiro escolhe mandar.
	AccountExistsNoticePerAccount Rule

	// Por CASA. A chave é o HMAC do household_id, pelo mesmo motivo do
	// e-mail: o id em claro não entra no mapa do limitador.
	//
	// A importação tem cota própria porque POST /imports é a rota mais cara do
	// sistema — ela infla, parseia e varre uma janela de deduplicação inteira.
	// O limite por casa é o que importa aqui (o arquivo é da casa); o limite
	// por IP existe ao lado dele para o caso de várias casas atrás do mesmo
	// endereço serem, na verdade, a mesma pessoa tentando.
	ImportUpload  Rule
	ImportConfirm Rule

	// ImportUploadIP é o teto por IP da mesma rota.
	ImportUploadIP Rule

	// AutoCategorize é o teto POR CASA de POST /transactions/auto-categorize
	// (spec 0005 §4.3, emenda §10.8). Mesma classe de escrita pesada do
	// confirm da importação — a rota varre até 10.000 lançamentos e escreve em
	// lote —, mas com balde PRÓPRIO: um uso legítimo gasta duas chamadas
	// (prévia com dryRun e confirmação), e dividir o balde com o confirm faria
	// uma importação grande trancar a categorização, ou o contrário.
	AutoCategorize Rule

	// TransferDetect é o teto POR CASA de POST /transfers/detect (spec 0005
	// §13.1.8, ADR-028f). Mesma classe do AutoCategorize — varre até 10.000
	// candidatas e 20.000 espelhos e escreve em lote — e pelo mesmo motivo
	// tem balde PRÓPRIO: cada uso legítimo gasta prévia + confirmação, e
	// dividir o balde com o auto-categorize faria um consertar o mês trancar
	// o outro.
	TransferDetect Rule

	// InvestmentDetect é o teto POR CASA de POST /investments/detect (spec
	// 0006 §3.3.5, ADR-029h). Mesma classe das duas acima — varre até 10.000
	// receitas e despesas do mês, pontua cada uma contra as palavras-chave da
	// casa e escreve em lote — e balde PRÓPRIO pelo mesmo motivo: cada uso
	// legítimo gasta prévia + confirmação, e dividir o balde faria uma
	// detecção trancar a outra.
	//
	// O ESTOURO importa aqui mais do que na maioria: na execução real os
	// UPDATE condicionais e a auditoria rodam DENTRO de uma transação, então
	// cada requisição em voo segura uma conexão do pool (25, por
	// DB_MAX_OPEN_CONNS). Um estouro igual à cota deixaria uma casa sozinha
	// ocupar o pool inteiro sem passar de limite nenhum — é o achado A2 da
	// revisão de segurança, e ele vale para esta rota pela mesma razão.
	InvestmentDetect Rule

	// TransactionUpdate é o teto POR CASA de PATCH /transactions/{id} (spec
	// 0005, emenda §11 — o atalho de categoria).
	//
	// Ela não é escrita em massa como as duas acima: grava UMA linha e registra
	// UM evento de auditoria por chamada. Mesmo assim ganhou balde próprio
	// porque, até 17/09/2026, o único teto sobre ela era o GLOBAL de 100/min
	// por IP — que não é por casa e não segura quem tem endereço sobrando.
	//
	// O teto é generoso de propósito: categorizar a fatura recém-importada é
	// uma RAJADA legítima — a pessoa varre a lista clicando categoria em
	// lançamento após lançamento —, e um teto apertado quebraria o uso normal.
	// Generoso, mas FINITO: 120 escritas por hora por casa cobre com folga
	// qualquer sessão de arrumação humana e ainda assim põe um teto por casa
	// em cima de um endpoint que escreve e audita a cada chamada.
	TransactionUpdate Rule

	// AiExport é o teto POR CASA de GET /ai/export-prompt (spec 0010 §8.6).
	//
	// É LEITURA pura — não escreve nada, não abre transação e não segura
	// conexão do pool dentro de uma escrita —, mas não é barata: uma chamada
	// agrega até 3 meses de lançamentos por `description_norm` (coluna sem
	// índice próprio), lê contas e categorias da casa e monta um texto grande.
	// 30/h é a mesma cota do confirm da importação, e chega para dezenas de
	// exportações por dia.
	//
	// # O estouro é 6, e não 3 — o achado A1 se repetindo no balde novo
	//
	// A tela `/ia` pede UM prompt por par (mês do cabeçalho × tamanho da
	// janela), com `staleTime: Infinity`: cada par é buscado uma vez e fica em
	// cache. Explorar os TRÊS tamanhos de janela que a spec permite (1, 2 e 3
	// meses) e depois trocar o mês do cabeçalho e explorar de novo são 3 × 2 =
	// 6 requisições, em sequência, em segundos.
	//
	// Com estouro 3 a QUARTA era 429 DETERMINÍSTICO — medido pelo `qa-testes`
	// em 21/09/2026, com `retry_after: 2m` —, e "Tentar de novo" também era
	// negado, porque 30/h repõem 1 token a cada 2 minutos e ninguém espera dois
	// minutos entre dois cliques. É literalmente o achado A1 da emenda §10 da
	// spec 0010 (que subiu AutoCategorize e TransferDetect de 3 para 6) caindo
	// de novo, agora no balde que nasceu depois dele.
	//
	// 6 é o MENOR número que faz o uso legítimo caber, não uma folga escolhida
	// a olho: com 5, a última exploração da segunda janela já era negada. A
	// cota horária de 30 fica intacta — o estouro é a rajada, não o teto —, e
	// o balde continua finito: a 7ª chamada instantânea é 429, e isso está em
	// teste. Segue 4× abaixo do pool de 25 conexões e 5× abaixo da cota.
	AiExport Rule

	// AiImportPreview é o teto POR CASA de POST /ai/keyword-import/preview
	// (spec 0010 §8.6).
	//
	// 60/h, o dobro do confirm, pelo motivo de sempre neste projeto: o uso
	// legítimo é iterativo — a pessoa cola o JSON, confere, ajusta, confere de
	// novo —, e a prévia é a metade barata do par. Barata em escrita, não em
	// CPU: ela roda o `internal/textmatch` sobre as descrições da janela para
	// medir o impacto de cada palavra-chave de conta. Daí o estouro de 3, que
	// transforma a rajada em fila sem estorvar quem confere duas ou três
	// vezes seguidas.
	AiImportPreview Rule

	// AiImportConfirm é o teto POR CASA de POST /ai/keyword-import/confirm
	// (spec 0010 §8.6).
	//
	// 30/h — metade da prévia, pela razão inversa: confirmar é o passo raro e
	// o caro. Ele revalida tudo do zero e grava categorias e palavras-chave
	// numa ÚNICA transação, então cada requisição em voo segura uma conexão do
	// pool (25, por DB_MAX_OPEN_CONNS) até terminar. O estouro de 3 é o mesmo
	// achado A2 aplicado aqui: sem ele o estouro seria a cota inteira, e uma
	// casa sozinha ocuparia o pool sem passar de limite nenhum.
	AiImportConfirm Rule

	// IdleTTL é o tempo que uma chave ociosa sobrevive no limitador, para o
	// mapa não crescer sem fim (risco 9 da §9 da spec 0001).
	IdleTTL time.Duration
}

// DefaultRateLimits devolve os padrões da §7 da spec 0001.
//
// PISO (decisão de produto de 09/09/2026):
//
//	RegisterMailPerAccount.Requests (10/h)  >  Register.Requests (5/h)
//
// ATENÇÃO: este piso é necessário, mas NÃO é suficiente — e a versão anterior
// deste comentário afirmava que era. Ver o bloco "LIMITE CONHECIDO" abaixo
// antes de confiar nele.
//
// A cota por endereço governa o ENVIO e não pode recusar a requisição (o
// e-mail vem do corpo, sem prova de posse). O efeito colateral é que um
// terceiro consegue QUEIMAR a cota do endereço alheio só registrando: com 3
// mensagens/h por endereço, ~3,7 cadastros por hora de um ÚNICO IP zeravam o
// balde e o dono do endereço não recebia mais nada — negação de cadastro por
// procuração, sem custo nenhum para o atacante.
//
// A mitigação escolhida foi a FOLGA: 10 slots por endereço em vez de 3. Isso
// ajuda o dono no caso comum.
//
// LIMITE CONHECIDO (refutado com PoC em 09/09/2026 — NÃO apague este bloco):
// um único IP AINDA drena a cota. Três rotas gastam este mesmo balde
// (canSendVerificationMail é o funil único), e a reemissão do /auth/login não
// verificado é limitada por LoginPerAccount = 5/15min = 20/h POR CONTA, que
// não limita nada num atacante de um IP só. Soma do que um IP gasta:
// Register 5/h + ResendCode 3/h + login-reissue 20/h ~= 28/h contra 10 slots.
//
// PoC: o atacante registra o endereço primeiro (a linha de users nasce com a
// senha dele) e depois só faz login a cada ~3,5 min; mediram-se 33 mensagens
// disparadas e o dono sem receber nada. Na ordem inversa o atacante queima 8
// dos 10 slots.
//
// INVARIANTE CORRETO, ainda NÃO satisfeito: cota por endereço > soma de tudo
// que um IP consegue gastar dela por hora. Backlog: (a) tirar do login não
// verificado o poder de reemitir mensagem, ou (b) sub-balde reservado para
// pedidos que não sejam login-reissue. Detalhes na §1.1 de docs/SEGURANCA.md.
//
// CUSTO: o orçamento de palpites do OTP por endereço está atado a esta cota —
// 10 códigos/h × 5 tentativas por código = 50 palpites/h contra o espaço de
// 10⁶ (antes eram 15/h). Continua folgado — da ordem de 20 mil horas para
// varrer o espaço — e está registrado na §1.1 de docs/SEGURANCA.md.
func DefaultRateLimits() RateLimits {
	return RateLimits{
		Global:         Rule{Requests: 100, Window: time.Minute},
		Login:          Rule{Requests: 10, Window: time.Minute},
		Register:       Rule{Requests: 5, Window: time.Hour},
		ResendCode:     Rule{Requests: 3, Window: time.Hour},
		ForgotPassword: Rule{Requests: 5, Window: time.Hour},
		VerifyEmail:    Rule{Requests: 10, Window: time.Minute},
		ResetPassword:  Rule{Requests: 10, Window: time.Minute},
		Refresh:        Rule{Requests: 60, Window: time.Minute},
		Health:         Rule{Requests: 60, Window: time.Minute},

		LoginPerAccount:          Rule{Requests: 5, Window: 15 * time.Minute},
		ForgotPasswordPerAccount: Rule{Requests: 3, Window: time.Hour},

		RegisterMailPerAccount:        Rule{Requests: 10, Window: time.Hour},
		AccountExistsNoticePerAccount: Rule{Requests: 1, Window: 24 * time.Hour},

		// §5.6 da spec 0004. O confirm é mais barato que o envio (ele não
		// infla nem parseia nada), mas continua sendo escrita financeira em
		// lote — daí um teto próprio em vez de nenhum.
		ImportUpload:   Rule{Requests: 10, Window: time.Hour},
		ImportUploadIP: Rule{Requests: 20, Window: time.Hour},
		ImportConfirm:  Rule{Requests: 30, Window: time.Hour},

		// Spec 0005 (emenda §10.8): 60 e não 30 porque cada uso legítimo é
		// prévia + confirmação — o dobro do confirm da importação, na mesma
		// janela, dá a mesma quantidade de USOS por hora.
		//
		// O ESTOURO é 6, igual ao TransferDetect e pelo mesmo motivo (achado
		// A2 da revisão de segurança): sem Burst, o estouro é a cota inteira —
		// 60 execuções simultâneas de uma casa só, cada uma varrendo até
		// 10.000 lançamentos e pontuando-os contra até 4.000 palavras-chave.
		//
		// POR QUE 6 E NÃO 3 (21/09/2026, decisão do usuário — achado A1 da
		// emenda §10 da spec 0010): o "Reprocessar" do menu IA roda a janela
		// de trabalho inteira, que é de até 3 MESES DE COMPETÊNCIA, mês a mês.
		// A conta é fechada: a prévia gasta 3 chamadas neste balde (uma por
		// mês, com dryRun) e a execução gasta outras 3 — 6 chamadas em
		// sequência, em segundos. O balde repõe 1 token/min, então com estouro
		// 3 a 4ª chamada tomaria 429 DETERMINÍSTICO: o botão nasceria quebrado
		// para todo mundo, e não por abuso.
		//
		// 6 é o MENOR número que faz o uso legítimo caber — não uma folga
		// escolhida a olho. Ele continua ~4× abaixo do pool de 25 conexões
		// (DB_MAX_OPEN_CONNS) e 10× abaixo da cota de 60/h, então a defesa do
		// A2 — impedir que uma casa sozinha ocupe o pool inteiro dentro da
		// cota — segue de pé.
		//
		// ⚠️ Isto AMENDA um número fixado pelo achado A2 de uma revisão de
		// segurança anterior e está PENDENTE DE RATIFICAÇÃO do
		// `revisor-seguranca`. Não mexer sem passar por lá.
		AutoCategorize: Rule{Requests: 60, Window: time.Hour, Burst: 6},

		// Spec 0005 §13.1.8: o mesmo 60/h do auto-categorize, em balde
		// separado — prévia + confirmação por uso. O ESTOURO, porém, é 6 e
		// não 60: a rota varre até 30.000 linhas e, na execução real, segura
		// uma conexão do pool em transação aberta.
		//
		// O 6 é o mesmo do AutoCategorize, pela mesma aritmética e na mesma
		// decisão de 21/09/2026: o "Reprocessar" do menu IA gasta 3 chamadas
		// aqui na prévia e 3 na execução (a janela é de até 3 meses), e o
		// balde repõe 1 token/min — com 3, a 4ª chamada era 429 garantido.
		// Menor número que acomoda o uso legítimo, ~4× abaixo do pool de 25 e
		// 10× abaixo da cota de 60/h.
		//
		// ⚠️ Amenda o achado A2 de revisão anterior; PENDENTE DE RATIFICAÇÃO
		// do `revisor-seguranca`.
		TransferDetect: Rule{Requests: 60, Window: time.Hour, Burst: 6},

		// Spec 0006 §3.3.5: o mesmo 60/h das duas irmãs, em balde separado —
		// prévia + confirmação por uso. O ESTOURO é 3 pelo motivo do campo:
		// a execução real segura conexão do pool dentro da transação, e três
		// de cada vez cobre o uso legítimo com folga para um reenvio.
		//
		// A DIVERGÊNCIA das irmãs (3 aqui, 6 nelas, desde 21/09/2026) é
		// decisão, não descuido: o "Reprocessar" do menu IA encadeia
		// `/transfers/detect` e `/transactions/auto-categorize` mês a mês na
		// janela de 3 meses, e só esses dois baldes recebem 6 chamadas em
		// sequência. `POST /investments/detect` não participa dessa
		// orquestração — continua sendo prévia + confirmação de UM mês, que
		// cabe em 3 —, e subir o estouro sem uso legítimo que o exija seria
		// afrouxar de graça.
		InvestmentDetect: Rule{Requests: 60, Window: time.Hour, Burst: 3},

		// Emenda §11: o atalho de categoria é escrita de UMA linha, mas com
		// auditoria por chamada e sem nenhum teto por casa até 17/09/2026.
		// 120/h é rajada legítima acomodada (categorizar a fatura inteira de
		// uma sentada) com teto ainda assim finito — ver o comentário do campo.
		TransactionUpdate: Rule{Requests: 120, Window: time.Hour},

		// Spec 0010 §8.6 — os três baldes do menu IA, todos POR CASA.
		//
		// Eles são SEPARADOS entre si (e dos demais) pelo motivo que já vale
		// para as rotas de detecção: exportar o prompt não pode trancar a
		// conferência do JSON, e conferir não pode trancar o confirm. A pessoa
		// usa os três na mesma sentada, em ordem, e um balde compartilhado
		// faria o passo seguinte pagar pelo anterior.
		//
		// A relação entre os três é a mesma do resto do projeto: a metade
		// barata e iterativa do par (a prévia) tem o dobro da cota da metade
		// cara (o confirm). A exportação acompanha o confirm por ser agregação
		// pesada que ninguém repete dezenas de vezes por hora.
		// AiExport com estouro 6: a tela explora os três tamanhos de janela
		// em dois meses de cabeçalho antes de a primeira reposição chegar
		// (ver o doc do campo). Os outros dois seguem em 3 — o par de
		// importação é colar/conferir/confirmar, não exploração.
		AiExport:        Rule{Requests: 30, Window: time.Hour, Burst: 6},
		AiImportPreview: Rule{Requests: 60, Window: time.Hour, Burst: 3},
		AiImportConfirm: Rule{Requests: 30, Window: time.Hour, Burst: 3},

		IdleTTL: time.Hour,
	}
}

// ---------------------------------------------------------------------------
// Perfis não-padrão: teste (17/09/2026) e desenvolvimento (18/09/2026) —
// decisões do usuário
// ---------------------------------------------------------------------------

// Perfis de limite reconhecidos por RATE_LIMITS_PROFILE.
//
// São TRÊS perfis NOMEADOS, e continua não existindo override por regra: o
// caminho pelo qual um limite frouxo vaza para produção é sempre a variável
// solta ("só esta, só agora") que alguém deixa no manifesto. Aqui o
// interruptor é único, explícito, nomeado, e a configuração AMARRA cada perfil
// não-padrão ao seu ambiente (config.validate): `test` só sobe com
// APP_ENV=test, `dev` só com APP_ENV=development, e produção recusa os dois.
const (
	// RateLimitProfileDefault são os limites de produção — o padrão quando a
	// variável não existe.
	RateLimitProfileDefault = "default"
	// RateLimitProfileTest é o perfil FROUXO da suíte automatizada. Nunca em
	// produção: o boot falha se APP_ENV=production o encontrar.
	RateLimitProfileTest = "test"
	// RateLimitProfileDev é o perfil da MÁQUINA DE DESENVOLVIMENTO (decisão do
	// usuário, 18/09/2026): os padrões de produção com os tetos da IMPORTAÇÃO
	// elevados, e mais nada. Exige APP_ENV=development; produção recusa.
	RateLimitProfileDev = "dev"
)

// testProfileRateLimits devolve o perfil FROUXO usado pela suíte automatizada.
// Não é exportado: quem precisa dele pede por NOME de perfil, em
// profileRateLimits — assim não existe um atalho para montar a config frouxa
// sem passar pela validação que a recusa em produção.
//
// POR QUE EXISTE: a suíte de ponta a ponta exercita o produto inteiro contra a
// API real, e os tetos de produção são dimensionados para uma PESSOA, não para
// um robô — 5 cadastros/h por IP e 10 importações/h por casa foram atingidos
// exatamente (5/5 e 10/10) em 09/2026, e o próximo cenário de importação não
// cabe. A alternativa (endpoint de teste que zera limites) é pior: backdoor
// que só existe para teste tem o hábito de sobreviver até produção.
//
// O QUE ELE NÃO É: um afrouxamento de produção. Os valores de
// DefaultRateLimits não mudam, e o perfil só é escolhido por um interruptor
// explícito de ambiente que produção recusa no boot.
//
// DESENHO: o perfil parte dos padrões e ELEVA os tetos, mantendo a FORMA —
// mesmas regras, mesmas janelas relativas, mesmas relações entre regras
// (`RegisterMailPerAccount > Register` continua valendo, o estouro da rota
// cara continua menor que a cota). Um perfil que zerasse os limites deixaria
// de testar o produto: o E2E precisa que o limitador esteja montado e
// funcionando, só que com folga para um robô.
func testProfileRateLimits() RateLimits {
	rl := DefaultRateLimits()

	// Por IP. O robô da suíte é UM endereço fazendo o trabalho de dezenas de
	// pessoas; o fator é o que separa "cabe a suíte" de "cabe um ataque".
	rl.Global = Rule{Requests: 3000, Window: time.Minute}
	rl.Login = Rule{Requests: 300, Window: time.Minute}
	rl.Register = Rule{Requests: 200, Window: time.Hour}
	rl.ResendCode = Rule{Requests: 200, Window: time.Hour}
	rl.ForgotPassword = Rule{Requests: 200, Window: time.Hour}
	rl.VerifyEmail = Rule{Requests: 300, Window: time.Minute}
	rl.ResetPassword = Rule{Requests: 300, Window: time.Minute}
	rl.Refresh = Rule{Requests: 300, Window: time.Minute}
	rl.Health = Rule{Requests: 600, Window: time.Minute}

	// Por conta. RegisterMailPerAccount continua ACIMA de Register: a relação
	// entre as duas regras é invariante de segurança, não número solto, e o
	// perfil de teste não pode ser o lugar onde ela se perde.
	rl.LoginPerAccount = Rule{Requests: 200, Window: 15 * time.Minute}
	rl.ForgotPasswordPerAccount = Rule{Requests: 200, Window: time.Hour}
	rl.RegisterMailPerAccount = Rule{Requests: 400, Window: time.Hour}
	rl.AccountExistsNoticePerAccount = Rule{Requests: 50, Window: 24 * time.Hour}

	// Por casa.
	rl.ImportUpload = Rule{Requests: 200, Window: time.Hour}
	rl.ImportUploadIP = Rule{Requests: 400, Window: time.Hour}
	rl.ImportConfirm = Rule{Requests: 300, Window: time.Hour}
	// O estouro sobe junto nas DUAS rotas caras (a suíte roda prévia +
	// confirmação em sequência e não pode esperar o balde de 3 se recompor),
	// mas continua MENOR que a cota: a forma da regra cara é preservada, e é
	// isso que TestRotasDeEscritaEmMassaTemEstouroMenorQueACota trava.
	rl.AutoCategorize = Rule{Requests: 600, Window: time.Hour, Burst: 30}
	rl.TransferDetect = Rule{Requests: 600, Window: time.Hour, Burst: 30}
	// InvestmentDetect acompanha as duas irmãs, e pelo MESMO motivo: o E2E
	// desta tela encadeia `dryRun: true` e confirmação, com mais de um caso
	// por arquivo, e com estouro 3 o Playwright levaria 429 intermitente —
	// o pior tipo de vermelho, o que some quando se olha.
	//
	// A diferença entre os perfis é deliberada e está do lado seguro:
	// PRODUÇÃO fica em 60/h com estouro 3 (a execução real segura conexão do
	// pool dentro da transação — achado A2), e só o perfil frouxo sobe. O
	// perfil de teste existe para a suíte exercitar a FEATURE, não o
	// limitador; o que ele não pode é perder a forma da regra, e não perde:
	// mesma janela do padrão, teto nunca menor, e 0 < Burst < Requests.
	rl.InvestmentDetect = Rule{Requests: 600, Window: time.Hour, Burst: 30}
	rl.TransactionUpdate = Rule{Requests: 1200, Window: time.Hour}

	// Menu IA (spec 0010). O E2E da tela `/ia` exporta o prompt, cola o JSON,
	// confere, desmarca uma categoria e confirma — e repete isso por cenário,
	// num robô só. Com os tetos de produção (30/h e 60/h, estouro 3) o
	// Playwright levaria 429 intermitente, que é o pior tipo de vermelho: o
	// que some quando alguém vai olhar.
	//
	// A FORMA é preservada, como nas irmãs: mesma janela do padrão, teto nunca
	// menor, estouro positivo e sempre menor que a cota, e a relação entre os
	// três mantida (a prévia continua com o dobro da cota do confirm).
	rl.AiExport = Rule{Requests: 300, Window: time.Hour, Burst: 30}
	rl.AiImportPreview = Rule{Requests: 600, Window: time.Hour, Burst: 30}
	rl.AiImportConfirm = Rule{Requests: 300, Window: time.Hour, Burst: 30}

	return rl
}

// devProfileRateLimits devolve o perfil da MÁQUINA DE DESENVOLVIMENTO: os
// padrões de produção com UM grupo elevado — os tetos da IMPORTAÇÃO (decisão
// do usuário, 18/09/2026).
//
// POR QUE EXISTE: em desenvolvimento o app roda com os limites de PRODUÇÃO (o
// perfil frouxo é da suíte e só é aceito com APP_ENV=test). Os tetos da
// importação são dimensionados para o uso real de uma casa — 10 arquivos por
// hora —, e quem está construindo ou conferindo a feature sobe o mesmo extrato
// dezenas de vezes numa tarde. O 429 aí não protege ninguém: só atrapalha o
// autor.
//
// O QUE ELE NÃO É: o perfil de teste com outro nome. Ele NÃO toca em auth, no
// global por IP, nas rotas de detecção nem em nada mais — quem depurar login,
// cadastro ou código de 6 dígitos em desenvolvimento continua batendo nos
// tetos de produção, que é exatamente onde esses limites precisam ser
// exercitados à mão. O teste que trava isso campo a campo, por reflexão, é
// TestPerfilDeDesenvolvimentoSobeSoAImportacao.
//
// DESENHO: a mesma FORMA das regras de importação, com a cota multiplicada por
// 5. Janelas idênticas às do padrão e proporções preservadas de propósito (o
// teto por IP é o dobro do teto por casa; o confirm é o triplo do upload), para
// que isto continue sendo "os mesmos limites com folga" e não um desenho
// paralelo que ninguém revisou.
//
// O ESTOURO é a parte que NÃO sobe, e é deliberado (achado F2 da revisão de
// segurança de 18/09/2026). As três regras de importação não declaram Burst no
// padrão, e Burst zero significa "estouro = cota" (ver o campo Burst): subir a
// cota para 5x sem tocar no estouro deixaria 50 uploads e 150 confirms
// SIMULTÂNEOS de uma casa só. O confirm segura uma conexão do pool dentro de
// uma transação aberta (DB_MAX_OPEN_CONNS = 25), e cada operação de importação
// empilha ~46 MiB de heap vivo (a conta está em internal/textmatch/matcher.go)
// — é a forma exata do achado A2, aplicada a rotas que ele não cobria.
//
// Então o perfil fixa o estouro NA COTA DO PADRÃO: instantaneamente, uma casa
// em desenvolvimento não consegue nada que ela já não conseguisse em produção
// (10 uploads, 20 por IP, 30 confirms de uma vez); o que muda é quantas vezes
// por hora ela pode repetir. Escrito como padrao.X.Requests e não como número
// solto, para que mexer no padrão carregue o estouro junto.
func devProfileRateLimits() RateLimits {
	padrao := DefaultRateLimits()
	rl := padrao

	const fator = 5

	rl.ImportUpload = Rule{
		Requests: fator * padrao.ImportUpload.Requests,
		Window:   padrao.ImportUpload.Window,
		Burst:    padrao.ImportUpload.Requests,
	}
	rl.ImportUploadIP = Rule{
		Requests: fator * padrao.ImportUploadIP.Requests,
		Window:   padrao.ImportUploadIP.Window,
		Burst:    padrao.ImportUploadIP.Requests,
	}
	rl.ImportConfirm = Rule{
		Requests: fator * padrao.ImportConfirm.Requests,
		Window:   padrao.ImportConfirm.Window,
		Burst:    padrao.ImportConfirm.Requests,
	}

	return rl
}

// profileRateLimits escolhe o conjunto de limites de um perfil.
//
// NÃO é exportada (achado B3 da revisão de segurança): quem está fora do
// pacote não tem por que montar um conjunto de limites por NOME DE PERFIL — o
// caminho legítimo é Load/LoadFrom, que passa pela validação que amarra cada
// perfil não-padrão ao seu ambiente (`test` a APP_ENV=test, `dev` a
// APP_ENV=development) e recusa os dois em produção. O teste alcança esta
// função por export_test.go, que só existe durante os testes deste pacote.
//
// Perfil desconhecido devolve os PADRÕES — o lado seguro do erro. Isso NÃO é a
// defesa: a validação da configuração recusa o boot com perfil desconhecido,
// para que um erro de digitação em RATE_LIMITS_PROFILE apareça como falha e
// não como comportamento silencioso.
func profileRateLimits(profile string) RateLimits {
	switch profile {
	case RateLimitProfileTest:
		return testProfileRateLimits()
	case RateLimitProfileDev:
		return devProfileRateLimits()
	default:
		return DefaultRateLimits()
	}
}
