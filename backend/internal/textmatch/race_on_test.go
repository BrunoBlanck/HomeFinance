//go:build race

package textmatch

// raceEnabled informa aos testes de desempenho que o binário está
// instrumentado pelo detector de corrida (5–10× mais lento).
const raceEnabled = true
