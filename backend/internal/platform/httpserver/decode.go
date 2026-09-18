package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"sync"
)

// Erros de decodificação, traduzidos pelo handler para status HTTP.
var (
	// ErrUnsupportedMediaType — Content-Type não é application/json (415).
	ErrUnsupportedMediaType = errors.New("content-type não suportado")
	// ErrPayloadTooLarge — corpo acima de MaxBytes (413).
	ErrPayloadTooLarge = errors.New("corpo grande demais")
	// ErrMalformedBody — JSON inválido, campo desconhecido, tipo errado (400).
	ErrMalformedBody = errors.New("corpo malformado")
)

// DecodeJSON lê o corpo da requisição em T aplicando as defesas da
// docs/SEGURANCA.md §3: Content-Type exigido, campos desconhecidos
// rejeitados, NOME DE CAMPO na caixa exata do contrato, e um único objeto JSON
// por corpo.
//
// O limite de tamanho vem do middleware MaxBytes, aplicado antes na cadeia —
// aqui só traduzimos o erro do http.MaxBytesReader.
//
// O corpo é lido primeiro como valor BRUTO (json.RawMessage) e só depois
// convertido em T. O desvio custa uma cópia do corpo — limitada a
// config.MaxRequestBodyBytes pelo middleware — MAIS uma segunda varredura dele
// (o corpo é percorrido duas vezes: a conferência de nomes e a decodificação de
// verdade). Ele paga por si porque é entre as duas etapas que os nomes dos
// campos são conferidos com a caixa exata, coisa que o DisallowUnknownFields
// não faz (ver nomesNaCaixaExata). A varredura anda por TOKENS, com o índice de
// campos memorizado por tipo: ela não materializa mapa nem fatia por objeto do
// corpo, que é o que a tornaria cara num confirm de milhares de linhas.
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var dst T

	if err := requireJSONContentType(r); err != nil {
		return dst, err
	}

	dec := json.NewDecoder(r.Body)

	var bruto json.RawMessage
	if err := dec.Decode(&bruto); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return dst, ErrPayloadTooLarge
		}
		if errors.Is(err, io.EOF) {
			return dst, fmt.Errorf("%w: corpo vazio", ErrMalformedBody)
		}
		// A mensagem detalhada fica só no log do handler; o cliente recebe
		// a mensagem genérica de validação.
		return dst, fmt.Errorf("%w: %w", ErrMalformedBody, err)
	}

	// Um corpo com mais de um valor JSON ("{}{}") é entrada hostil.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return dst, ErrPayloadTooLarge
		}
		return dst, fmt.Errorf("%w: corpo com mais de um valor JSON", ErrMalformedBody)
	}

	if err := nomesNaCaixaExata(bruto, reflect.TypeFor[T]()); err != nil {
		return dst, err
	}

	corpo := json.NewDecoder(bytes.NewReader(bruto))
	corpo.DisallowUnknownFields()
	if err := corpo.Decode(&dst); err != nil {
		return dst, fmt.Errorf("%w: %w", ErrMalformedBody, err)
	}

	return dst, nil
}

// tipoUnmarshaler é o json.Unmarshaler para comparação por reflexão.
var tipoUnmarshaler = reflect.TypeFor[json.Unmarshaler]()

// Memórias por TIPO da conferência de nomes. Só crescem com o número de tipos
// de corpo da API (finito e fixo em tempo de compilação): não há entrada por
// requisição, logo não há como o cliente fazê-las crescer.
var (
	indiceDeCampos sync.Map // reflect.Type -> map[string]reflect.Type
	tiposComNome   sync.Map // reflect.Type -> bool
)

