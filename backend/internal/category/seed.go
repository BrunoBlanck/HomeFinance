package category

import (
	"context"
	"errors"
	"fmt"
)

// DefaultGroup é um grupo da semente inicial.
type DefaultGroup struct {
	Name string
	Kind string
}

// DefaultGroups é o conjunto que uma casa nova recebe (D5 do PLANOS.md).
//
// Por que semear em vez de deixar a casa vazia: casa vazia obriga a pessoa a
// inventar uma taxonomia antes de conseguir lançar o primeiro gasto, e é
// exatamente aí que se desiste de um app de finanças. Doze grupos cobrem o uso
// doméstico e dão de onde partir.
//
// Nenhum deles é "de sistema": todos são editáveis, arquiváveis e excluíveis
// como qualquer outro. Categoria que o usuário não pode mexer é categoria que
// vai atrapalhar alguém.
//
// A ordem é a de exibição: despesas primeiro (é o que mais se usa), receitas
// depois, aporte e resgate no fim, e "Outras" no fim de cada bloco.
//
// "Investimentos" e "Resgates" entram por ADR-029a. Como a semente é
// idempotente e roda também no auto-reparo do login (household.EnsureDefault),
// as casas que já existem os recebem SEM migração de dados — com a exceção
// documentada em SeedDefaults.
func DefaultGroups() []DefaultGroup {
	return []DefaultGroup{
		{Name: "Moradia", Kind: KindExpense},
		{Name: "Alimentação", Kind: KindExpense},
		{Name: "Transporte", Kind: KindExpense},
		{Name: "Saúde", Kind: KindExpense},
		{Name: "Educação", Kind: KindExpense},
		{Name: "Lazer", Kind: KindExpense},
		{Name: "Serviços", Kind: KindExpense},
		{Name: "Pessoal", Kind: KindExpense},
		{Name: "Impostos", Kind: KindExpense},
		{Name: "Outras despesas", Kind: KindExpense},
		{Name: "Salário", Kind: KindIncome},
		{Name: "Outras receitas", Kind: KindIncome},
		{Name: "Investimentos", Kind: KindInvestment},
		{Name: "Resgates", Kind: KindRedemption},
	}
}

// SeedDefaults cria os grupos iniciais da casa.
//
// **Idempotente**: um grupo cujo nome normalizado já exista na casa é pulado.
// Isso importa porque `household.EnsureDefault` também roda como auto-reparo
// no login — sem a idempotência, cada login duplicaria a semente.
//
// **A idempotência é por NOME, não por (nome, natureza) — e isso é decidido,
// não esquecido.** A casa que já criou "Investimentos" à mão como categoria de
// DESPESA continua com ela e **não** recebe o grupo novo de natureza
// `investment`: criar um segundo grupo com o mesmo nome e natureza diferente
// daria duas categorias "Investimentos" na mesma tela, e o auto-reparo do
// login recriaria a confusão a cada entrada. O caminho dessa casa é a troca de
// natureza (`expense → investment`, ADR-029c), que é explícita, auditada com
// autor e leva as subcategorias junto. Semear por natureza aqui tiraria dela a
// decisão. Há teste fixando este comportamento — ele não é um bug a consertar.
//
// **Não abre transação própria.** Ela é chamada de dentro da transação que
// cria a casa, e é isso que atende o S10 do PLANOS.md: sem chave estrangeira
// física (ADR-013), casa criada com semente pela metade é um estado que o
// banco não barra.
//
// **Respeita MaxPerHousehold, e para em silêncio quando o teto chega.** A
// semente não é exceção ao teto — e não ser era um defeito com consequência
// concreta: como ela roda no auto-reparo do LOGIN, uma casa que estivesse
// exatamente em 200 receberia os dois grupos do ADR-029a e ficaria com 202. A
// partir daí a taxonomia passa do teto que o resto do código assume, e
// `/investments/detect` com `overwriteCategorized` responde 500 PERMANENTE
// (transaction.ErrTooManyCategories, ADR-029 j.2) — sem nenhuma ação de
// autoatendimento, porque a pessoa não tem como saber que precisa excluir uma
// categoria.
//
// Parar em silêncio, e não falhar: esta função roda no caminho do LOGIN. Um
// erro aqui trancaria a entrada de quem tem a taxonomia cheia, o que é um
// estrago muito maior do que não ganhar um grupo sugerido. Casa NOVA nunca
// chega perto do teto (14 grupos contra 200).
func (s *Service) SeedDefaults(ctx context.Context, householdID string) error {
	// A semente NÃO é auditada por ator: ela roda dentro da criação da casa,
	// que já tem a sua própria entrada (`household.created`). Registrar doze
	// linhas de `category.created` sem usuário que as pediu encheria o rastro
	// de ruído e esconderia o que uma pessoa de fato fez.
	if householdID == "" {
		return fmt.Errorf("semeando categorias: %w", ErrNotFound)
	}

	// UMA contagem antes do laço, e um contador local depois: contar a cada
	// grupo custaria catorze consultas por login para responder sempre a mesma
	// coisa.
	total, err := s.repo.CountAll(ctx, householdID)
	if err != nil {
		return fmt.Errorf("contando categorias antes da semente: %w", err)
	}

	now := s.clock()
	for _, grupo := range DefaultGroups() {
		if total >= MaxPerHousehold {
			return nil
		}
		name, norm, err := NormalizeName(grupo.Name)
		if err != nil {
			// Só aconteceria se a lista acima tivesse um nome inválido, o que
			// é bug de código, não entrada do usuário.
			return fmt.Errorf("semente inválida %q: %w", grupo.Name, err)
		}

		taken, err := s.repo.NameTaken(ctx, householdID, nil, norm, "")
		if err != nil {
			return fmt.Errorf("verificando semente: %w", err)
		}
		if taken {
			continue
		}

		created := Category{
			ID:          s.ids(),
			HouseholdID: householdID,
			Name:        name,
			NameNorm:    norm,
			Kind:        grupo.Kind,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.repo.Create(ctx, &created); err != nil && !errors.Is(err, ErrNameTaken) {
			return fmt.Errorf("criando categoria da semente: %w", err)
		}
		total++
	}
	return nil
}
