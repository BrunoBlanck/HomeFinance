//go:build race

package importer_test

// raceEnabled informa aos testes de desempenho que o binário está
// instrumentado pelo detector de corrida (5–10× mais lento): o limite do
// critério 11 ponta a ponta é relaxado, como em textmatch/race_on_test.go.
const raceEnabled = true
