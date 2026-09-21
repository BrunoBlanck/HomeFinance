//go:build !race

package gormstore_test

// raceEnabled informa aos testes de tempo que o binário NÃO está instrumentado
// pelo detector de corrida: o orçamento vale cheio.
const raceEnabled = false
