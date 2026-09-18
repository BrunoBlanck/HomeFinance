// Package archive abre o contêiner do arquivo importado: um ZIP que contém
// exatamente UM CSV, possivelmente protegido por senha.
//
// Este é o ponto onde entra um arquivo binário de origem externa — a superfície
// mais hostil do importador. As regras abaixo são invariantes do pacote, não
// sugestões:
//
//   - **nada toca o disco**, nunca. A extração é 100% em memória; `os.CreateTemp`
//     é proibido aqui. Arquivo temporário de conteúdo financeiro é vazamento
//     esperando acontecer (backup, antivírus, container compartilhado) e é o
//     que transforma "zip slip" de bug em escrita arbitrária de arquivo;
//   - **nada do cabeçalho do ZIP é confiável**. `UncompressedSize64` é um número
//     escrito por quem montou o arquivo: quem o usa para dimensionar buffer ou
//     para decidir se "cabe" já perdeu. Todo limite é medido CONTANDO os bytes
//     que saem do descompressor, com `io.CopyN`, nunca com `io.Copy`;
//   - **o nome da entrada nunca vira caminho**. Ele é validado (sem `/`, `\`,
//     `..`, sem caractere de controle) porque é exibido e guardado, não porque
//     vamos abri-lo — abrir é que não acontece.
//
// Sobre senha: o ZIP do banco usa **ZipCrypto** (PKWARE tradicional), não o AES
// do WinZip. A `archive/zip` da stdlib não implementa nem DETECTA cifra alguma —
// ela simplesmente entrega os bytes cifrados ao descompressor, que falha com
// "flate: corrupt input". Sem a detecção do bit 0 deste pacote, o usuário que
// esquecesse a senha receberia "arquivo corrompido" no lugar de "falta a senha".
//
// Este pacote é FOLHA: não importa nada do projeto.
package archive

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"unicode/utf8"
)

// Limites de segurança. Todos RECUSAM; nenhum trunca em silêncio.
const (
	// MaxArchiveBytes limita o ZIP inteiro que chega à função.
	MaxArchiveBytes = 4 << 20 // 4 MiB

	// MaxCompressedBytes limita os bytes comprimidos da única entrada.
	MaxCompressedBytes = 4 << 20 // 4 MiB

	// MaxUncompressedBytes limita o conteúdo já inflado. É o teto ABSOLUTO,
	// medido enquanto se descomprime.
	MaxUncompressedBytes = 8 << 20 // 8 MiB

	// MaxCompressionRatio é a razão máxima descomprimido:comprimido.
	// Um CSV real fica na casa de 5:1 a 20:1; 200:1 já é bomba.
	MaxCompressionRatio = 200

	// ratioFloorBytes é o piso a partir do qual a razão passa a valer.
	// Sem ele, um CSV minúsculo e muito repetitivo (30 bytes comprimidos,
	// 7 KiB inflados = 233:1) seria recusado sem risco nenhum — a razão só
	// diz alguma coisa quando o volume absoluto já é relevante.
	ratioFloorBytes = 64 << 10 // 64 KiB

	// MaxNameBytes limita o nome da entrada. Medido em bytes porque é o nome
	// que atravessa header HTTP, log e banco.
	MaxNameBytes = 255

	// methodAES é o `method=99` do AES do WinZip. Não suportado — e recusado
	// explicitamente, para não virar "arquivo corrompido".
	methodAES uint16 = 99

	// flagEncrypted é o bit 0 do campo de flags: "entrada cifrada".
	flagEncrypted uint16 = 1 << 0

	// flagStrongEncryption é o bit 6: cifragem forte (AE-x, certificados).
	flagStrongEncryption uint16 = 1 << 6
)