const (
	// maxProfundidadeDeNomes é o teto de aninhamento conferido. Os corpos
	// deste contrato têm dois níveis (o confirm da importação é o mais fundo);
	// 32 é folga de sobra e, ainda assim, um número.
	maxProfundidadeDeNomes = 32

	// maxTokensConferidos é o teto de TOKENS lidos pela varredura num corpo. É
	// a defesa de CUSTO dela: `json.Decoder.Token()` aloca por token, e um
	// corpo de 1 MiB tem centenas de milhares deles. Acima do teto o corpo é
	// 400 — e não "passa sem conferir": desistir da conferência seria ensinar
	// como escapar dela.
	//
	// O teto conta TOKENS, e não nomes de campo, porque nome só é lido dentro
	// de objeto: um corpo que é uma lista gigante de escalares, ou um objeto
	// vazio repetido dez mil vezes, não leria nome nenhum e passaria por baixo
	// de um teto de nomes (achado A1 da revisão de 17/09/2026).
	//
	// 50.000 é ~3,5× o maior corpo legítimo do contrato: o confirm da
	// importação com importer.MaxDecisions (2.000) decisões de cinco campos lê
	// sete tokens por decisão (as chaves e as duas chaves do objeto; os VALORES
	// escalares são pulados pelo scanner, sem token), ou seja ~14 mil — e o
	// serviço recusa acima de 2.000 decisões de qualquer forma.
	maxTokensConferidos = 50_000

	// maxPromocoesDeEmbutido limita a recursão por struct EMBUTIDO. Um tipo
	// que se embute por ponteiro (`type T struct{ *T }`) é legal em Go, e sem
	// este teto a montagem do índice de campos não terminaria.
	maxPromocoesDeEmbutido = 8
)

// nomesNaCaixaExata recusa o corpo cujo nome de campo não é EXATAMENTE o do
// contrato (spec 0005 §13).
//
// Por que existe: o encoding/json casa o nome do campo ignorando a caixa, e o
// DisallowUnknownFields não muda isso — ele recusa o que não casa com campo
// nenhum, e `{"CategoryId":…}` casa. O contrato (backend/api/openapi.yaml)
// declara UM nome por campo; aceitar quatro grafias do mesmo campo cria quatro
// formas de descrever o mesmo pedido, e é dessa folga que nascem as diferenças
// entre o que um WAF, um log e o servidor acham que foi enviado.
//
// A conferência é RECURSIVA (o objeto aninhado e o item de lista têm o mesmo
// furo) e deliberadamente PERMISSIVA fora do caso que ela existe para pegar:
// quando o tipo decide sozinho como se decodificar (json.Unmarshaler, como
// civil.Date e os tri-estados de PATCH), quando o JSON não tem a forma que o
// tipo espera, ou quando o destino é interface/RawMessage, ela sai calada e
// deixa o decodificador de verdade dar a palavra final. O único NÃO que ela
// diz é "este objeto tem uma chave que não é campo deste struct" — e essa
// chave, se for só a caixa trocada, o DisallowUnknownFields deixaria passar.
//
// Ela anda por TOKENS, e não desserializando cada objeto num
// map[string]json.RawMessage, por dois motivos que andam juntos:
//   - CORREÇÃO: mapa colapsa chave repetida e guarda só a ÚLTIMA, enquanto o
//     encoding/json processa TODAS as ocorrências e deixa cada uma escrever no
//     destino. Conferir pelo mapa deixaria passar
//     `{"decisions":[{"RowId":…}],"decisions":[{}]}` — a primeira ocorrência
//     escreve e nunca é olhada. Andando por tokens, toda ocorrência é
//     conferida (achado M2 da revisão de segurança de 17/09/2026).
//   - CUSTO: um corpo de 1 MiB de decisões triviais tem dezenas de milhares de
//     objetos. Um mapa e um índice de campos por objeto multiplicavam as
//     alocações por linha do arquivo; por token, não se aloca nada por objeto,
//     e o índice de campos é memorizado por TIPO (achado M1).
func nomesNaCaixaExata(bruto json.RawMessage, t reflect.Type) error {
	if t == nil || len(bruto) == 0 {
		return nil
	}
	v := &varreduraDeNomes{dec: json.NewDecoder(bytes.NewReader(bruto))}
	return v.valor(t, 0)
}

