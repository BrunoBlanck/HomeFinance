// Package aiimport recebe o JSON que a pessoa colou de volta no app depois de
// conversar com uma IA de terceiros (spec 0010, entrega E9b) e o aplica em
// palavras-chave e categorias, numa transação só.
//
// O pacote NÃO fala com IA — não existe cliente, chave de API nem saída de
// rede em lugar nenhum daqui. Ele é o irmão que ESCREVE; quem lê e monta o
// prompt é `internal/aiprompt`, que não tem `Transactor` nem `Auditor` no
// construtor. São dois pacotes, e não um `internal/ai` com dois arquivos, para
// que "o export não escreve" seja verificável pelo COMPILADOR em vez de ser
// convenção de arquivo (§10.3 da spec 0010).
//
// # Como o pacote está dividido
//
//   - types.go — as interfaces ESTREITAS declaradas aqui, no consumidor (o
//     que o import precisa de categoria, conta, razão, transação e
//     auditoria), as entradas e o DTO do relatório;
//   - validate.go — as regras das §§4.2–4.3 da spec como funções PURAS sobre
//     um índice em memória, sem I/O. É aqui que a maior parte dos testes
//     bate, e é por ser puro que prévia e confirm não têm como divergir;
//   - impact.go — a medição de impacto das palavras de conta (§4.4), sob UM
//     orçamento de trabalho do `internal/textmatch` por requisição;
//   - service.go — Preview e Confirm sobre o MESMO plano, diferindo só por
//     `apply`; a escrita passa por category.Service.Create (o caminho do
//     POST /categories) e por AppendKeywords dos dois serviços — nunca pelos
//     repositórios, e nunca apagando nada;
//   - handler.go — as duas rotas, com o corpo decodificado pelas defesas da
//     borda e `notes` descartado no ato.
//
// # Regras que o pacote sustenta
//
//   - a casa vem do TOKEN, sempre (docs/SEGURANCA.md §2). Todo id do JSON é
//     procurado num índice montado a partir de List(casa do token): id de
//     outra casa não está no mapa e é `item_not_found`, indistinguível de id
//     inexistente. Forma não canônica também é `item_not_found`, nunca 400;
//   - o JSON é ENTRADA HOSTIL (ADR-036 (c)): teto de corpo (abaixo), teto de
//     entradas e de palavras reconferidos no serviço, conjunto fechado de
//     campos, e zero reflexão do conteúdo colado em erro ou em log. A palavra
//     recusada que volta no relatório passa pela mesma allowlist de runas
//     visíveis do prompt;
//   - uma entrada recusada NUNCA derruba o lote; o confirm é UMA transação
//     (grupos novos → subcategorias novas → palavras), e o estado que muda
//     entre a leitura e a escrita dela é 409, nada gravado;
//   - reimportar o mesmo JSON é inofensivo: tudo `already_present`, banco
//     inalterado;
//   - as palavras nunca vão para o log nem para a auditoria (spec 0005 §4.1).
//
// O teto de corpo mora aqui, e não solto em cmd/api, pelo mesmo motivo de
// `importer.MaxUploadBytes`: o limite é do domínio, e quem o revisar quer
// encontrá-lo junto das regras que ele protege.
package aiimport

// MaxPayloadBytes é o teto de corpo de POST /ai/keyword-import/preview e
// /confirm (spec 0010 §4.2, regra 1).
//
// 128 KiB é MUITO maior que qualquer payload legítimo — 200 entradas por
// lista com 20 palavras de até 40 runas cada não chegam perto — e MUITO menor
// que o teto global de 1 MiB. A folga é deliberada nas duas direções: o JSON
// vem de uma IA, que erra o tamanho para mais, e a pessoa não deve tomar 413
// por um lote grande e legítimo; mas o conteúdo é entrada hostil que o
// servidor vai parsear, validar campo a campo e casar contra o banco, e 1 MiB
// de JSON adversarial é trabalho de graça para quem manda.
//
// Quem corta é o `MaxBytesReader` da cadeia global
// (httpserver.MaxBytesByPath), por comparação de caminho EXATA, e o resultado
// é **413 PAYLOAD_TOO_LARGE** antes de qualquer parsing de negócio — o mesmo
// caminho das outras rotas do projeto (achado A4 da emenda §10 da spec 0010;
// a spec dizia 400 e o código impõe 413, e o código está certo).
const MaxPayloadBytes int64 = 128 << 10 // 128 KiB
