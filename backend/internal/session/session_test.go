package session_test

import (
	"context"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromContextSemIdentidade(t *testing.T) {
	t.Parallel()

	_, ok := session.FromContext(context.Background())
	assert.False(t, ok)
}

func TestNewContextEFromContext(t *testing.T) {
	t.Parallel()

	want := session.Identity{UserID: "u1", HouseholdID: "h1", Role: session.RoleOwner, SessionID: "s1"}
	got, ok := session.FromContext(session.NewContext(context.Background(), want))
	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestIdentityIncompletaNaoEhAceita(t *testing.T) {
	t.Parallel()

	incompletas := []session.Identity{
		{HouseholdID: "h", Role: "owner", SessionID: "s"},
		{UserID: "u", Role: "owner", SessionID: "s"},
		{UserID: "u", HouseholdID: "h", SessionID: "s"},
		{UserID: "u", HouseholdID: "h", Role: "owner"},
	}
	for _, ident := range incompletas {
		assert.False(t, ident.Valid())
		_, ok := session.FromContext(session.NewContext(context.Background(), ident))
		assert.False(t, ok, "identidade incompleta não pode autenticar: %+v", ident)
	}
}
