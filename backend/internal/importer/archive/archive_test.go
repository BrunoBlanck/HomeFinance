package archive

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/rand"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Ferramentas do teste
//
// O lado do ENCRYPT do ZipCrypto vive aqui, e só aqui: produção nunca cifra
// nada com ZipCrypto (é cifra quebrada). Gerar os arquivos dentro do teste é o
// que permite cobrir os casos de abuso sem versionar um único ZIP real nem uma
// única senha literal.
// ---------------------------------------------------------------------------

// randomPassword gera uma senha imprevisível e imprimível.
//
// Nenhuma senha literal aparece no repositório: mesmo uma senha "de teste"
// vira, com o tempo, a senha que alguém copiou para algum lugar de verdade.
func randomPassword(t *testing.T, n int) []byte {
	t.Helper()
	raw := make([]byte, n)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	const printable = '~' - '!' + 1
	for i := range raw {
		raw[i] = byte('!') + raw[i]%printable
	}
	return raw
}

// encryptZipCrypto cifra `plain` (cabeçalho de 12 bytes + dados comprimidos)
// com a senha. É o inverso exato de zipCryptoKeys.decrypt.
func encryptZipCrypto(password, plain []byte) []byte {
	k := newZipCryptoKeys(password)
	out := make([]byte, len(plain))
	for i, p := range plain {
		out[i] = p ^ k.streamByte()
		k.update(p)
	}
	return out
}

// entrySpec descreve uma entrada a fabricar dentro do ZIP de teste.
type entrySpec struct {
	name     string
	plain    []byte
	password []byte // nil = entrada sem cifra

	// method só vale quando methodSet é true; o padrão do builder é
	// zip.Deflate, que é o que os bancos usam.
	method      uint16
	methodSet   bool
	checkByte   byte
	forceBadCRC bool
	extra       []byte
}

func deflateBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var comp bytes.Buffer
	fw, err := flate.NewWriter(&comp, flate.BestCompression)
	require.NoError(t, err)
	_, err = fw.Write(plain)
	require.NoError(t, err)
	require.NoError(t, fw.Close())
	return comp.Bytes()
}

// buildArchive monta um ZIP em memória com as entradas descritas.
func buildArchive(t *testing.T, specs ...entrySpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, s := range specs {
		method := uint16(zip.Deflate)
		if s.methodSet {
			method = s.method
		}

		var payload []byte
		switch method {
		case zip.Store:
			payload = append([]byte(nil), s.plain...)
		default:
			payload = deflateBytes(t, s.plain)
		}

		// Flags do arquivo real do banco: bit 3 (0x0008, data descriptor) +
		// bit 11 (0x0800, nome em UTF-8). Com o bit 0 da cifra vira 0x0809.
		flags := uint16(0x0008 | 0x0800)
		if s.password != nil {
			header := make([]byte, zipCryptoHeaderLen)
			_, err := rand.Read(header[:zipCryptoHeaderLen-1])
			require.NoError(t, err)
			header[zipCryptoHeaderLen-1] = s.checkByte
			payload = encryptZipCrypto(s.password, append(header, payload...))
			flags |= flagEncrypted // 0x0809, igual ao arquivo real do banco
		}

		crc := crc32.ChecksumIEEE(s.plain)
		if s.forceBadCRC {
			crc ^= 0xFFFFFFFF
		}

		fh := &zip.FileHeader{
			Name:               s.name,
			Method:             method,
			Flags:              flags,
			ReaderVersion:      20,
			CRC32:              crc,
			CompressedSize64:   uint64(len(payload)),
			UncompressedSize64: uint64(len(s.plain)),
			Extra:              s.extra,
		}
		w, err := zw.CreateRaw(fh)
		require.NoError(t, err)
		_, err = w.Write(payload)
		require.NoError(t, err)
	}

	require.NoError(t, zw.Close())
	return buf.Bytes()
}

const csvSample = "Data,Valor,Descrição\n04/08/2026,-20.00,Pagamento de fatura\n"

// ---------------------------------------------------------------------------
// Testes
// ---------------------------------------------------------------------------

