package importer_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A CONTRAPARTE: o único id de conta que vem no CORPO do confirm.
//
// `id_canonico_test.go` cobre o `accountId` do LOTE, que chega pelo multipart
// da fase 1. O `counterpartAccountId` é outro caminho, com outras defesas, e é
// o que este arquivo cobre (achado de severidade média do revisor sobre o
// delta da E6b — ADR-035):
//
//   - a FORMA é conferida na borda do confirm (handler.Confirm), porque o
//     valor vira `WHERE id = ?` sem trim e sem teto de tamanho;
//   - a CAIXA TROCADA é forma canônica VÁLIDA e passa pela borda de propósito:
//     quem a fecha é `conferirContrapartes` (que devolve, para cada id pedido,
//     o id que o banco reconheceu) somada a `canonizarContas` (que troca o
//     pedido pelo canônico e RECONFERE que o par não ficou com as duas pernas
//     na mesma conta).
//
// O cenário concreto que a segunda defesa trava: num dialeto de collation
// frouxa, `counterpartAccountId = "<UUID-DO-LOTE-EM-MAIÚSCULAS>"` passa por
// `conferirDecisoes` (as strings são diferentes em Go) e, sem a reconferência,
// viraria um par com as duas pernas na MESMA conta — pego só lá adiante, por
// outra sentinela e com outra mensagem. O que estes testes travam é a
// RESPOSTA, que é o contrato.
//
// O dublê de collation frouxa é o `contasComColacaoFrouxa` do harness
// (`a.contas.frouxa`): ele casa o id como MySQL 8 e MSSQL casariam, SEMPRE
// dentro da mesma casa — afrouxar a comparação do id nunca afrouxa o escopo
// por household.

// Ids escolhidos a dedo, ricos em letras hexadecimais. Sem letra, "caixa
// trocada" não existiria como caso (ToUpper seria a identidade) e o teste
// passaria sem exercitar nada.
const (
	idDoLote        = "00000000-0000-7000-baba-0000000000a1"
	idDoCartao      = "00000000-0000-7000-abba-0000000000b2"
	idDaContaAlheia = "00000000-0000-7000-adda-0000000000c3"
)

// As mensagens contratuais de `fields.counterpartAccountId`. São o que
// distingue uma recusa da outra, e por isso estão literais aqui: importar a
// constante da produção deixaria a mudança de resposta passar em silêncio.
const (
	msgContaDiferente = "Escolha uma conta diferente da conta do arquivo." // ErrSameAccountTransfer
	msgFormaDaBorda   = "Escolha a conta da outra perna."                  // recusa de FORMA, na borda
	msgSemContraparte = "Escolha a conta da outra perna da transferência." // ErrCounterpartRequired
	msgParQuebrado    = "Não consegui montar o par da transferência."      // transaction.ErrBrokenTransfer
)

// contaComId cria uma conta com um id canônico ESCOLHIDO, na casa indicada.
func (a *ambiente) contaComId(t *testing.T, escolhido, householdID, nome, kind, instituicao string) *account.Account {
	t.Helper()
	require.True(t, id.IsCanonical(escolhido), "o id do próprio teste tem de ser canônico")

	nomeLimpo, norm, err := account.NormalizeName(nome)
	require.NoError(t, err)

	agora := a.relogio.now()
	c := &account.Account{
		ID:          escolhido,
		HouseholdID: householdID,
		Name:        nomeLimpo,
		NameNorm:    norm,
		Kind:        kind,
		Institution: instituicao,
		OpeningDate: civil.MustNew(2026, 1, 1),
		CreatedAt:   agora,
		UpdatedAt:   agora,
	}
	require.NoError(t, a.repoConta.Create(t.Context(), c))
	return c
}

// loteComPagamentoDeFatura monta o cenário comum: o extrato importado e a
// linha `pagamento_de_fatura` — a única do arquivo que oferece a ação
// `transfer` sem depender de palavra-chave nenhuma.
func loteComPagamentoDeFatura(t *testing.T, a *ambiente, conta *account.Account) (importer.BatchView, importer.RowView) {
	t.Helper()
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU_2026-08.csv", fixtureExtrato(t)))
	linha := linhaComDescricao(t, revisarPelaAPI(t, a, lote.ID).Items, "Pagamento de fatura")
	return lote, linha
}