// Erros do pacote. O handler os traduz para mensagens de UI; nenhum deles
// carrega a senha, o nome do arquivo do usuário ou qualquer byte do conteúdo.
var (
	// ErrPasswordRequired — a entrada está cifrada e nenhuma senha foi dada.
	// É a razão de existir da detecção pelo bit 0: sem ela este caso chegaria
	// ao usuário como "arquivo corrompido".
	ErrPasswordRequired = errors.New("o arquivo está protegido por senha")

	// ErrPasswordInvalid — a senha não abre o arquivo.
	//
	// O veredito é o CRC-32 do conteúdo INFLADO contra o CRC do diretório
	// central. O byte de verificação do cabeçalho de 12 bytes NÃO serve aqui:
	// com o bit 3 (data descriptor) ligado — e os arquivos reais dos bancos
	// ligam —, a APPNOTE manda compará-lo com o byte alto da HORA DOS, não com
	// `CRC>>24`. A receita que circula na internet compara com o CRC e recusa
	// esses arquivos mesmo com a senha certa.
	ErrPasswordInvalid = errors.New("senha incorreta")

	// ErrEncryptionUnsupported — AES do WinZip (method=99) ou cifragem forte.
	ErrEncryptionUnsupported = errors.New("tipo de proteção do arquivo não suportado")

	// ErrNotSingleEntry — o ZIP não tem exatamente uma entrada.
	ErrNotSingleEntry = errors.New("o arquivo ZIP deve conter exatamente um arquivo")

	// ErrEntryNotCSV — a única entrada não é um .csv.
	ErrEntryNotCSV = errors.New("o arquivo dentro do ZIP deve ser um .csv")

	// ErrUnsafeName — nome de entrada com caminho, caractere de controle ou
	// tamanho fora da faixa.
	ErrUnsafeName = errors.New("nome de arquivo inválido")

	// ErrTooLarge — estourou um dos tetos absolutos de tamanho.
	ErrTooLarge = errors.New("arquivo grande demais")

	// ErrCompressionBomb — razão de compressão acima do permitido.
	ErrCompressionBomb = errors.New("arquivo com compressão suspeita")

	// ErrNestedArchive — o conteúdo extraído é outro arquivo compactado.
	ErrNestedArchive = errors.New("arquivo compactado dentro de arquivo compactado")

	// ErrInvalidArchive — não é um ZIP legível.
	ErrInvalidArchive = errors.New("arquivo ZIP inválido")
)

// Entry é a única entrada extraída.
type Entry struct {
	// Name é o nome da entrada, já validado. Serve para exibir e registrar —
	// nunca para abrir caminho no sistema de arquivos.
	Name string

	// Data é o conteúdo inflado, em memória.
	Data []byte
}