// TestGeneratedArchiveMatchesRealBankFile documenta que o ZIP fabricado nos
// testes tem a MESMA forma do arquivo real do banco — se a fixture divergir, os
// testes deixam de provar o que dizem provar.
func TestGeneratedArchiveMatchesRealBankFile(t *testing.T) {
	password := randomPassword(t, 12)
	data := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), password: password})

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	f := zr.File[0]

	require.Equal(t, uint16(zip.Deflate), f.Method, "method=8 (deflate)")
	require.Equal(t, uint16(0x0809), f.Flags, "flags=0x0809: cifrado (bit 0) + data descriptor (bit 3)")
	require.Equal(t, uint16(20), f.ReaderVersion)
	require.Empty(t, f.Extra, "ExtraLen=0")
	require.NotZero(t, f.CRC32, "o CRC do diretório central vem preenchido mesmo com o bit 3 ligado")

	// A stdlib NÃO detecta a cifra: ela entrega os bytes cifrados ao flate.
	rc, err := f.Open()
	require.NoError(t, err)
	_, err = rc.Read(make([]byte, 64))
	require.Error(t, err, "f.Open() falha com 'corrupt input' — a stdlib não sabe que está cifrado")
}

func TestExtractCorrectPassword(t *testing.T) {
	password := randomPassword(t, 16)
	data := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), password: append([]byte(nil), password...)})

	entry, err := Extract(data, password)
	require.NoError(t, err)
	require.Equal(t, "extrato.csv", entry.Name)
	require.Equal(t, csvSample, string(entry.Data))
}

// TestExtractIgnoresCheckByte é a razão de ErrPasswordInvalid usar o CRC do
// conteúdo inflado como único veredito.
//
// Com o bit 3 ligado, a APPNOTE manda comparar o 12º byte do cabeçalho de
// cifragem com o byte alto da HORA DOS — não com `CRC>>24`. A implementação que
// circula na internet compara com o CRC e recusaria o arquivo do banco COM A
// SENHA CERTA. Aqui o byte é deliberadamente qualquer coisa.
func TestExtractIgnoresCheckByte(t *testing.T) {
	for _, checkByte := range []byte{0x00, 0x42, 0xFF} {
		password := randomPassword(t, 10)
		data := buildArchive(t, entrySpec{
			name:      "extrato.csv",
			plain:     []byte(csvSample),
			password:  append([]byte(nil), password...),
			checkByte: checkByte,
		})
		entry, err := Extract(data, password)
		require.NoError(t, err, "byte de verificação 0x%02X não pode reprovar a senha certa", checkByte)
		require.Equal(t, csvSample, string(entry.Data))
	}
}

func TestExtractWrongPassword(t *testing.T) {
	right := randomPassword(t, 16)
	wrong := randomPassword(t, 16)
	require.NotEqual(t, right, wrong)

	data := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), password: right})

	_, err := Extract(data, wrong)
	require.ErrorIs(t, err, ErrPasswordInvalid)
	require.NotErrorIs(t, err, ErrInvalidArchive, "senha errada não pode virar 'arquivo corrompido'")
}

// TestExtractWrongPasswordManyTimes cobre o caminho em que o ruído por acaso
// infla sem erro e só o CRC reprova — senão a cobertura ficaria refém de qual
// dos dois caminhos o ruído toma.
func TestExtractWrongPasswordManyTimes(t *testing.T) {
	for i := range 40 {
		right := randomPassword(t, 8)
		wrong := randomPassword(t, 8)
		data := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), password: right})
		_, err := Extract(data, wrong)
		require.ErrorIs(t, err, ErrPasswordInvalid, "tentativa %d", i)
	}
}

// TestExtractEncryptedWithoutPassword é o caso que o bit 0 existe para pegar.
func TestExtractEncryptedWithoutPassword(t *testing.T) {
	password := randomPassword(t, 16)
	data := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), password: password})

	for _, p := range [][]byte{nil, {}} {
		_, err := Extract(data, p)
		require.ErrorIs(t, err, ErrPasswordRequired)
		require.NotErrorIs(t, err, ErrInvalidArchive, "faltar senha não pode virar 'arquivo corrompido'")
	}
}

func TestExtractPlainZip(t *testing.T) {
	data := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample)})
	entry, err := Extract(data, nil)
	require.NoError(t, err)
	require.Equal(t, csvSample, string(entry.Data))
}

func TestExtractStoredEntry(t *testing.T) {
	data := buildArchive(t, entrySpec{
		name:      "extrato.csv",
		plain:     []byte(csvSample),
		method:    zip.Store,
		methodSet: true,
	})
	entry, err := Extract(data, nil)
	require.NoError(t, err)
	require.Equal(t, csvSample, string(entry.Data))
}

