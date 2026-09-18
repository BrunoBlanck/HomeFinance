package config

// Portas de teste do pacote config.
//
// Este arquivo só é compilado em `go test` deste pacote: nada daqui existe no
// binário de produção. É o que permite manter `profileRateLimits` não
// exportada (achado B3 da revisão de segurança) sem cegar a suíte que
// verifica a FORMA do perfil frouxo.

// ProfileRateLimits expõe profileRateLimits ao teste externo (package
// config_test). Fora do teste, o único caminho para um conjunto de limites
// continua sendo Load/LoadFrom — que valida o perfil antes de entregá-lo.
var ProfileRateLimits = profileRateLimits
