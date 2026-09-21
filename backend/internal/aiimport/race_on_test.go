//go:build race

package aiimport

// raceEnabled informa ao teste de heap que o binário está instrumentado pelo
// detector de corrida (a medição muda de ordem de grandeza sob ele).
const raceEnabled = true