func TestExtractStoredEncryptedEntry(t *testing.T) {
	password := randomPassword(t, 9)
	data := buildArchive(t, entrySpec{
		name:      "extrato.csv",
		plain:     []byte(csvSample),
		password:  append([]byte(nil), password...),
		method:    zip.Store,
		methodSet: true,
	})
	entry, err := Extract(data, password)
	require.NoError(t, err)
	require.Equal(t, csvSample, string(entry.Data))
}

func TestExtractTwoEntries(t *testing.T) {
	data := buildArchive(t,
		entrySpec{name: "extrato.csv", plain: []byte(csvSample)},
		entrySpec{name: "fatura.csv", plain: []byte(csvSample)},
	)
	_, err := Extract(data, nil)
	require.ErrorIs(t, err, ErrNotSingleEntry)
}

func TestExtractZeroEntries(t *testing.T) {
	data := buildArchive(t)
	_, err := Extract(data, nil)
	require.ErrorIs(t, err, ErrNotSingleEntry)
}

func TestExtractRejectsNonCSV(t *testing.T) {
	for _, name := range []string{"extrato.txt", "extrato", "extrato.csv.exe", "extrato.zip", ".csv"} {
		data := buildArchive(t, entrySpec{name: name, plain: []byte(csvSample)})
		_, err := Extract(data, nil)
		require.ErrorIs(t, err, ErrEntryNotCSV, "nome %q", name)
	}
}

func TestExtractRejectsUnsafeNames(t *testing.T) {
	casos := map[string]string{
		"traversal relativo":     "../extrato.csv",
		"traversal profundo":     "a/../../extrato.csv",
		"barra":                  "pasta/extrato.csv",
		"contrabarra do Windows": `pasta\extrato.csv`,
		"drive do Windows":       "C:extrato.csv",
		"ponto-ponto embutido":   "extrato..csv",
		"NUL no nome":            "extrato\x00.csv",
		"quebra de linha":        "extrato\n.csv",
		"DEL":                    "extrato\x7f.csv",
		"nome longo demais":      strings.Repeat("a", MaxNameBytes) + ".csv",
	}
	for nome, entrada := range casos {
		t.Run(nome, func(t *testing.T) {
			data := buildArchive(t, entrySpec{name: entrada, plain: []byte(csvSample)})
			_, err := Extract(data, nil)
			require.ErrorIs(t, err, ErrUnsafeName)
		})
	}
}

// TestExtractRejectsNestedArchive cobre as duas formas: pelo nome e, quando o
// nome mente, pelos bytes mágicos do conteúdo.
func TestExtractRejectsNestedArchive(t *testing.T) {
	inner := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample)})

	t.Run("nome de zip", func(t *testing.T) {
		outer := buildArchive(t, entrySpec{name: "interno.zip", plain: inner})
		_, err := Extract(outer, nil)
		require.ErrorIs(t, err, ErrEntryNotCSV)
	})

	t.Run("zip disfarçado de csv", func(t *testing.T) {
		outer := buildArchive(t, entrySpec{name: "interno.csv", plain: inner})
		_, err := Extract(outer, nil)
		require.ErrorIs(t, err, ErrNestedArchive)
	})

	t.Run("gzip disfarçado de csv", func(t *testing.T) {
		gz := append([]byte{0x1F, 0x8B, 0x08}, bytes.Repeat([]byte{0x41}, 1024)...)
		outer := buildArchive(t, entrySpec{name: "interno.csv", plain: gz})
		_, err := Extract(outer, nil)
		require.ErrorIs(t, err, ErrNestedArchive)
	})
}

// TestExtractRejectsCompressionBomb usa uma razão de ~1000:1 ficando ABAIXO do
// teto absoluto, para provar que a defesa da razão existe por si — e não que o
// teste só bateu no limite de 8 MiB.
func TestExtractRejectsCompressionBomb(t *testing.T) {
	plain := bytes.Repeat([]byte{'A'}, 1<<20) // 1 MiB, infla para muito menos de 8 MiB
	data := buildArchive(t, entrySpec{name: "extrato.csv", plain: plain})

	_, err := Extract(data, nil)
	require.ErrorIs(t, err, ErrCompressionBomb)
}

// TestExtractRejectsOversizedContent bate no teto ABSOLUTO durante a inflação —
// o cabeçalho continua dizendo o tamanho "certo", e é justamente isso que não
// pode ser usado como critério.
func TestExtractRejectsOversizedContent(t *testing.T) {
	plain := make([]byte, MaxUncompressedBytes+1024)
	// Conteúdo pouco compressível o suficiente para a razão não disparar antes.
	for i := range plain {
		plain[i] = byte(i%26) + 'a'
	}
	data := buildArchive(t, entrySpec{name: "extrato.csv", plain: plain})

	_, err := Extract(data, nil)
	require.ErrorIs(t, err, ErrTooLarge)
}