// Extract devolve o único CSV de dentro do ZIP.
//
// ⚠️ CONTRATO DA SENHA: Extract toma posse de `password` e a ZERA antes de
// retornar, sempre — inclusive nos caminhos de erro. O chamador não deve
// reutilizar o slice depois da chamada. É deliberado: a senha entra pelo corpo
// da requisição, é usada uma vez e não tem razão nenhuma para continuar legível
// na memória do processo. Para ZIP sem senha, passe `nil`.
//
// A senha nunca é convertida para `string`, nunca é registrada em log e nunca
// aparece em erro.
func Extract(data []byte, password []byte) (Entry, error) {
	defer Zero(password)

	if len(data) == 0 {
		return Entry{}, fmt.Errorf("%w: arquivo vazio", ErrInvalidArchive)
	}
	if len(data) > MaxArchiveBytes {
		return Entry{}, fmt.Errorf("%w: o ZIP passa de %d bytes", ErrTooLarge, MaxArchiveBytes)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	// ErrInsecurePath vem ACOMPANHADA de um reader utilizável: o próprio Go
	// só avisa. Seguimos em frente porque a validação de nome logo abaixo é
	// mais rígida do que a dele — e ela é quem decide.
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return Entry{}, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if zr == nil {
		return Entry{}, ErrInvalidArchive
	}

	if len(zr.File) != 1 {
		return Entry{}, fmt.Errorf("%w: encontrei %d", ErrNotSingleEntry, len(zr.File))
	}
	f := zr.File[0]

	if err := checkName(f.Name); err != nil {
		return Entry{}, err
	}

	encrypted, err := checkEncryption(f)
	if err != nil {
		return Entry{}, err
	}
	if encrypted && len(password) == 0 {
		return Entry{}, ErrPasswordRequired
	}
	if f.Method != zip.Store && f.Method != zip.Deflate {
		return Entry{}, fmt.Errorf("%w: método de compressão %d", ErrInvalidArchive, f.Method)
	}

	// Rejeição barata antes de mexer em byte nenhum. O valor é declarado pelo
	// arquivo, então ele só serve para RECUSAR cedo — nunca para aceitar: o
	// teto de verdade é a contagem feita durante a leitura, logo abaixo.
	if f.CompressedSize64 > MaxCompressedBytes {
		return Entry{}, fmt.Errorf("%w: entrada comprimida passa de %d bytes", ErrTooLarge, MaxCompressedBytes)
	}

	payload, err := readRaw(f)
	if err != nil {
		return Entry{}, err
	}

	if encrypted {
		keys := newZipCryptoKeys(password)
		defer keys.zero()
		if len(payload) < zipCryptoHeaderLen {
			return Entry{}, fmt.Errorf("%w: entrada cifrada truncada", ErrInvalidArchive)
		}
		keys.decrypt(payload)
		// Os 12 primeiros bytes são o cabeçalho de cifragem. O byte de
		// verificação (payload[11]) é DESCARTADO de propósito — ver o
		// comentário de ErrPasswordInvalid.
		payload = payload[zipCryptoHeaderLen:]
	}

	plain, err := decompress(f.Method, payload, encrypted)
	if err != nil {
		return Entry{}, err
	}

	// Razão de compressão: só depois do teto absoluto, e só acima do piso.
	if len(plain) >= ratioFloorBytes && len(payload) > 0 &&
		len(plain)/len(payload) > MaxCompressionRatio {
		return Entry{}, fmt.Errorf("%w: razão de %d:1", ErrCompressionBomb, len(plain)/len(payload))
	}

	// VEREDITO DA SENHA. O CRC do diretório central está preenchido mesmo com
	// o bit 3 ligado, e é contra ele que comparamos.
	if crc32.ChecksumIEEE(plain) != f.CRC32 {
		if encrypted {
			return Entry{}, ErrPasswordInvalid
		}
		return Entry{}, fmt.Errorf("%w: CRC não confere", ErrInvalidArchive)
	}

	if isArchive(plain) {
		return Entry{}, ErrNestedArchive
	}

	return Entry{Name: f.Name, Data: plain}, nil
}

// checkEncryption classifica a proteção da entrada e recusa o que não sabemos
// abrir com uma mensagem que diz a verdade.
func checkEncryption(f *zip.File) (bool, error) {
	if f.Method == methodAES {
		return false, fmt.Errorf("%w: AES do WinZip", ErrEncryptionUnsupported)
	}
	if f.Flags&flagStrongEncryption != 0 {
		return false, fmt.Errorf("%w: cifragem forte", ErrEncryptionUnsupported)
	}
	// O campo extra 0x9901 marca AES mesmo quando o `method` externo mente.
	if hasAESExtraField(f.Extra) {
		return false, fmt.Errorf("%w: AES do WinZip", ErrEncryptionUnsupported)
	}
	return f.Flags&flagEncrypted != 0, nil
}

// hasAESExtraField varre os campos extras procurando o header ID 0x9901
// (AE-x do WinZip). O formato é uma sequência de (id uint16, len uint16, dados).
func hasAESExtraField(extra []byte) bool {
	for len(extra) >= 4 {
		id := uint16(extra[0]) | uint16(extra[1])<<8
		size := int(uint16(extra[2]) | uint16(extra[3])<<8)
		if id == 0x9901 {
			return true
		}
		if size > len(extra)-4 {
			return false
		}
		extra = extra[4+size:]
	}
	return false
}

// readRaw lê os bytes crus da entrada (cifrados e/ou comprimidos), CONTANDO o
// que passa em vez de acreditar no cabeçalho.
func readRaw(f *zip.File) ([]byte, error) {
	rc, err := f.OpenRaw()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	var buf bytes.Buffer
	n, err := io.CopyN(&buf, rc, MaxCompressedBytes+1)
	if n > MaxCompressedBytes {
		return nil, fmt.Errorf("%w: entrada comprimida passa de %d bytes", ErrTooLarge, MaxCompressedBytes)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	return buf.Bytes(), nil
}

// decompress infla o payload com teto contado.
//
// `io.CopyN` em vez de `io.Copy` é o ponto inteiro desta função: `io.Copy` de
// um `flate.Reader` é a bomba de descompressão clássica (regra G110 do gosec),
// e a forma correta de não tê-la não é silenciar o alerta com uma anotação de
// exceção — é não escrever a chamada ilimitada.
func decompress(method uint16, payload []byte, encrypted bool) ([]byte, error) {
	corrupt := func(err error) error {
		// Com a senha errada, o payload "descriptografado" é ruído — e ruído
		// não infla. Este é o caminho normal de senha errada, e ele tem de
		// dizer "senha", não "corrompido".
		if encrypted {
			return ErrPasswordInvalid
		}
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}

	if method == zip.Store {
		if len(payload) > MaxUncompressedBytes {
			return nil, fmt.Errorf("%w: conteúdo passa de %d bytes", ErrTooLarge, MaxUncompressedBytes)
		}
		return payload, nil
	}

	fr := flate.NewReader(bytes.NewReader(payload))
	defer func() { _ = fr.Close() }()

	var out bytes.Buffer
	n, err := io.CopyN(&out, fr, MaxUncompressedBytes+1)
	if n > MaxUncompressedBytes {
		return nil, fmt.Errorf("%w: conteúdo passa de %d bytes", ErrTooLarge, MaxUncompressedBytes)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, corrupt(err)
	}
	return out.Bytes(), nil
}

// checkName aplica as regras do nome da entrada.
//
// O nome não é usado para abrir nada — mas ele é exibido ao usuário e guardado
// junto do lote importado, e um nome com separador de caminho ou caractere de
// controle é exatamente o que vira "zip slip" no dia em que alguém, no futuro,
// resolver escrever o arquivo em disco. Recusar aqui é o que garante que esse
// dia não chegue por acidente.
func checkName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: nome vazio", ErrUnsafeName)
	}
	if len(name) > MaxNameBytes {
		return fmt.Errorf("%w: nome com mais de %d bytes", ErrUnsafeName, MaxNameBytes)
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("%w: nome não é UTF-8 válido", ErrUnsafeName)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: nome contém separador de caminho", ErrUnsafeName)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("%w: nome contém \"..\"", ErrUnsafeName)
	}
	if strings.Contains(name, ":") {
		// "C:extrato.csv" é caminho relativo a drive no Windows.
		return fmt.Errorf("%w: nome contém \":\"", ErrUnsafeName)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7F {
			return fmt.Errorf("%w: nome contém caractere de controle", ErrUnsafeName)
		}
	}
	// A extensão é conferida por último para que um nome perigoso responda
	// "nome inválido" em vez de "não é CSV".
	if !strings.HasSuffix(strings.ToLower(name), ".csv") || len(name) == len(".csv") {
		return fmt.Errorf("%w: %q", ErrEntryNotCSV, name)
	}
	return nil
}

// isArchive detecta, pelos bytes mágicos, que o "CSV" extraído é na verdade
// outro contêiner. Um ZIP dentro de ZIP nomeado `extrato.csv` passaria pela
// checagem de extensão; esta é a segunda barreira, e ela fecha a recursão.
func isArchive(b []byte) bool {
	magics := [][]byte{
		{'P', 'K', 0x03, 0x04}, // ZIP
		{'P', 'K', 0x05, 0x06}, // ZIP vazio
		{'P', 'K', 0x07, 0x08}, // ZIP spanned
		{0x1F, 0x8B},           // gzip
		{'R', 'a', 'r', '!'},   // RAR
		{'7', 'z', 0xBC, 0xAF}, // 7z
		{'B', 'Z', 'h'},        // bzip2
	}
	for _, m := range magics {
		if bytes.HasPrefix(b, m) {
			return true
		}
	}
	return false
}
