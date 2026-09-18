//go:build !race

package textmatch

// raceEnabled informa aos testes de desempenho que o binário NÃO está
// instrumentado pelo detector de corrida: o limite do critério 11 vale cheio.
const raceEnabled = false