// TestExtractIgnoresDeclaredUncompressedSize é o teste que falharia se alguém
// passasse a confiar no cabeçalho: aqui ele DECLARA 1 byte e entrega 9 MiB.
func TestExtractIgnoresDeclaredUncompressedSize(t *testing.T) {
	plain := bytes.Repeat([]byte{'A'}, MaxUncompressedBytes+1024)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fh := &zip.FileHeader{
		Name:               "extrato.csv",
		Method:             zip.Deflate,
		Flags:              0x0008,
		ReaderVersion:      20,
		CRC32:              crc32.ChecksumIEEE(plain),
		UncompressedSize64: 1, // mentira deliberada
	}
	payload := deflateBytes(t, plain)
	fh.CompressedSize64 = uint64(len(payload))
	w, err := zw.CreateRaw(fh)
	require.NoError(t, err)
	_, err = w.Write(payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	_, err = Extract(buf.Bytes(), nil)
	require.Error(t, err)
	require.True(t,
		errors.Is(err, ErrTooLarge) || errors.Is(err, ErrCompressionBomb),
		"o teto tem de sair da contagem real, não do cabeçalho; veio: %v", err)
}

func TestExtractRejectsOversizedArchive(t *testing.T) {
	_, err := Extract(make([]byte, MaxArchiveBytes+1), nil)
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestExtractRejectsAES(t *testing.T) {
	t.Run("method 99", func(t *testing.T) {
		data := buildArchive(t, entrySpec{
			name:      "extrato.csv",
			plain:     []byte(csvSample),
			method:    99,
			methodSet: true,
			password:  randomPassword(t, 8),
		})
		_, err := Extract(data, randomPassword(t, 8))
		require.ErrorIs(t, err, ErrEncryptionUnsupported)
	})

	t.Run("campo extra 0x9901", func(t *testing.T) {
		// id=0x9901, len=7, dados
		extra := []byte{0x01, 0x99, 0x07, 0x00, 0x02, 0x00, 'A', 'E', 0x03, 0x08, 0x00}
		data := buildArchive(t, entrySpec{
			name:     "extrato.csv",
			plain:    []byte(csvSample),
			password: randomPassword(t, 8),
			extra:    extra,
		})
		_, err := Extract(data, randomPassword(t, 8))
		require.ErrorIs(t, err, ErrEncryptionUnsupported)
	})

	t.Run("bit 6 de cifragem forte", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		payload := deflateBytes(t, []byte(csvSample))
		fh := &zip.FileHeader{
			Name:               "extrato.csv",
			Method:             zip.Deflate,
			Flags:              0x0008 | flagEncrypted | flagStrongEncryption,
			ReaderVersion:      20,
			CRC32:              crc32.ChecksumIEEE([]byte(csvSample)),
			CompressedSize64:   uint64(len(payload)),
			UncompressedSize64: uint64(len(csvSample)),
		}
		w, err := zw.CreateRaw(fh)
		require.NoError(t, err)
		_, err = w.Write(payload)
		require.NoError(t, err)
		require.NoError(t, zw.Close())

		_, err = Extract(buf.Bytes(), randomPassword(t, 8))
		require.ErrorIs(t, err, ErrEncryptionUnsupported)
	})
}

func TestExtractRejectsCorruptInput(t *testing.T) {
	casos := map[string][]byte{
		"vazio":           {},
		"lixo":            []byte("isto não é um zip"),
		"só a assinatura": {'P', 'K', 0x03, 0x04},
	}
	for nome, data := range casos {
		t.Run(nome, func(t *testing.T) {
			_, err := Extract(data, nil)
			require.ErrorIs(t, err, ErrInvalidArchive)
		})
	}
}

func TestExtractRejectsBadCRCWithoutPassword(t *testing.T) {
	data := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), forceBadCRC: true})
	_, err := Extract(data, nil)
	require.ErrorIs(t, err, ErrInvalidArchive)
}

// TestExtractZeroesPassword prova o contrato destrutivo documentado em Extract.
func TestExtractZeroesPassword(t *testing.T) {
	t.Run("sucesso", func(t *testing.T) {
		password := randomPassword(t, 20)
		data := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), password: append([]byte(nil), password...)})

		_, err := Extract(data, password)
		require.NoError(t, err)
		require.Equal(t, make([]byte, 20), password, "a senha tem de sair zerada da chamada")
	})

	t.Run("erro", func(t *testing.T) {
		password := randomPassword(t, 20)
		_, err := Extract([]byte("lixo"), password)
		require.Error(t, err)
		require.Equal(t, make([]byte, 20), password, "a senha tem de sair zerada também no caminho de erro")
	})
}

