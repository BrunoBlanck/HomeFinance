package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parâmetros mínimos de Argon2id exigidos por docs/SEGURANCA.md §1.
const (
	MinArgon2MemoryKiB   uint32 = 64 * 1024 // 64 MiB
	MinArgon2Iterations  uint32 = 3
	MinArgon2Parallelism uint8  = 2
	Argon2SaltLen        uint32 = 16
	Argon2KeyLen         uint32 = 32
)

// Faixa aceita para o tamanho da chave derivada guardada no PHC.
const (
	minKeyLen = 16
	maxKeyLen = 64
)

// Argon2Params descreve o custo do hash.
type Argon2Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	// MaxConcurrent limita quantos hashes rodam ao mesmo tempo.
	MaxConcurrent int
}

// PasswordHasher aplica Argon2id.
//
// D15 da spec 0001 — o SEMÁFORO não é detalhe de performance, é defesa: cada
// verificação reserva 64 MiB. Sem limite de concorrência, 200 tentativas de
// login simultâneas pedem 12,8 GiB e o processo morre. O canal com buffer
// resolve sem trazer x/sync/semaphore para o projeto.
type PasswordHasher struct {
	params    Argon2Params
	sem       chan struct{}
	dummyHash string
}

// NewPasswordHasher monta o hasher, elevando qualquer parâmetro abaixo do
// mínimo (nunca aceitamos custo mais fraco que o do documento de segurança).
func NewPasswordHasher(p Argon2Params) (*PasswordHasher, error) {
	if p.MemoryKiB < MinArgon2MemoryKiB {
		p.MemoryKiB = MinArgon2MemoryKiB
	}
	if p.Iterations < MinArgon2Iterations {
		p.Iterations = MinArgon2Iterations
	}
	if p.Parallelism < MinArgon2Parallelism {
		p.Parallelism = MinArgon2Parallelism
	}
	if p.MaxConcurrent < 1 {
		p.MaxConcurrent = 1
	}

	h := &PasswordHasher{
		params: p,
		sem:    make(chan struct{}, p.MaxConcurrent),
	}

	// Hash-isca calculado uma vez no boot, com os MESMOS parâmetros: é o que
	// permite gastar o mesmo tempo de CPU quando o e-mail não existe
	// (grupo E da §3.12).
	dummy, err := h.Hash(context.Background(), "hash-isca-nunca-corresponde-a-nenhuma-senha-real")
	if err != nil {
		return nil, fmt.Errorf("preparando hash-isca: %w", err)
	}
	h.dummyHash = dummy
	return h, nil
}

// Params devolve os parâmetros efetivos.
func (h *PasswordHasher) Params() Argon2Params { return h.params }

func (h *PasswordHasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("aguardando vaga de hash: %w", ctx.Err())
	}
}

func (h *PasswordHasher) release() { <-h.sem }

// Hash devolve a senha no formato PHC do Argon2id.
func (h *PasswordHasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()

	salt := make([]byte, Argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("gerando salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.MemoryKiB, h.params.Parallelism, Argon2KeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.params.MemoryKiB, h.params.Iterations, h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify confere a senha contra o hash PHC guardado.
//
// Devolve ErrPasswordMismatch quando não confere e ErrInvalidHash quando o
// registro está corrompido — o chamador trata os dois como credencial
// inválida, sem distinguir para o cliente.
func (h *PasswordHasher) Verify(ctx context.Context, password, encoded string) error {
	params, salt, want, err := decodePHC(encoded)
	if err != nil {
		return err
	}
	if err := h.acquire(ctx); err != nil {
		return err
	}
	defer h.release()

	// decodePHC já garantiu 16 <= len(want) <= 64; a checagem repetida deixa
	// a conversão para uint32 obviamente segura para quem lê e para o SAST.
	keyLen := len(want)
	if keyLen < minKeyLen || keyLen > maxKeyLen {
		return ErrInvalidHash
	}

	got := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism, uint32(keyLen))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// VerifyDummy gasta exatamente o mesmo tempo de CPU de um Verify real.
//
// Chamado quando o e-mail do login não existe. Sem isso, o tempo de resposta
// separa "conta inexistente" de "senha errada" e o grupo E da §3.12 cai —
// padTo sozinho não resolveria, porque o Argon2id pode passar do piso.
func (h *PasswordHasher) VerifyDummy(ctx context.Context, password string) {
	_ = h.Verify(ctx, password, h.dummyHash)
}

// NeedsRehash informa se o hash guardado usa custo menor que o atual.
func (h *PasswordHasher) NeedsRehash(encoded string) bool {
	params, _, _, err := decodePHC(encoded)
	if err != nil {
		return true
	}
	return params.MemoryKiB < h.params.MemoryKiB ||
		params.Iterations < h.params.Iterations ||
		params.Parallelism < h.params.Parallelism
}

// decodePHC lê "$argon2id$v=19$m=...,t=...,p=...$salt$hash".
func decodePHC(encoded string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 || parts[0] != "" {
		return Argon2Params{}, nil, nil, ErrInvalidHash
	}
	if parts[1] != "argon2id" {
		// Recusamos argon2i e argon2d de propósito: só o híbrido id tem
		// resistência conjunta a canal lateral e a GPU.
		return Argon2Params{}, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Argon2Params{}, nil, nil, ErrInvalidHash
	}

	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Iterations, &p.Parallelism); err != nil {
		return Argon2Params{}, nil, nil, ErrInvalidHash
	}
	// Teto defensivo: um registro corrompido (ou adulterado num backup) com
	// m=4194304 viraria 4 GiB de alocação por verificação.
	if p.MemoryKiB == 0 || p.MemoryKiB > 1<<21 || p.Iterations == 0 || p.Iterations > 32 || p.Parallelism == 0 {
		return Argon2Params{}, nil, nil, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return Argon2Params{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < minKeyLen || len(key) > maxKeyLen {
		return Argon2Params{}, nil, nil, ErrInvalidHash
	}
	return p, salt, key, nil
}
