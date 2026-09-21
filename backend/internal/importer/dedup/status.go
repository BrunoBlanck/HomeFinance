package dedup

// Status é o veredito de uma linha na revisão (§4.6 da spec 0004).
//
// O par status + DEFAULT é o que cumpre a exigência do usuário — "nada entra
// sem a minha confirmação". O índice único do banco é a rede embaixo do
// trapézio, não o trapézio.
type Status string

const (
	// StatusNew — nenhuma colisão. Entra por default.
	StatusNew Status = "novo"

	// StatusRepeatedInFile — é a 2ª (ou enésima) ocorrência idêntica dentro do
	// PRÓPRIO arquivo, e o banco ainda não tem tantas.
	//
	// Entra por default, e é isso que impede o sistema de engolir compra
	// legítima repetida: dois cafés iguais no mesmo dia são dois gastos reais.
	// Num app de dinheiro, sumir com um lançamento é pior do que duplicar um —
	// a duplicata o usuário vê e apaga, a linha que sumiu ele descobre três
	// meses depois conferindo o extrato.
	//
	// Só a chave DERIVADA produz este status. Na chave natural não existe
	// "segunda ocorrência legítima": o identificador do emissor é único por
	// transação, então a repetição dele no arquivo é StatusDuplicateExact.
	StatusRepeatedInFile Status = "repetido_no_arquivo"

	// StatusDuplicateExact — a chave e o ordinal já existem numa linha VIVA,
	// ou a chave NATURAL já apareceu numa linha anterior do próprio arquivo
	// (neste caso sem MatchTransactionID: a gêmea ainda não está no banco, é
	// a 1ª ocorrência do arquivo, que entra no mesmo confirm).
	//
	// Barrada e NÃO liberável: liberar não adiantaria, porque o índice único
	// — ou a recusa de ordinal > 1 em chave natural, na escrita — rejeitaria o
	// INSERT de qualquer jeito. A tela mostra o lançamento existente quando
	// ele existe.
	StatusDuplicateExact Status = "duplicado_exato"

	// StatusDuplicateDeleted — a gêmea existe, mas está excluída logicamente.
	// Barrada e liberável: liberar RESTAURA a linha existente em vez de
	// inserir outra (ADR-025f). É a única saída do beco sem saída que o índice
	// único cria — sem ela, quem apagasse um lançamento importado nunca mais
	// conseguiria reimportá-lo.
	StatusDuplicateDeleted Status = "duplicado_excluido"

	// StatusPossibleDuplicate — marcação FRACA (ADR-025e): mesma conta, mesmo
	// valor, data a até WeakWindowDays de distância e descrição DIFERENTE; ou
	// external_id já usado em outra conta da casa.
	//
	// Ela cobre o buraco que a chave derivada tem: o banco que enriquece o nome
	// do estabelecimento entre dois downloads ("DL*99 RIDE" virando
	// "99 Tecnologia Ltda") geraria outra chave e passaria batido. Fica FORA do
	// índice único de propósito — heurística dentro do banco vira bloqueio que
	// ninguém consegue desfazer.
	StatusPossibleDuplicate Status = "possivel_duplicado"

	// StatusCardPayment — classificada como pagamento ou recebimento de fatura.
	// Barrada por default; a liberação oferecida é "registrar como
	// transferência para a conta X" (ADR-016).
	StatusCardPayment Status = "pagamento_de_fatura"

	// StatusInternalTransfer — a descrição bateu (≥ 80) com palavra-chave de
	// OUTRA conta ativa da casa (spec 0005 §4.2.1, ADR-026): o dinheiro não
	// saiu da casa, só mudou de bolso. Barrada por default; a liberação
	// oferecida é `transfer` (cria o par, ADR-016), além de `import` (entra
	// como receita/despesa comum) e `skip`.
	//
	// Só entra por aqui a linha cujo status de deduplicação ENTRARIA por
	// default (`novo` e `repetido_no_arquivo` — emenda §10.1 da spec): colisão
	// dura continua vencendo, e pagamento de fatura também (ele tem liberação
	// própria e mais específica).
	StatusInternalTransfer Status = "transferencia_interna"

	// StatusTransferAlreadyRegistered — a OUTRA perna já existe: um lançamento
	// transfer_* vivo da conta do lote, com contraparte igual à sugerida, mesmo
	// valor, data a ±DedupWindowDays, ainda não reivindicado por outra linha
	// deste lote. MatchTransactionID aponta para ela.
	//
	// Analyze NÃO produz este status: quem produz é o pareamento no importer,
	// que precisa das pernas carregadas do banco. Barrada; a liberação é
	// `link` — não cria movimento, só grava nesta perna a identidade de
	// deduplicação da linha, para a reimportação cair em `duplicado_exato`.
	// `import` não é oferecido: seria a duplicata que a spec 0004 impede.
	StatusTransferAlreadyRegistered Status = "transferencia_ja_registrada"

	// StatusRejected — linha malformada (valor, data ou colunas). Barrada e não
	// liberável.
	//
	// Analyze NÃO produz este status: linha rejeitada não tem valor canônico e
	// portanto não tem chave para comparar. Ela vem do parser
	// (importer.RejectedRow) e é o serviço que a grava com este status. O valor
	// mora aqui porque a taxonomia é uma só.
	StatusRejected Status = "rejeitado"
)

// Valid informa se o status está na taxonomia.
func (s Status) Valid() bool {
	switch s {
	case StatusNew, StatusRepeatedInFile, StatusDuplicateExact, StatusDuplicateDeleted,
		StatusPossibleDuplicate, StatusCardPayment, StatusInternalTransfer,
		StatusTransferAlreadyRegistered, StatusRejected:
		return true
	default:
		return false
	}
}

// DefaultImports é o DEFAULT do status: a linha entra sem nenhuma decisão da
// pessoa?
//
// Só dois status entram sozinhos. Todo o resto é barrado, e a exigência
// "linha marcada só entra com decisão explícita" é literalmente esta função
// devolvendo false.
func (s Status) DefaultImports() bool {
	switch s {
	case StatusNew, StatusRepeatedInFile:
		return true
	default:
		return false
	}
}

// Releasable informa se a pessoa PODE liberar a linha na revisão.
//
// Os dois "não" têm motivos diferentes, e vale distingui-los:
//   - StatusDuplicateExact não é liberável porque o índice único recusaria o
//     INSERT de qualquer jeito — oferecer o botão seria prometer o impossível;
//   - StatusRejected não é liberável porque não há o que inserir: a linha não
//     produziu valor, data ou descrição.
//
// Os dois status de transferência (spec 0005) são liberáveis, cada um com a
// SUA liberação: `transferencia_interna` cria o par (ou entra como comum), e
// `transferencia_ja_registrada` vincula à perna existente — nunca insere.
func (s Status) Releasable() bool {
	switch s {
	case StatusDuplicateDeleted, StatusPossibleDuplicate, StatusCardPayment,
		StatusInternalTransfer, StatusTransferAlreadyRegistered:
		return true
	default:
		return false
	}
}
