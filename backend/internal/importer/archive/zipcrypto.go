package archive

import "hash/crc32"

// ZipCrypto — a cifra "PKWARE tradicional" do formato ZIP (APPNOTE.TXT §6.0).
//
// Por que ela está implementada à mão aqui, em ~60 linhas, em vez de uma
// dependência (ADR-024): as bibliotecas Go que suportam ZIP com senha
// (`github.com/yeka/zip`, `github.com/alexmullins/zip`) são forks do
// `archive/zip` INTEIRO — trazem junto o parser binário de contêiner, que é o
// componente mais perigoso deste caminho, e estão sem manutenção. Aqui o
// parsing continua sendo o da stdlib (auditado e corrigido pelo time do Go) e
// só o algoritmo de fluxo — que é aritmética pura, sem I/O e sem alocação — é
// nosso.
//
// ⚠️ ZipCrypto é uma cifra FRACA e reconhecidamente quebrada (ataque de texto
// claro conhecido de Biham–Kocher recupera as chaves com ~12 bytes conhecidos).
// Ela NÃO é usada para proteger nada nosso: é apenas o formato em que os bancos
// entregam o extrato, e precisamos saber lê-lo. Nada neste projeto é cifrado
// com ZipCrypto.

// Constantes de inicialização das três chaves, fixas na especificação.
const (
	zipCryptoKey0 uint32 = 0x12345678
	zipCryptoKey1 uint32 = 0x23456789
	zipCryptoKey2 uint32 = 0x34567890

	// zipCryptoHeaderLen é o cabeçalho de 12 bytes que precede os dados
	// cifrados de cada entrada.
	zipCryptoHeaderLen = 12
)

// zipCryptoKeys é o estado de 96 bits da cifra.
type zipCryptoKeys struct {
	k0, k1, k2 uint32
}

// newZipCryptoKeys deriva o estado inicial a partir da senha.
//
// A senha é lida como sequência de BYTES, nunca como string: a especificação
// não define codificação, os utilitários usam os bytes crus, e um []byte é o
// único tipo que conseguimos apagar da memória depois (ver Zero).
func newZipCryptoKeys(password []byte) *zipCryptoKeys {
	k := &zipCryptoKeys{k0: zipCryptoKey0, k1: zipCryptoKey1, k2: zipCryptoKey2}
	for _, b := range password {
		k.update(b)
	}
	return k
}

// update avança o estado com um byte de TEXTO CLARO.
//
// O `crc32update` da especificação é a atualização crua do CRC-32 refletido —
// sem a inversão inicial/final que `crc32.Update` aplica. Por isso o código usa
// a tabela (`crc32.IEEETable`) diretamente: usar `crc32.Update` aqui daria
// chaves erradas e o arquivo seria recusado com a senha certa.
//
// O índice da tabela é recortado com `& 0xff` em vez de `byte(...)`: dá o mesmo
// valor, e deixa o recorte explícito em vez de escondido numa conversão.
func (k *zipCryptoKeys) update(plain byte) {
	k.k0 = crc32.IEEETable[(k.k0^uint32(plain))&0xff] ^ (k.k0 >> 8)
	k.k1 = (k.k1+(k.k0&0xff))*134775813 + 1
	k.k2 = crc32.IEEETable[(k.k2^(k.k1>>24))&0xff] ^ (k.k2 >> 8)
}

// streamByte devolve o próximo byte do fluxo pseudoaleatório.
//
// `temp` é de 16 bits por definição, mas o produto `temp * (temp^1)` é
// calculado em 32 bits (é o que o `int` do C faz por promoção); truncar o
// produto em 16 bits daria outro resultado. Como `0xFFFF * 0xFFFE` ainda cabe
// em uint32, não há estouro.
//
// As máscaras (`& 0xffff`, `& 0xff`) fazem o mesmo que uma conversão estreita,
// com duas vantagens: o recorte fica explícito para quem lê e a faixa do valor
// fica PROVÁVEL para o analisador estático — é o que faz a regra G115
// (conversão com possível estouro) passar sem anotação de exceção.
func (k *zipCryptoKeys) streamByte() byte {
	t := (k.k2 & 0xffff) | 2
	return byte(((t * (t ^ 1)) >> 8) & 0xff)
}

// decrypt descriptografa `buf` no lugar.
//
// Na decifragem o estado avança com o byte de texto CLARO (o resultado do XOR);
// na cifragem, também. Inverter isso é o erro clássico que produz um fluxo
// divergente a partir do segundo byte.
func (k *zipCryptoKeys) decrypt(buf []byte) {
	for i, c := range buf {
		plain := c ^ k.streamByte()
		k.update(plain)
		buf[i] = plain
	}
}

// zero apaga o estado derivado da senha assim que ele sai de uso.
func (k *zipCryptoKeys) zero() {
	k.k0, k.k1, k.k2 = 0, 0, 0
}

// Zero sobrescreve um segredo em memória com zeros.
//
// Limite conhecido e documentado: Go não garante que o compilador preserve a
// escrita (não existe `explicit_bzero`), e o GC pode ter copiado o slice antes.
// Ainda assim vale a pena — encurta a janela em que a senha fica legível num
// core dump ou num heap profile. O que NÃO vale é guardar senha em `string`:
// string é imutável, não há o que apagar.
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
