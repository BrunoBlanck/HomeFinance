package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedigeChavesSensiveis(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})

	lg.Info("evento",
		slog.String("password", "senha-do-usuario"),
		slog.String("Password", "senha-do-usuario"),
		slog.String("otp_code", "123456"),
		slog.String("code", "654321"),
		slog.String("refresh_token", "tok-abcdef"),
		slog.String("token_hash", "deadbeef"),
		slog.String("Cookie", "hf_access=abc"),
		slog.String("db_dsn", "postgres://u:p@h/db"),
		slog.String("jwt_secret", "segredo"),
		slog.String("otp_pepper", "pimenta"),
		slog.String("Authorization", "Bearer xyz"),
		slog.String("user_id", "u-123"),
	)

	out := buf.String()
	for _, sensivel := range []string{
		"senha-do-usuario", "123456", "654321", "tok-abcdef", "deadbeef",
		"hf_access=abc", "postgres://u:p@h/db", "segredo", "pimenta", "Bearer xyz",
	} {
		assert.NotContains(t, out, sensivel, "valor sensível vazou no log")
	}
	// IDs continuam visíveis: são o que torna o log útil.
	assert.Contains(t, out, "u-123")

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))
	assert.Equal(t, logging.Redacted, parsed["password"])
	assert.Equal(t, logging.Redacted, parsed["code"])
}

func TestRedigeDentroDeGrupos(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})
	lg.Info("evento", slog.Group("auth",
		slog.String("password", "abracadabra"),
		slog.String("user_id", "u-1"),
	))
	lg.With(slog.String("db_dsn", "postgres://u:p@h/db")).Info("outro")

	out := buf.String()
	assert.NotContains(t, out, "abracadabra")
	assert.NotContains(t, out, "postgres://u:p@h/db")
	assert.Contains(t, out, "u-1")
}

func TestRedigeGrupoInteiroComNomeSensivel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})
	lg.Info("evento", slog.Group("token", slog.String("valor", "tok-secreto")))

	assert.NotContains(t, buf.String(), "tok-secreto")
}

func TestNivelERespeitado(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "warn", Format: "text"})
	lg.Debug("debug")
	lg.Info("info")
	lg.Warn("warn")

	out := buf.String()
	assert.NotContains(t, out, "msg=debug")
	assert.NotContains(t, out, "msg=info")
	assert.Contains(t, out, "msg=warn")
}

func TestIsSensitiveKey(t *testing.T) {
	t.Parallel()

	sensiveis := []string{"password", "PASSWORD", "user_password", "code", "otpCode", "token", "TokenHash", "dsn", "cookie", "jwt_secret", "otp_pepper", "authorization"}
	for _, k := range sensiveis {
		assert.True(t, logging.IsSensitiveKey(k), "%q deveria ser sensível", k)
	}
	neutras := []string{"user_id", "household_id", "status", "method", "path", "duration_ms", "request_id", "ip"}
	for _, k := range neutras {
		assert.False(t, logging.IsSensitiveKey(k), "%q não deveria ser sensível", k)
	}
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	assert.Equal(t, slog.LevelDebug, logging.ParseLevel("debug"))
	assert.Equal(t, slog.LevelInfo, logging.ParseLevel("info"))
	assert.Equal(t, slog.LevelWarn, logging.ParseLevel("WARN"))
	assert.Equal(t, slog.LevelError, logging.ParseLevel("error"))
	assert.Equal(t, slog.LevelInfo, logging.ParseLevel("outro"))
}
