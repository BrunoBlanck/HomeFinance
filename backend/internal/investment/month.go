package investment

import (
	"fmt"
	"math"
)

// mes é um mês de competência decomposto. Existe como tipo para que a
// aritmética de mês aconteça em UM lugar, em inteiros — e não com funções de
// data, que têm quatro sintaxes diferentes nos quatro dialetos (armadilha P1)
// e que aqui nem chegam a ser necessárias: "AAAA-MM" tem largura fixa, então
// a comparação de TEXTO já é cronológica (a mesma propriedade que
// SumByAccountUntil usa em "AAAA-MM-DD").
type mes struct {
	ano int
	num int // 1..12
}

// String devolve a forma canônica "AAAA-MM" — a mesma do contrato, da coluna
// competence_month e do parâmetro `month`.
//
// O ano é sempre escrito com quatro dígitos: sem o zero à esquerda, "999-12"
// compararia MAIOR que "1000-01" numa ordenação de texto, e a série de 12
// meses passaria a mentir na única fronteira em que ninguém olha.
func (m mes) String() string { return fmt.Sprintf("%04d-%02d", m.ano, m.num) }

// menos recua n meses (n ≥ 0), com vira-ano correto.
//
// A conta é feita em "meses absolutos" justamente para não precisar de um laço
// nem de aritmética modular com sinal, que é onde se erra: 2026-01 menos 1 é
// 2025-12, e um `num--` desprotegido produziria o mês zero.
func (m mes) menos(n int) mes {
	total := m.ano*12 + (m.num - 1) - n
	ano := total / 12
	resto := total % 12
	// Go trunca a divisão em direção ao zero, então resto negativo existe
	// (ano < 0 só acontece antes do ano 1, que civil.New nem aceita — mas a
	// aritmética não pode depender disso).
	if resto < 0 {
		resto += 12
		ano--
	}
	return mes{ano: ano, num: resto + 1}
}

// mais avança n meses. É `menos` com o sinal trocado, e não uma segunda
// implementação: duas aritméticas de mês no mesmo arquivo divergem na virada
// do ano, que é justamente o caso que ninguém testa à mão.
func (m mes) mais(n int) mes { return m.menos(-n) }

// janelaDaSerie devolve o primeiro e o último mês da série de SeriesMonths
// meses que TERMINA no mês pedido.
//
// É a MAIOR das duas janelas que a tela precisa: a do ano-até-o-mês (Y-01..M)
// está sempre contida nela, para qualquer M. Prova em dois casos, que são os
// extremos: com M = Y-01, a série começa em (Y-1)-02 e a janela do ano é o
// próprio M; com M = Y-12, a série começa exatamente em Y-01. Por isso UMA
// consulta serve os três números (ADR-029j).
func janelaDaSerie(m mes) (primeiro, ultimo mes) {
	return m.menos(SeriesMonths - 1), m
}

// primeiroMesDoAno é o "Y-01" da janela do ano-até-o-mês. Ano CIVIL da casa,
// não "últimos 12 meses" — são perguntas diferentes e a tela mostra as duas.
func primeiroMesDoAno(m mes) mes { return mes{ano: m.ano, num: 1} }

// somaSegura soma dois valores NÃO NEGATIVOS e informa se coube em int64.
//
// Cópia deliberada de report.somaSegura (achado B3 da revisão de segurança
// daquela entrega), e não um import: os dois pacotes são leitores
// independentes e um helper compartilhado exportado só para isso seria pior
// do que doze linhas duplicadas com o mesmo teste.
//
// A não negatividade é VERIFICADA, não confiada: com `a < 0`, a conta
// `math.MaxInt64-a` transbordaria e a função passaria a responder "não coube"
// para quase todo `b`. Nem centavos (ValidateAmount recusa negativo) nem
// COUNT(*) podem ser negativos; se um deles for, é banco em estado que a
// aplicação não produz, e a tela prefere 500 a publicar um aporte negativo
// contra um contrato que declara `minimum: 0` (ADR-029 j.1).
func somaSegura(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if b > math.MaxInt64-a {
		return 0, false
	}
	return a + b, true
}