// varreduraDeNomes é a conferência em curso sobre UM corpo.
//
// O contador de TOKENS existe para que o custo da varredura não dependa do
// tamanho do corpo, e sim de um teto próprio: um corpo de 1 MiB pode ter
// centenas de milhares de tokens, e lê-los todos custaria mais do que a
// decodificação que vem depois (achados M1 e A1). Nenhum corpo legítimo da API
// chega perto do teto — o maior é o confirm da importação, com
// importer.MaxDecisions (2.000) decisões de cinco campos.
type varreduraDeNomes struct {
	dec    *json.Decoder
	tokens int
}

// errEstruturaDemais é o corpo que passou do teto de tokens da varredura. É
// ErrMalformedBody na cadeia, logo 400 genérico para o cliente — e o corpo NÃO
// é decodificado depois dele.
var errEstruturaDemais = fmt.Errorf("%w: corpo com estrutura demais", ErrMalformedBody)

// valor confere UM valor JSON — o próximo do decodificador — contra o tipo que
// vai recebê-lo, e consome exatamente esse valor.
//
// profundidade é o freio contra tipo recursivo (uma árvore, um dia): passado o
// teto, a conferência para, o valor é pulado e o corpo continua nas mãos do
// decodificador. Nunca uma pilha estourada no caminho de request.
func (v *varreduraDeNomes) valor(t reflect.Type, profundidade int) error {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || profundidade > maxProfundidadeDeNomes || !temNomeAConferir(t) {
		return v.pularValor()
	}

	tok, err := v.token()
	if err != nil {
		return v.seForTeto(err)
	}
	abre, ok := tok.(json.Delim)
	if !ok {
		return nil // escalar: não há nome de campo para conferir
	}

	switch {
	case abre == '{' && t.Kind() == reflect.Struct:
		aceitos := camposDoTipo(t)
		for v.dec.More() {
			chave, err := v.token()
			if err != nil {
				return v.seForTeto(err)
			}
			nome, ok := chave.(string)
			if !ok {
				return nil
			}
			tipoDoCampo, conhecido := aceitos[nome]
			if !conhecido {
				// Sem o nome recebido na mensagem: ele vem do cliente, o erro
				// atravessa o log do handler, e nada obriga esse nome a ser
				// texto inofensivo.
				return fmt.Errorf("%w: nome de campo fora do contrato", ErrMalformedBody)
			}
			if err := v.valor(tipoDoCampo, profundidade+1); err != nil {
				return err
			}
		}

	case abre == '{' && t.Kind() == reflect.Map:
		// A CHAVE de um mapa é dado, não nome de campo: só os valores descem.
		for v.dec.More() {
			if _, err := v.token(); err != nil {
				return v.seForTeto(err)
			}
			if err := v.valor(t.Elem(), profundidade+1); err != nil {
				return err
			}
		}

	case abre == '[' && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array):
		for v.dec.More() {
			if err := v.valor(t.Elem(), profundidade+1); err != nil {
				return err
			}
		}

	default:
		// Forma que o tipo não espera (objeto onde cabe lista, e vice-versa):
		// é erro de TIPO, não de nome, e quem o relata — com o campo — é o
		// decodificador.
		return v.pularAteFechar()
	}

	if _, err := v.token(); err != nil { // o '}' ou ']' que fecha este valor
		return v.seForTeto(err)
	}
	return nil
}

// token lê o próximo token CONTANDO-O.
//
// Todo token do caminhador passa por aqui, e é isso que dá ao custo da
// varredura um teto que não depende do corpo: `json.Decoder.Token()` embrulha
// cada valor numa interface — uma alocação por token —, e um corpo de 1 MiB
// tem centenas de milhares deles (achado A1 da revisão de segurança de
// 17/09/2026, alcançável nas rotas públicas de autenticação).
func (v *varreduraDeNomes) token() (json.Token, error) {
	v.tokens++
	if v.tokens > maxTokensConferidos {
		return nil, errEstruturaDemais
	}
	return v.dec.Token()
}

