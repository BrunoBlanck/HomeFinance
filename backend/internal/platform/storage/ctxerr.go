package storage

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// Prazo e cancelamento: fazer `errors.Is` voltar a funcionar quando o driver
// INTERROMPE o comando.
//
// # O problema, medido e não suposto
//
// `database/sql` devolve `ctx.Err()` em dois momentos — antes de pegar a
// conexão, e quando o `Rows` de uma LEITURA é fechado pelo contexto. A ESCRITA
// não tem `Rows`: no driver puro-Go (glebarez/go-sqlite sobre
// modernc.org/sqlite) o `stmt.exec` instala um `interruptOnDone` e, quando o
// contexto morre com o comando em voo, o `sqlite3_step` volta com
// `SQLITE_INTERRUPT` — e é ESSE erro que sobe, não o do contexto.
//
// Medido nesta máquina, com a tabela povoada (sem linhas o SQLite nem avalia a
// condição, e o comando volta em 1 ms sem erro — foi essa a primeira medição,
// enganosa, e é por isso que o teste de regressão insere linhas antes):
//
//	UPDATE lento, prazo de 80 ms, fora de transação  → "interrupted (9)"
//	UPDATE lento, prazo de 80 ms, dentro de sql.Tx   → "interrupted (9)"
//
// Em nenhum dos dois `errors.Is(err, context.DeadlineExceeded)` é verdadeiro.
//
// A consequência é toda na borda: o handler que traduz prazo em 422 e
// cancelamento em log silencioso deixa de reconhecer os dois, a pessoa recebe
// 500 INTERNAL_ERROR por um limite de trabalho previsto, e cada aba fechada no
// meio de uma escrita vira uma linha de ERROR — sinal de segurança poluído por
// comportamento normal de cliente.
//
// # Onde a correção mora, e por quê
//
// Aqui, ao lado de `gorm.Config.TranslateError`: este é o lugar onde o projeto
// já converte erro de DRIVER em erro que o resto do código entende. A raiz não
// é de nenhum repositório — é do driver —, então a cura é registrada uma vez, na
// abertura da conexão, e vale para toda consulta e toda escrita, dentro ou fora
// de transação, nos quatro dialetos.
//
// A pergunta que decide o embrulho é `ctx.Err() != nil`, e nunca o TEXTO do
// erro do driver: comparar mensagem por string faria a correção valer só no
// SQLite e falhar calada nos outros três.

// ComErroDeContexto liga err ao motivo do contexto quando o contexto morreu e o
// erro não o embrulha.
//
// Três cuidados, cada um deliberado:
//
//   - `err == nil` continua nil. Contexto morto com comando bem-sucedido não
//     inventa falha — quem decide o que fazer com o resultado é quem chamou;
//   - o erro ORIGINAL continua na cadeia (`%w` duplo): gorm.ErrDuplicatedKey,
//     gorm.ErrRecordNotFound e qualquer sentinela de domínio continuam
//     reconhecíveis por `errors.Is` depois do embrulho. A informação é somada,
//     nunca trocada;
//   - erro que JÁ embrulha o motivo passa intacto, para o caso comum (leitura
//     interrompida) não ganhar uma segunda camada sem conteúdo.
func ComErroDeContexto(ctx context.Context, err error) error {
	if err == nil || ctx == nil {
		return err
	}
	motivo := ctx.Err()
	if motivo == nil || errors.Is(err, motivo) {
		return err
	}
	// O erro do driver vem PRIMEIRO na mensagem porque é ele que diz o que
	// aconteceu no banco; o motivo do contexto entra em seguida, e é dele que a
	// borda precisa para traduzir prazo e cancelamento.
	return fmt.Errorf("%w (contexto: %w)", err, motivo)
}

// instalarErroDeContexto registra, UMA vez por conexão, o embrulho acima ao fim
// dos processadores do GORM por onde o código desta aplicação emite SQL.
//
// "After" e não "Before": o erro só existe depois que o comando rodou.
//
// # Por que o processador Raw fica de fora, e por que isso não é um buraco
//
// O processador Raw do GORM serve exclusivamente aos dois métodos de SQL cru —
// e os dois são PROIBIDOS no código de produção deste projeto, com gate
// automatizado (TestSemSQLMontadoNoGormstore, critério de aceite 6 da spec
// 0001). Nenhum caminho de requisição passa por ele: o que resta ali é o SQL
// que o próprio GORM emite no AutoMigrate, que roda no boot, com o contexto de
// inicialização, e cujo erro já é fatal por outro caminho.
//
// Registrar ali exigiria escrever, neste arquivo, exatamente o texto que aquele
// gate procura. Entre driblar o gate por formatação e afrouxar a expressão
// dele, a terceira saída é a honesta: não registrar onde a aplicação não passa,
// e deixar o motivo escrito.
func instalarErroDeContexto(gdb *gorm.DB) error {
	const nome = "homefinance:erro_de_contexto"

	embrulhar := func(db *gorm.DB) {
		if db == nil || db.Error == nil || db.Statement == nil {
			return
		}
		db.Error = ComErroDeContexto(db.Statement.Context, db.Error)
	}

	cb := gdb.Callback()
	registros := map[string]func(func(*gorm.DB)) error{
		"create": func(fn func(*gorm.DB)) error { return cb.Create().After("gorm:create").Register(nome, fn) },
		"query":  func(fn func(*gorm.DB)) error { return cb.Query().After("gorm:query").Register(nome, fn) },
		"update": func(fn func(*gorm.DB)) error { return cb.Update().After("gorm:update").Register(nome, fn) },
		"delete": func(fn func(*gorm.DB)) error { return cb.Delete().After("gorm:delete").Register(nome, fn) },
		"row":    func(fn func(*gorm.DB)) error { return cb.Row().After("gorm:row").Register(nome, fn) },
	}
	for qual, registrar := range registros {
		if err := registrar(embrulhar); err != nil {
			return fmt.Errorf("registrando tradução de prazo em %s: %w", qual, err)
		}
	}
	return nil
}
