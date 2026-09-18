package importer

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/importer/archive"
)

// A leitura do multipart de POST /imports.
//
// ⚠️ É lida por r.MultipartReader(), e NUNCA por ParseMultipartForm ou
// ReadForm. Os dois gravam em disco tudo o que passa do teto de memória, e o
// que eles gravariam aqui é extrato bancário — nome de terceiro, CPF mascarado,
// agência e conta, num arquivo temporário que ninguém apaga e que não está em
// política de retenção nenhuma (spec 0004 §6.2). os.CreateTemp também está
// proibido nesta rota, pelo mesmo motivo.
//
// A leitura é toda em memória, com teto por parte, e o corpo inteiro já vem
// limitado a 8 MiB pela cadeia global (httpserver.MaxBytesByPath).

// Limites das partes (§5.3 da spec 0004).
const (
	// MaxMultipartParts é o teto de partes lidas. São QUATRO nomes aceitos; o
	// teto é 5 para que uma parte a mais seja recusada com erro claro em vez
	// de fazer o laço varrer um corpo inteiro de partes inúteis.
	MaxMultipartParts = 5

	// MaxAccountIDBytes espelha varchar(36) do id de conta.
	MaxAccountIDBytes = 36

	// MaxPasswordBytes é o teto da senha do ZIP.
	MaxPasswordBytes = 128

	// MaxFormatBytes espelha o maxLength do schema ImportFormatID.
	MaxFormatBytes = 40
)

// Nomes de parte aceitos. ALLOWLIST FECHADA: parte com nome fora desta lista é
// 400, nunca ignorada em silêncio (§6.7). Ignorar faria um cliente acreditar
// que mandou algo que o servidor considerou.
const (
	PartFile      = "file"
	PartAccountID = "accountId"
	PartPassword  = "password"
	PartFormat    = "format"
)

// Erros da leitura do formulário.
var (
	// ErrNotMultipart — Content-Type que não é multipart/form-data (415).
	ErrNotMultipart = errors.New("o corpo precisa ser multipart/form-data")

	// ErrMalformedForm — corpo multipart ilegível (400).
	ErrMalformedForm = errors.New("formulário malformado")

	// ErrTooManyParts — mais partes do que o teto (400).
	ErrTooManyParts = errors.New("partes demais no formulário")

	// ErrPayloadTooLarge — o corpo passou do teto da rota (413).
	ErrPayloadTooLarge = errors.New("corpo grande demais")
)

// FormFieldError aponta QUAL campo do formulário foi recusado.
//
// A mensagem é genérica e em português e nunca ecoa o valor recebido — em
// especial o da parte `password`, que não aparece em erro nenhum.
type FormFieldError struct {
	Field string
	Msg   string
}

func (e *FormFieldError) Error() string { return fmt.Sprintf("campo %q inválido", e.Field) }

// UploadForm é o formulário já lido, em memória.
type UploadForm struct {
	// Content são os bytes do arquivo. CSV solto ou ZIP — o tipo REAL é
	// decidido pelos magic bytes lá no serviço, nunca pela extensão nem pelo
	// Content-Type da parte, que são texto escolhido pelo cliente.
	Content []byte

	// FileName é o nome que o cliente mandou, ainda CRU: quem o sanitiza é o
	// serviço, antes de guardar.
	FileName string

	AccountID string

	// Password é []byte e nunca string: string é imutável e não se apaga da
	// memória. Quem a consome (Analyze) toma posse dela e a zera.
	Password []byte

	FormatID string
}

// Zero apaga a senha. Chamada pelo handler em TODO caminho de saída, inclusive
// nos de erro — é a garantia de que ela não sobrevive à requisição nem quando
// nada foi importado.
func (f *UploadForm) Zero() {
	if f == nil {
		return
	}
	archive.Zero(f.Password)
	f.Password = nil
}

// RequireMultipart confere o Content-Type ANTES de tocar no corpo.
//
// 415 (e não 400) quando não é multipart: é o tipo de mídia que está errado, e
// a tela precisa distinguir "mandei JSON numa rota de upload" de "o arquivo não
// serve".
func RequireMultipart(r *http.Request) error {
	bruto := r.Header.Get("Content-Type")
	if bruto == "" {
		return ErrNotMultipart
	}
	tipo, _, err := mime.ParseMediaType(bruto)
	if err != nil || !strings.EqualFold(tipo, "multipart/form-data") {
		return ErrNotMultipart
	}
	return nil
}

