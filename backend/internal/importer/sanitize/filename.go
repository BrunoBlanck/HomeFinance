package sanitize

import (
	"path"
	"strings"
	"unicode/utf8"
)

// MaxFileNameRunes é o teto de import_batches.file_name (varchar(200)).
//
// Medido em RUNAS pelo mesmo motivo de MaxRunes: cortar por byte partiria um
// caractere multibyte no meio e gravaria UTF-8 inválido.
const MaxFileNameRunes = 200

// fileNameFallback é o nome usado quando não sobra nada de aproveitável.
//
// Nome vazio não é opção: a coluna é NOT NULL e o histórico ficaria com uma
// linha sem rótulo nenhum na tela.
const fileNameFallback = "arquivo.csv"

// FileName devolve o nome de arquivo pronto para GUARDAR e EXIBIR.
//
// ⚠️ Ele NÃO participa do contrato de deduplicação (Version): a chave é
// calculada sobre os valores canônicos da linha, e o nome do arquivo não entra
// em nenhuma delas. Mudar esta função não cega a deduplicação — mudar
// Description, sim.
//
// O nome do arquivo é conteúdo do USUÁRIO, e conteúdo do usuário que vai para
// a tela passa pelas mesmas regras da descrição. O que ele NUNCA é: caminho.
// Nada neste projeto abre arquivo em disco a partir daqui (a extração é 100%
// em memória — spec 0004 §6.2), e a limpeza existe justamente para que ele
// continue não sendo um caminho no dia em que alguém esquecer disso.
//
// O pipeline, nesta ordem:
//
//  1. remoção de invisíveis e de controle, inclusive os overrides de direção
//     do Trojan Source (U+202A–U+202E, U+2066–U+2069) — sem isso um nome com
//     um override no meio seria EXIBIDO ao contrário do que está gravado, e o
//     histórico mostraria ".csv" onde o arquivo era executável;
//  2. corte de tudo o que vier antes do último separador de caminho, nas DUAS
//     convenções (a barra invertida do Windows não é separador para o
//     path.Base do Go, e é exatamente o caso do navegador antigo que manda o
//     caminho completo);
//  3. recusa de "." e ".." e de qualquer resto que ainda contenha separador;
//  4. colapso de espaços e truncagem em MaxFileNameRunes.
func FileName(s string) string {
	limpo := limparInvisiveis(s)

	// Os dois separadores, na ordem: primeiro o do Windows, porque path.Base
	// não o enxerga e deixaria "..\\..\\etc\\senha.csv" passar inteiro.
	if i := strings.LastIndexAny(limpo, `\/`); i >= 0 {
		limpo = limpo[i+1:]
	}
	limpo = path.Base(limpo)

	limpo = strings.TrimSpace(reEspacos.ReplaceAllString(limpo, " "))

	switch limpo {
	case "", ".", "..":
		return fileNameFallback
	}
	if strings.ContainsAny(limpo, `\/`) {
		// Não deveria sobrar nenhum depois dos cortes acima; se sobrou, é
		// entrada que não entendemos, e o certo é descartar o nome — não
		// "consertar" o que não sabemos ler.
		return fileNameFallback
	}
	if utf8.RuneCountInString(limpo) > MaxFileNameRunes {
		limpo = truncarRunas(limpo, MaxFileNameRunes)
	}
	return limpo
}
