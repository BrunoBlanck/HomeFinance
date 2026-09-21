package transaction

import "context"

// UsageChecker responde "esta conta/categoria já foi usada?" para os serviços
// de conta e de categoria.
//
// É o verificador que a spec 0003 §1 deixou declarado como dívida da E1 e que
// esta entrega paga: com ele registrado, DELETE /accounts/{id} e
// DELETE /categories/{id} com lançamento associado passam a responder 422
// RESOURCE_IN_USE em vez de excluir e deixar lançamento pendurado em conta que
// não existe mais.
//
// Um tipo só para as duas perguntas porque a resposta vem da mesma tabela; os
// dois serviços consomem interfaces diferentes (account.UsageChecker e
// category.UsageChecker), e este tipo satisfaz as duas sem que nenhum dos três
// pacotes precise conhecer os outros.
type UsageChecker struct{ repo Repository }

// NewUsageChecker monta o verificador.
func NewUsageChecker(repo Repository) UsageChecker { return UsageChecker{repo: repo} }

// AccountInUse responde ao serviço de contas.
//
// ⚠️ CRITÉRIO, e ele é deliberado: conta com lançamento EXCLUÍDO LOGICAMENTE
// continua "em uso". A pergunta que o 422 responde é "isto já foi usado?", e
// um lançamento excluído foi.
//
// A alternativa — contar só os vivos, deixando excluir a conta de quem apagou
// todos os lançamentos — foi avaliada e RECUSADA por dois motivos concretos,
// não por conservadorismo:
//
//  1. a restauração existe (ADR-025f). Apagar os lançamentos, apagar a conta e
//     depois reimportar o arquivo restauraria lançamentos apontando para uma
//     conta excluída: dinheiro vivo pendurado em conta que sumiu da listagem,
//     com saldo que não aparece em lugar nenhum. O critério frouxo criaria o
//     estado inconsistente justamente pelo caminho que esta entrega abre;
//  2. a linha excluída continua ocupando (dedup_key, dedup_ordinal). O
//     histórico dela é real e referencia a conta; a conta é o rótulo que torna
//     esse histórico legível.
//
// A parede que isso cria TEM saída, e ela é a operação certa: **arquivar**.
// Arquivar tira a conta das telas e do seletor, preserva o histórico e é
// reversível — é exatamente o que "não uso mais esta conta" quer dizer. Excluir
// é para a conta criada por engano, que nunca teve lançamento nenhum. Quem
// escrever a mensagem do 422 na interface precisa dizer isso em português:
// "esta conta tem lançamentos; arquive-a em vez de excluir".
func (u UsageChecker) AccountInUse(ctx context.Context, householdID, accountID string) (bool, error) {
	return u.repo.ExistsByAccount(ctx, householdID, accountID)
}

// CategoryInUse responde ao serviço de categorias, pelo mesmo critério e pelo
// mesmo motivo — inclusive o da restauração, que devolve o lançamento com a
// categoria original.
func (u UsageChecker) CategoryInUse(ctx context.Context, householdID, categoryID string) (bool, error) {
	return u.repo.ExistsByCategory(ctx, householdID, categoryID)
}