// corpoTransfer monta o confirm de UMA decisão `transfer`.
func corpoTransfer(rowID, contraparte string) string {
	return fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`,
		rowID, contraparte)
}

// confirmarComLog roda o confirm por um handler com o LOG capturado.
//
// A recusa da borda não pode ecoar o valor recusado em lugar nenhum (S8) — e
// "lugar nenhum" inclui o log, que é onde um id de 10 kB ou com quebra de
// linha faria estrago sem nunca aparecer na resposta.
func confirmarComLog(t *testing.T, a *ambiente, loteID, corpoJSON string) (*httptest.ResponseRecorder, string) {
	t.Helper()

	var registrado bytes.Buffer
	h := importer.NewHandler(a.svc,
		slog.New(slog.NewJSONHandler(&registrado, &slog.HandlerOptions{Level: slog.LevelDebug})), 0)

	r := requisicao(t, http.MethodPost, "/api/v1/imports/"+loteID+"/confirm",
		strings.NewReader(corpoJSON), identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", loteID)

	rec := httptest.NewRecorder()
	h.Confirm(rec, r)
	return rec, registrado.String()
}

// camposDo400 exige 400 VALIDATION_FAILED e devolve o `fields`.
func camposDo400(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Equal(t, httpserver.CodeValidationFailed, env.Error.Code, rec.Body.String())
	return env.Error.Fields
}

// A contraparte que CANONIZA para a conta do lote é recusada com o erro
// contratual do campo — e não com o erro genérico do par, lá adiante.
//
// ⚠️ Este é o teste que trava a refatoração perigosa. Sem o laço de
// `p.contrapartes` em `canonizarContas`, a primeira metade do método ainda
// canonizaria as PERNAS, as duas ficariam na mesma conta e quem barraria seria
// `validarParesCanonicos`, dentro do CreateBatch — outra sentinela
// (transaction.ErrBrokenTransfer) e outra mensagem no mesmo campo. O usuário
// leria "não consegui montar o par" no lugar de "escolha uma conta diferente",
// e a suíte inteira continuaria verde.
func TestConfirmRecusaContraparteQueCanonizaParaAContaDoLote(t *testing.T) {
	t.Parallel()

	t.Run("pela API: 400 com a mensagem de conta igual, nunca a do par quebrado", func(t *testing.T) {
		t.Parallel()

		a := novoAmbiente(t)
		conta := a.contaComId(t, idDoLote, a.casa.ID, "Nubank Conta", account.KindChecking, "nubank")
		lote, pagamento := loteComPagamentoDeFatura(t, a, conta)

		// A collation frouxa é ligada DEPOIS da fase 1: o defeito que este
		// teste vigia mora no confirm, e a fase 1 tem teste próprio.
		a.contas.frouxa.Store(true)

		pedido := strings.ToUpper(conta.ID)
		require.NotEqual(t, conta.ID, pedido, "sem letra hexadecimal no id não haveria caso")
		require.True(t, id.IsCanonical(pedido),
			"a caixa trocada é forma canônica VÁLIDA — a borda deixa passar de propósito")

		rec := confirmarPelaAPI(t, a, lote.ID, corpoTransfer(pagamento.ID, pedido))

		campos := camposDo400(t, rec)
		assert.Equal(t, msgContaDiferente, campos["counterpartAccountId"])
		assert.NotEqual(t, msgParQuebrado, campos["counterpartAccountId"],
			"o par quebrado do CreateBatch é a resposta ERRADA para este pedido")
		assert.NotContains(t, campos, "accountId", "o campo recusado é o que está no corpo")
		assert.NotContains(t, rec.Body.String(), pedido, "a recusa nunca ecoa o valor recebido")

		// Tudo ou nada: nada gravado, lote ainda pendente.
		assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
		assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)
	})

	t.Run("no serviço: a sentinela é ErrSameAccountTransfer, nunca ErrBrokenTransfer", func(t *testing.T) {
		t.Parallel()

		a := novoAmbiente(t)
		conta := a.contaComId(t, idDoLote, a.casa.ID, "Nubank Conta", account.KindChecking, "nubank")
		lote, pagamento := loteComPagamentoDeFatura(t, a, conta)
		a.contas.frouxa.Store(true)

		// As duas grafias que os dialetos frouxos casam com a conta do lote. A
		// segunda (espaço à direita) a borda do HTTP já recusa por forma; no
		// serviço ela tem de ser recusada do mesmo jeito, porque a defesa não
		// pode depender de quem chamou.
		grafias := map[string]string{
			"caixa trocada (MySQL 8, utf8mb4_0900_ai_ci)": strings.ToUpper(conta.ID),
			"espaço à direita (MSSQL, padding ANSI)":      conta.ID + "   ",
		}
		for nome, pedido := range grafias {
			t.Run(nome, func(t *testing.T) {
				_, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
					Decisions: []importer.Decision{{
						RowID:                pagamento.ID,
						Action:               importer.ActionTransfer,
						CounterpartAccountID: pedido,
					}},
				})
				require.ErrorIs(t, err, importer.ErrSameAccountTransfer)
				require.NotErrorIs(t, err, transaction.ErrBrokenTransfer,
					"o par quebrado significa que a reconferência da canonização não aconteceu")
				assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
			})
		}
	})
}

// A borda do confirm recusa o `counterpartAccountId` fora da forma canônica
// ANTES de ele virar `WHERE id = ?`.
//
// Vazio NÃO entra nesta lista, e isso é desenho: `counterpartAccountId` vazio
// significa "use a contraparte que a análise sugeriu". Quem o recusa, quando
// não há sugestão, é o SERVIÇO — mesmo campo, outra mensagem (subteste no
// fim).
func TestBordaDoConfirmRecusaCounterpartAccountIdForaDaFormaCanonica(t *testing.T) {
	t.Parallel()

	a := novoAmbiente(t)
	conta := a.contaComId(t, idDoLote, a.casa.ID, "Nubank Conta", account.KindChecking, "nubank")
	cartao := a.contaComId(t, idDoCartao, a.casa.ID, "Nubank Cartão", account.KindCreditCard, "nubank")
	lote, pagamento := loteComPagamentoDeFatura(t, a, conta)

	// Ligada de propósito: a recusa é de FORMA e acontece antes de qualquer
	// consulta, então o dialeto não pode mudar a resposta.
	a.contas.frouxa.Store(true)

	malformados := map[string]string{
		"id curto":                 "acc-1",
		"sem hífen":                strings.ReplaceAll(cartao.ID, "-", ""),
		"35 caracteres":            cartao.ID[:35],
		"37 caracteres":            cartao.ID + "a",
		"espaço à direita (MSSQL)": cartao.ID + " ",
		"espaço à esquerda":        " " + cartao.ID,
		"percent-encoded":          cartao.ID[:33] + "%61",
		"quebra de linha":          cartao.ID[:35] + "\n",
		"homóglifo cirílico":       strings.Replace(cartao.ID, "a", "а", 1),
		"chaves do uuid.Parse":     "{" + cartao.ID + "}",
		"10 kB":                    strings.Repeat("a", 10*1024),
	}
	for nome, valor := range malformados {
		t.Run(nome, func(t *testing.T) {
			rec, registrado := confirmarComLog(t, a, lote.ID, corpoTransfer(pagamento.ID, valor))

			campos := camposDo400(t, rec)
			assert.Equal(t, msgFormaDaBorda, campos["counterpartAccountId"])
			assert.Len(t, campos, 1, "a borda aponta um campo só")

			// Sem eco: nem no corpo, nem no log. Para o valor de 10 kB, o
			// trecho conferido é um pedaço reconhecível dele — que é o que
			// apareceria num eco truncado.
			trecho := valor
			if len(trecho) > 64 {
				trecho = trecho[:64]
			}
			assert.NotContains(t, rec.Body.String(), trecho, "a resposta não ecoa o valor recusado")
			assert.NotContains(t, registrado, trecho, "o log não ecoa o valor recusado")

			// E não chegou ao serviço: nada gravado, lote intacto.
			assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
			assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)
		})
	}

	// O byte NUL tem DOIS caminhos, e os dois terminam em 400 — por motivos
	// diferentes, que vale distinguir porque a forma da resposta muda.
	//
	// Cru, ele nem chega à borda: JSON não aceita controle (< 0x20) solto
	// dentro de string, então o pedido morre no decodificador, com o 400
	// genérico e SEM `fields`. Escapado na forma que o JSON exige para um
	// controle, o corpo é válido, o valor chega ao handler como string com NUL
	// e aí sim quem recusa é a conferência de forma, apontando o campo.
	t.Run("byte NUL cru morre no decodificador, sem campo", func(t *testing.T) {
		corpo := `{"decisions":[{"rowId":"` + pagamento.ID +
			`","action":"transfer","counterpartAccountId":"` + cartao.ID[:35] + "\x00" + `"}]}`
		rec, registrado := confirmarComLog(t, a, lote.ID, corpo)

		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		var env httpserver.ErrorEnvelope
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
		assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
		assert.Empty(t, env.Error.Fields, "controle cru em string JSON não chega a virar campo")
		assert.NotContains(t, rec.Body.String(), cartao.ID[:35], "o erro de decodificação não ecoa o corpo")
		assert.NotContains(t, registrado, cartao.ID[:35])
		assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	})

	t.Run("byte NUL escapado é recusado pela borda, no campo", func(t *testing.T) {
		// O corpo é montado pelo encoding/json de propósito: é ele que escreve
		// o controle na forma que o JSON exige, e é essa a requisição que um
		// cliente hostil manda — JSON válido com um NUL dentro do id.
		corpo, err := json.Marshal(map[string]any{
			"decisions": []map[string]string{{
				"rowId":                pagamento.ID,
				"action":               "transfer",
				"counterpartAccountId": cartao.ID[:35] + "\x00",
			}},
		})
		require.NoError(t, err)
		rec, registrado := confirmarComLog(t, a, lote.ID, string(corpo))

		campos := camposDo400(t, rec)
		assert.Equal(t, msgFormaDaBorda, campos["counterpartAccountId"])
		assert.NotContains(t, rec.Body.String(), cartao.ID[:35])
		assert.NotContains(t, registrado, cartao.ID[:35])
		assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	})

	// A prova de que a recusa é da BORDA, e não do serviço: com um id de lote
	// que não existe, a resposta continua 400 de forma — o 404 do serviço
	// exigiria que o pedido tivesse chegado lá.
	t.Run("recusa antes de o lote sequer ser procurado", func(t *testing.T) {
		rec, _ := confirmarComLog(t, a, "00000000-0000-7000-9999-999999999999",
			corpoTransfer(pagamento.ID, "acc-1"))
		campos := camposDo400(t, rec)
		assert.Equal(t, msgFormaDaBorda, campos["counterpartAccountId"])
	})

	// Vazio é legítimo na borda. Sem sugestão da análise, quem recusa é o
	// serviço, no MESMO campo e com a OUTRA mensagem.
	t.Run("vazio passa pela borda e é recusado pelo serviço", func(t *testing.T) {
		rec := confirmarPelaAPI(t, a, lote.ID, corpoTransfer(pagamento.ID, ""))
		campos := camposDo400(t, rec)
		assert.Equal(t, msgSemContraparte, campos["counterpartAccountId"])
	})

	// E o caminho feliz continua passando por tudo isso — sem ele, os casos
	// acima poderiam estar sendo recusados por qualquer outro motivo. Vai por
	// último porque o confirm consome o lote.
	t.Run("a mesma decisão com id canônico é aceita", func(t *testing.T) {
		res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, corpoTransfer(pagamento.ID, cartao.ID)))
		assert.Equal(t, 1, res.TransfersCreated)
	})
}

