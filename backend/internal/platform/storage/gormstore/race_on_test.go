//go:build race

package gormstore_test

// raceEnabled informa aos testes de tempo que o binário ESTÁ instrumentado
// pelo detector de corrida, que multiplica o tempo de parede por uma ordem de
// grandeza. O orçamento é relaxado na mesma proporção: o critério é sobre o
// binário real, e o tempo medido vai sempre para o log.
const raceEnabled = true
