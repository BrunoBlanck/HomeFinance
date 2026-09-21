//go:build !race

package aiimport

// raceEnabled informa ao teste de heap que o binário NÃO está instrumentado
// pelo detector de corrida.
const raceEnabled = false