// O caminho feliz da canonização: contraparte de OUTRA conta, em caixa
// trocada, com collation frouxa — a transferência é aceita e o que fica
// GRAVADO nas duas pernas é o id canônico de cada conta, nunca a grafia do
// cliente.
func TestConfirmGravaOIdCanonicoNasDuasPernasDaTransferencia(t *testing.T) {
	t.Parallel()

	a := novoAmbiente(t)
	conta := a.contaComId(t, idDoLote, a.casa.ID, "Nubank Conta", account.KindChecking, "nubank")
	cartao := a.contaComId(t, idDoCartao, a.casa.ID, "Nubank Cartão", account.KindCreditCard, "nubank")
	lote, pagamento := loteComPagamentoDeFatura(t, a, conta)
	a.contas.frouxa.Store(true)

	pedido := strings.ToUpper(cartao.ID)
	require.NotEqual(t, cartao.ID, pedido)

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, corpoTransfer(pagamento.ID, pedido)))
	require.Equal(t, 1, res.TransfersCreated)

	var saida, entrada *transaction.Transaction
	lancamentos := a.lancamentosDa(t, a.casa.ID, "2026-08")
	require.NotEmpty(t, lancamentos)
	for i := range lancamentos {
		l := lancamentos[i]
		// NENHUMA linha da casa — perna ou não — carrega a grafia do cliente.
		assert.NotEqual(t, pedido, l.AccountID,
			"a string do cliente gravada é a linha que o SQL encontra e que nenhum mapa em Go encontra")
		assert.True(t, l.AccountID == conta.ID || l.AccountID == cartao.ID,
			"todo lançamento aponta para um id canônico conhecido: %q", l.AccountID)

		switch l.Kind {
		case transaction.KindTransferOut:
			saida = &lancamentos[i]
		case transaction.KindTransferIn:
			entrada = &lancamentos[i]
		}
	}

	require.NotNil(t, saida, "a perna de saída foi gravada")
	require.NotNil(t, entrada, "a perna de entrada foi gravada")
	assert.Equal(t, conta.ID, saida.AccountID, "o dinheiro sai da conta do lote")
	assert.Equal(t, cartao.ID, entrada.AccountID, "e entra na contraparte, pelo id CANÔNICO dela")
	require.NotNil(t, saida.TransferGroupID)
	require.NotNil(t, entrada.TransferGroupID)
	assert.Equal(t, *saida.TransferGroupID, *entrada.TransferGroupID, "as duas pernas são o mesmo par")
}