// seForTeto separa os dois motivos de um token não vir: o TETO, que é recusa
// do corpo, e o JSON quebrado, que é silêncio — quem explica JSON quebrado ao
// cliente é o decodificador de verdade, que roda logo em seguida sobre os
// mesmos bytes.
func (v *varreduraDeNomes) seForTeto(err error) error {
	if errors.Is(err, errEstruturaDemais) {
		return err
	}
	return nil
}

// pularValor consome o próximo valor inteiro sem conferir nada.
//
// Usa o SCANNER do encoding/json (Decode em json.RawMessage), e não o laço de
// tokens: pular é o caminho de todo campo escalar e de toda lista de escalares
// — a maioria absoluta do contrato —, e ali o laço de tokens custava uma
// alocação por token do corpo (achado A1). Misturar Decode e Token no mesmo
// decodificador é suportado: em posição de valor, Decode consome exatamente um
// valor e devolve o decodificador sincronizado.
//
// O erro é ignorado de propósito: JSON quebrado é assunto do decodificador de
// verdade, e um valor que não pôde ser pulado deixa o caminhador sem sincronia
// — os tokens seguintes deixam de casar com o tipo e a conferência sai calada,
// que é o lado seguro dela.
func (v *varreduraDeNomes) pularValor() error {
	_ = v.dec.Decode(new(json.RawMessage))
	return nil
}

// pularAteFechar consome o resto de um objeto ou lista JÁ ABERTO, incluindo o
// delimitador que o fecha. Cada token conta para o teto.
func (v *varreduraDeNomes) pularAteFechar() error {
	aberto := 1
	for aberto > 0 {
		tok, err := v.token()
		if err != nil {
			return v.seForTeto(err)
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				aberto++
			case '}', ']':
				aberto--
			}
		}
	}
	return nil
}

// temNomeAConferir informa se o tipo (ou algo dentro dele) tem nome de campo
// para conferir. `[]string`, `int` e os tri-estados com UnmarshalJSON próprio
// não têm — e pular a varredura deles é o que mantém o custo perto do que era
// antes desta conferência existir.
func temNomeAConferir(t reflect.Type) bool {
	if v, ok := tiposComNome.Load(t); ok {
		return v.(bool)
	}
	resposta := calcularTemNome(t, 0)
	tiposComNome.Store(t, resposta)
	return resposta
}

func calcularTemNome(t reflect.Type, profundidade int) bool {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || profundidade > maxProfundidadeDeNomes {
		return false
	}
	// Tipo que decodifica a si mesmo manda no próprio corpo: parar nele é o
	// que impede a conferência de discordar do decodificador.
	if t.Kind() != reflect.Interface && reflect.PointerTo(t).Implements(tipoUnmarshaler) {
		return false
	}
	switch t.Kind() {
	case reflect.Struct:
		return true
	case reflect.Slice, reflect.Array, reflect.Map:
		return calcularTemNome(t.Elem(), profundidade+1)
	default:
		return false
	}
}

// camposDoTipo mapeia nome-no-JSON -> tipo do campo do struct, memorizado por
// TIPO: os tipos de corpo da API são finitos e fixos, e remontar o índice a
// cada objeto do corpo era o grosso do custo da conferência (achado M1 da
// revisão de 17/09/2026).
//
// O mapa devolvido é SOMENTE LEITURA — depois de guardado, ninguém escreve
// nele, e é isso que torna a leitura concorrente segura sem trava.
func camposDoTipo(t reflect.Type) map[string]reflect.Type {
	if v, ok := indiceDeCampos.Load(t); ok {
		return v.(map[string]reflect.Type)
	}
	campos := montarCampos(t, 0)
	indiceDeCampos.Store(t, campos)
	return campos
}