// TestExtractNeverMentionsPassword garante que nenhuma mensagem de erro carrega
// a senha — nem inteira, nem em pedaço reconhecível.
func TestExtractNeverMentionsPassword(t *testing.T) {
	right := randomPassword(t, 24)
	wrong := append([]byte(nil), randomPassword(t, 24)...)
	copiaWrong := append([]byte(nil), wrong...)

	data := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), password: right})
	_, err := Extract(data, wrong)
	require.Error(t, err)
	require.NotContains(t, err.Error(), string(copiaWrong))
	require.NotContains(t, err.Error(), string(copiaWrong[:8]))
}

// TestExtractWritesNothingToDisk aponta TODAS as variáveis de temporário para
// um diretório vazio e prova que ele continua vazio depois de exercitar os
// caminhos de sucesso e de erro.
//
// É o par do TestPackageNeverTouchesFilesystem logo abaixo: um observa o
// comportamento, o outro proíbe a chamada. Só os dois juntos fecham a regra.
func TestExtractWritesNothingToDisk(t *testing.T) {
	tmp := t.TempDir()
	for _, v := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(v, tmp)
	}

	antes, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, antes)

	password := randomPassword(t, 14)
	ok := buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), password: append([]byte(nil), password...)})

	_, err = Extract(ok, password)
	require.NoError(t, err)
	_, _ = Extract(buildArchive(t, entrySpec{name: "extrato.csv", plain: []byte(csvSample), password: randomPassword(t, 14)}), randomPassword(t, 14))
	_, _ = Extract(buildArchive(t, entrySpec{name: "extrato.csv", plain: bytes.Repeat([]byte{'A'}, 1<<20)}), nil)
	_, _ = Extract([]byte("lixo"), nil)

	depois, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, depois, "o pacote não pode escrever nada em disco — extração é 100%% em memória")
}

// TestPackageNeverTouchesFilesystem lê o próprio código-fonte do pacote e
// recusa qualquer escrita em disco. Um teste de comportamento não pega o dia em
// que alguém acrescentar um `os.CreateTemp` num caminho raro; este pega.
func TestPackageNeverTouchesFilesystem(t *testing.T) {
	// Os parênteses fazem parte do padrão de propósito: procuramos a CHAMADA,
	// não a menção em comentário (a doc do pacote cita os nomes para dizer que
	// são proibidos).
	proibidos := []string{
		"os.Create(", "os.CreateTemp(", "os.WriteFile(", "os.OpenFile(",
		"os.MkdirTemp(", "os.Mkdir(", "os.MkdirAll(",
		"ioutil.TempFile(", "ioutil.WriteFile(",
	}
	for _, arquivo := range fontesDoPacote(t) {
		conteudo, err := os.ReadFile(arquivo)
		require.NoError(t, err)
		for _, p := range proibidos {
			require.NotContains(t, string(conteudo), p,
				"%s usa %s — a extração é 100%% em memória (ver doc do pacote)", filepath.Base(arquivo), p)
		}
	}
}

// TestPackageNeverSilencesGosec garante que o teto de descompressão continua
// sendo real, e não uma anotação que desliga o alerta.
func TestPackageNeverSilencesGosec(t *testing.T) {
	for _, arquivo := range fontesDoPacote(t) {
		conteudo, err := os.ReadFile(arquivo)
		require.NoError(t, err)
		require.NotContains(t, string(conteudo), "#no"+"sec",
			"%s silencia o gosec; o objetivo é passar sem nenhuma exceção", filepath.Base(arquivo))
		require.NotContains(t, string(conteudo), "io.Copy(",
			"%s usa io.Copy num caminho de descompressão (G110); use io.CopyN", filepath.Base(arquivo))
	}
}

// fontesDoPacote devolve os .go de produção (sem os _test.go).
func fontesDoPacote(t *testing.T) []string {
	t.Helper()
	entradas, err := os.ReadDir(".")
	require.NoError(t, err)
	var fontes []string
	for _, e := range entradas {
		nome := e.Name()
		if e.IsDir() || !strings.HasSuffix(nome, ".go") || strings.HasSuffix(nome, "_test.go") {
			continue
		}
		fontes = append(fontes, nome)
	}
	require.NotEmpty(t, fontes)
	return fontes
}