// BOLA: contraparte de OUTRA casa é 404 — o mesmo 404 de conta inexistente,
// byte a byte (S1) —, inclusive com a collation frouxa ligada e com a caixa
// trocada, que é a variante nova.
//
// `contraparte_arquivada_test.go` já cobre a conta alheia ARQUIVADA com o id
// exato; este cobre a conta alheia VIVA, e a grafia trocada, que é o que a
// collation frouxa muda.
func TestContraparteDeOutraCasaEh404InclusiveComCaixaTrocada(t *testing.T) {
	t.Parallel()

	a := novoAmbiente(t)
	conta := a.contaComId(t, idDoLote, a.casa.ID, "Nubank Conta", account.KindChecking, "nubank")
	alheia := a.contaComId(t, idDaContaAlheia, a.alheia.ID, "Corrente Alheia",
		account.KindChecking, account.InstitutionOther)
	lote, pagamento := loteComPagamentoDeFatura(t, a, conta)
	a.contas.frouxa.Store(true)

	// A referência: um id canônico que não existe em casa nenhuma.
	const inexistente = "00000000-0000-7000-9999-999999999999"
	referencia := confirmarPelaAPI(t, a, lote.ID, corpoTransfer(pagamento.ID, inexistente))
	require.Equal(t, http.StatusNotFound, referencia.Code, referencia.Body.String())

	grafias := map[string]string{
		"id exato da conta alheia": alheia.ID,
		"caixa trocada":            strings.ToUpper(alheia.ID),
	}
	for nome, pedido := range grafias {
		t.Run(nome, func(t *testing.T) {
			rec := confirmarPelaAPI(t, a, lote.ID, corpoTransfer(pagamento.ID, pedido))

			assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			assert.Equal(t, referencia.Body.String(), rec.Body.String(),
				"a resposta é IDÊNTICA à de conta inexistente — nada confirma o recurso alheio")
			assert.NotContains(t, rec.Body.String(), pedido)

			// Nenhuma das duas casas ganhou lançamento, e o lote continua de pé.
			assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
			assert.Empty(t, a.lancamentosDa(t, a.alheia.ID, "2026-08"))
			assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)
		})
	}
}