// montarCampos aplica as MESMAS regras do encoding/json para escolher o nome:
// a tag manda, `json:"-"` some, campo sem tag usa o nome Go, e struct embutido
// sem tag promove os campos dele.
func montarCampos(t reflect.Type, promocoes int) map[string]reflect.Type {
	out := make(map[string]reflect.Type, t.NumField())
	var embutidos []reflect.Type

	// Primeira passada: os campos do PRÓPRIO struct. Eles têm precedência
	// sobre os promovidos, e por isso entram antes — resolver a disputa na
	// ordem de declaração daria resultado diferente conforme onde o embutido
	// aparece.
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue // não exportado: o encoding/json nem o enxerga
		}

		nome, _, temOpcoes := strings.Cut(f.Tag.Get("json"), ",")
		if nome == "-" && !temOpcoes {
			continue // json:"-" é campo que não existe no corpo
		}
		if nome != "" {
			out[nome] = f.Type
			continue
		}

		interno := f.Type
		for interno.Kind() == reflect.Pointer {
			interno = interno.Elem()
		}
		if f.Anonymous && interno.Kind() == reflect.Struct {
			embutidos = append(embutidos, interno)
			continue
		}
		out[f.Name] = f.Type
	}

	// Segunda passada: os campos promovidos do struct embutido sem tag.
	if promocoes >= maxPromocoesDeEmbutido {
		return out
	}
	for _, interno := range embutidos {
		for nome, tipo := range montarCampos(interno, promocoes+1) {
			if _, jaTem := out[nome]; !jaTem {
				out[nome] = tipo
			}
		}
	}
	return out
}

func requireJSONContentType(r *http.Request) error {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return ErrUnsupportedMediaType
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ErrUnsupportedMediaType
	}
	if !strings.EqualFold(mediaType, "application/json") {
		return ErrUnsupportedMediaType
	}
	return nil
}

// WriteDecodeError traduz o erro de DecodeJSON para a resposta HTTP do
// contrato. Nunca ecoa o erro original (evita vazar estrutura interna).
//
// Quando o `encoding/json` sabe QUAL campo tinha o tipo errado, esse nome é
// repassado em `fields`. Sem isso, um corpo com `"openingDate":"2026-02-30"`
// responderia só "Dados inválidos", e o formulário não teria como destacar o
// campo — o usuário reler o corpo inteiro procurando o erro. A MENSAGEM
// continua genérica: o que o cliente ganha é o endereço do problema, não uma
// descrição da estrutura interna.
func WriteDecodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnsupportedMediaType):
		WriteError(w, http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, MsgUnsupportedMediaType)

	case errors.Is(err, ErrPayloadTooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, MsgPayloadTooLarge)

	default:
		if campo := campoComTipoErrado(err); campo != "" {
			WriteValidationError(w, map[string]string{campo: MsgCampoInvalido})
			return
		}
		WriteError(w, http.StatusBadRequest, CodeValidationFailed, MsgValidationFailed)
	}
}

// MsgCampoInvalido é a mensagem por campo quando o valor não tem a forma que o
// contrato pede. Deliberadamente genérica: o formato exato de cada campo está
// no OpenAPI, e repeti-lo aqui em pt-BR seria mais um lugar para envelhecer.
const MsgCampoInvalido = "Valor inválido para este campo."

// campoComTipoErrado extrai o nome do campo de um *json.UnmarshalTypeError.
//
// O nome vem em notação de caminho para campo aninhado ("filtro.mes"); o
// contrato desta API é raso, mas a forma é preservada em vez de cortada — o
// dia em que houver aninhamento, a resposta continua apontando o lugar certo.
func campoComTipoErrado(err error) string {
	var tipoErrado *json.UnmarshalTypeError
	if errors.As(err, &tipoErrado) {
		return tipoErrado.Field
	}
	return ""
}