// ReadUploadForm lê as partes do multipart em memória.
//
// Nada é escrito em disco em momento algum: cada parte é lida por um
// io.LimitReader com o teto do seu campo, e o corpo inteiro já vem cortado em 8
// MiB pela cadeia global.
func ReadUploadForm(r *http.Request) (*UploadForm, error) {
	if err := RequireMultipart(r); err != nil {
		return nil, err
	}

	mr, err := r.MultipartReader()
	if err != nil {
		return nil, ErrMalformedForm
	}

	form := &UploadForm{}
	vistos := make(map[string]bool, MaxMultipartParts)

	for lidas := 0; ; lidas++ {
		if lidas >= MaxMultipartParts {
			form.Zero()
			return nil, ErrTooManyParts
		}

		parte, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			form.Zero()
			return nil, traduzirErroDeLeitura(err)
		}

		nome := parte.FormName()
		if vistos[nome] {
			// A mesma parte duas vezes: qual delas vale? Nenhuma resposta a
			// essa pergunta é visível na tela, então a requisição é recusada.
			fecharParte(parte)
			form.Zero()
			return nil, &FormFieldError{Field: nome, Msg: "Este campo foi enviado mais de uma vez."}
		}

		err = preencherParte(form, nome, parte)
		fecharParte(parte)
		if err != nil {
			form.Zero()
			return nil, err
		}
		vistos[nome] = true
	}

	if len(form.Content) == 0 {
		form.Zero()
		return nil, &FormFieldError{Field: PartFile, Msg: "Escolha um arquivo .csv ou .zip."}
	}
	if form.AccountID == "" {
		form.Zero()
		return nil, &FormFieldError{Field: PartAccountID, Msg: "Escolha a conta de destino."}
	}
	return form, nil
}

// preencherParte lê UMA parte para o campo correspondente.
func preencherParte(form *UploadForm, nome string, parte *multipart.Part) error {
	switch nome {
	case PartFile:
		conteudo, err := lerParte(parte, MaxUploadBytes)
		if err != nil {
			return err
		}
		form.Content = conteudo
		form.FileName = parte.FileName()
		return nil

	case PartAccountID:
		valor, err := lerTexto(parte, MaxAccountIDBytes, PartAccountID)
		if err != nil {
			return err
		}
		form.AccountID = valor
		return nil

	case PartPassword:
		bruto, err := lerParte(parte, MaxPasswordBytes)
		if err != nil {
			// Nem aqui o valor lido aparece: o erro diz o campo, e só.
			return &FormFieldError{Field: PartPassword, Msg: "Senha longa demais."}
		}
		// Sem trim e sem conversão para string: espaço pode fazer parte da
		// senha, e string não se apaga da memória.
		form.Password = bruto
		return nil

	case PartFormat:
		valor, err := lerTexto(parte, MaxFormatBytes, PartFormat)
		if err != nil {
			return err
		}
		form.FormatID = valor
		return nil

	default:
		// Parte fora da allowlist de quatro. 400, e não "ignoro o que não
		// conheço" (§6.7).
		return &FormFieldError{Field: "form", Msg: "Campo não reconhecido no formulário."}
	}
}

// lerParte lê no máximo `max` bytes da parte. Um byte a mais é recusa.
//
// O LimitReader recebe max+1 de propósito: assim o estouro é detectado sem que
// nada além de um byte extra seja alocado.
func lerParte(parte *multipart.Part, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(parte, max+1))
	if err != nil {
		return nil, traduzirErroDeLeitura(err)
	}
	if int64(len(b)) > max {
		return nil, ErrPayloadTooLarge
	}
	return b, nil
}

// lerTexto lê uma parte pequena como texto, já aparada.
//
// O trim existe porque cliente e proxy às vezes acrescentam espaço em volta de
// um campo de texto; ele NÃO é aplicado à senha, onde espaço é conteúdo.
func lerTexto(parte *multipart.Part, max int64, campo string) (string, error) {
	b, err := lerParte(parte, max)
	if err != nil {
		if errors.Is(err, ErrPayloadTooLarge) {
			return "", &FormFieldError{Field: campo, Msg: "Valor longo demais para este campo."}
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// fecharParte descarta o resto da parte. O erro é ignorado de propósito: o
// corpo já foi lido até o teto, e uma falha ao fechar não muda a resposta.
func fecharParte(p *multipart.Part) { _ = p.Close() }

// traduzirErroDeLeitura separa "o cliente mandou corpo demais" (413) de
// "o corpo está quebrado" (400).
func traduzirErroDeLeitura(err error) error {
	var teto *http.MaxBytesError
	if errors.As(err, &teto) {
		return ErrPayloadTooLarge
	}
	if errors.Is(err, ErrPayloadTooLarge) {
		return ErrPayloadTooLarge
	}
	return ErrMalformedForm
}
