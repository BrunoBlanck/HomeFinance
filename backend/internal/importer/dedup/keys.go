package dedup

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// KeyVersion é a versão da chave canônica.
//
// ⚠️ Ela é a primeira coisa dentro do hash, e mudá-la CEGA a deduplicação
// contra tudo o que já foi importado: o mesmo lançamento passa a produzir outra
// chave, não encontra a gêmea e entra de novo. Subir esta versão exige ADR e
// uma decisão explícita sobre o histórico (ADR-025c).
//
// Na prática isso também quer dizer que o sanitizador de descrição
// (importer/sanitize, com o seu próprio Version) é PARTE DESTE CONTRATO: mudar
// como uma descrição é limpa muda a chave derivada de todas as linhas futuras.
const KeyVersion = "v1"

// Discriminadores de cada tipo de chave (§4.4 da spec 0004). Eles estão dentro
// do hash para que uma chave natural nunca possa colidir com uma derivada.
const (
	kindNatural  = "nat"
	kindDerived  = "der"
	kindManual   = "man"
	kindPairLeg  = "pair"
	fieldSep     = "|"
	transferLegI = "in"
)

// NaturalKey é a chave de uma linha que o documento IDENTIFICA (o Identificador
// do Nubank).
//
// Duas decisões que parecem detalhe e não são:
//
//   - a instituição entra na chave porque um id só é único DENTRO do emissor.
//     Sem ela, o "1" do banco A e o "1" do banco B seriam a mesma linha;
//   - a conta entra na chave porque, sem ela, importar o arquivo na conta
//     errada e depois na conta certa ficaria bloqueado para sempre. Com ela, a
//     segunda importação passa — e o id repetido em OUTRA conta da casa vira
//     marcação fraca ("esta linha já foi importada na conta X"), que é o
//     usuário vendo o próprio erro em vez de bater numa parede (§4.2).
//
// externalID vai por ÚLTIMO de propósito: é o único campo de forma livre, e
// estando no fim ele não consegue "empurrar" um separador para dentro do campo
// seguinte e fabricar uma colisão com outra tupla.
func NaturalKey(institution, accountID, externalID string) string {
	return hashParts(KeyVersion, kindNatural, institution, accountID, externalID)
}

// DerivedKey é a chave de uma linha que o documento NÃO identifica (a fatura de
// cartão, que não numera as linhas).
//
// ⚠️ descriptionNorm tem de ser a descrição JÁ SANITIZADA E JÁ TRUNCADA em
// sanitize.MaxRunes — exatamente a que será gravada em
// transactions.description_norm (ADR-025c). Calculada sobre o texto cru, a
// truncagem do armazenamento faz a reimportação gerar uma chave diferente da
// gravada, e a deduplicação simplesmente deixa de funcionar. Não é um erro que
// um teste pequeno acuse: tudo continua "passando", e as duplicatas aparecem
// semanas depois no extrato do usuário. Use importer.Describe, que devolve a
// descrição e a forma normalizada juntas justamente para não haver dois
// caminhos.
//
// A tupla NÃO é única — e não deveria ser. Dois cafés de R$ 11,00 no mesmo dia
// na mesma padaria são dois gastos reais. A unicidade é
// (household_id, dedup_key, dedup_ordinal), e é o ordinal que separa os dois.
func DerivedKey(accountID, kind string, occurredOn civil.Date, amountCents int64, descriptionNorm string) string {
	return hashParts(
		KeyVersion,
		kindDerived,
		accountID,
		kind,
		occurredOn.String(),
		strconv.FormatInt(amountCents, 10),
		descriptionNorm,
	)
}

// ManualKey é a chave de um lançamento digitado por uma pessoa (E2b).
//
// Ela é única por construção e nunca barra nada. Existe para que
// transactions.dedup_key possa ser NOT NULL e a regra seja UMA só: sem isso,
// voltaríamos a ter coluna anulável dentro de índice único, que é a armadilha
// P3 (o MSSQL trata NULLs como iguais e deixaria passar exatamente UMA linha
// sem chave por casa — todos os lançamentos manuais colidindo entre si).
func ManualKey(transactionID string) string {
	return hashParts(KeyVersion, kindManual, transactionID)
}

// PairKey é a chave da perna de ENTRADA de uma transferência criada pela
// importação (ADR-016).
//
// Só a perna de entrada precisa de chave própria: a de saída é a linha do
// arquivo, e já tem a chave que veio dela.
func PairKey(transferGroupID string) string {
	return hashParts(KeyVersion, kindPairLeg, transferGroupID, transferLegI)
}

// hashParts junta os campos com "|" e devolve o SHA-256 em hexadecimal (64
// caracteres, que é a largura de transactions.dedup_key).
//
// SHA-256 simples, SEM HMAC, e é decisão consciente (ADR-025c): a chave não é
// credencial, não há ganho nenhum para um atacante em colidir as próprias
// linhas, e um segredo rotacionado cegaria a deduplicação contra todo o
// passado, em silêncio, no dia da rotação.
//
// ⚠️ INVARIANTE DO FORMATO: só o ÚLTIMO campo de cada chave pode conter o
// separador. Com o separador num campo do meio, a concatenação fica ambígua —
// NaturalKey("nubank", "conta", "a|b") e NaturalKey("nubank", "conta|a", "b")
// produziriam a MESMA string e, portanto, a mesma chave, o que faz duas linhas
// diferentes virarem uma e uma delas sumir. Na prática o id de conta é UUID e a
// instituição vem de allowlist, mas "na prática" não é invariante: Analyze a
// confere explicitamente (validarCamposDeChave). O formato da string é o da
// §4.4 da spec 0004, literal.
func hashParts(parts ...string) string {
	soma := sha256.Sum256([]byte(strings.Join(parts, fieldSep)))
	return hex.EncodeToString(soma[:])
}
