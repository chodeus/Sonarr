package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/users"
)

// Service wraps the users repo with the auth-specific operations the
// HTTP layer will need: create the singleton admin user, look up by
// username/identifier, authenticate a username+password, and (on
// successful authentication of a legacy SHA-256 row) lazily upgrade
// the row's hash to PBKDF2.
//
// The repo + service split mirrors C# UserService → IUserRepository:
// users.Repo owns persistence shape, this Service owns crypto +
// upgrade behaviour.
type Service struct {
	repo *users.Repo
}

// NewService binds a Service to an existing users.Repo. The Repo's
// lifecycle (DB ownership, close) is the caller's concern.
func NewService(repo *users.Repo) *Service {
	return &Service{repo: repo}
}

// ErrInvalidCredentials is returned by Authenticate when the
// username doesn't exist or the password doesn't match. Callers
// should NOT branch on which case — uniform failure reduces the
// account-existence side channel.
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// Add creates a new user row with the username lowercased
// (UserService.cs:46) and the password PBKDF2-hashed.
//
// The C# code generates Identifier with Guid.NewGuid(); the Go port
// uses 16 random bytes hex-encoded into the C# Guid string layout
// (8-4-4-4-12), so existing DBs that key the auth cookie on
// Identifier-string match continue to work.
func (s *Service) Add(ctx context.Context, username, password string) (*users.User, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("auth: username and password are required")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	id, err := newGuid()
	if err != nil {
		return nil, err
	}
	u := &users.User{
		Identifier: id,
		Username:   strings.ToLower(username),
		Password:   hash.Hash,
		Salt:       &hash.Salt,
		Iterations: &hash.Iterations,
	}
	return s.repo.Insert(ctx, u)
}

// Upsert is the seed-from-XML path's helper. Mirrors
// UserService.cs:59-76 — if a user already exists, its password is
// re-hashed only when the candidate password differs from the stored
// raw value (the C# code carries the seed plaintext verbatim, so
// "user.Password != password" detects a change). The Go port reuses
// the same shape so the behaviour is identical when the seed config
// is unchanged across restarts.
func (s *Service) Upsert(ctx context.Context, username, password string) (*users.User, error) {
	existing, err := s.findOne(ctx)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return s.Add(ctx, username, password)
	}
	// C# behaviour: replace hash only when stored Password differs from
	// the raw candidate. After an Add, Password is the PBKDF2 base64
	// hash, which never equals the plaintext, so this branch fires on
	// the first Upsert. On subsequent Upsert calls with the same
	// password, both sides remain whatever they previously were and
	// the branch doesn't fire again *unless* the password value
	// actually changes.
	if existing.Password != password {
		hash, err := HashPassword(password)
		if err != nil {
			return nil, err
		}
		existing.Password = hash.Hash
		existing.Salt = &hash.Salt
		existing.Iterations = &hash.Iterations
	}
	existing.Username = strings.ToLower(username)
	if err := s.repo.Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// Authenticate verifies a username+password pair. Returns the User on
// success. On a successful login of a legacy SHA-256-hashed row, the
// row is rehashed with PBKDF2 and persisted before returning — the
// caller observes the upgraded row.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*users.User, error) {
	if username == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	u, err := s.repo.GetByUsername(ctx, strings.ToLower(username))
	switch {
	case errors.Is(err, users.ErrNotFound):
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, err
	}

	// Legacy path: Salt is NULL → Password column holds a SHA-256 hex
	// hash. Compare; on match, upgrade to PBKDF2.
	if u.Salt == nil || *u.Salt == "" {
		if u.Password != SHA256Hex(password) {
			return nil, ErrInvalidCredentials
		}
		hash, err := HashPassword(password)
		if err != nil {
			return nil, err
		}
		u.Password = hash.Hash
		u.Salt = &hash.Salt
		u.Iterations = &hash.Iterations
		if err := s.repo.Update(ctx, u); err != nil {
			return nil, fmt.Errorf("auth: upgrade legacy hash: %w", err)
		}
		return u, nil
	}

	// Modern path: PBKDF2 verify.
	iters := pbkdf2Iterations
	if u.Iterations != nil {
		iters = *u.Iterations
	}
	if !VerifyPassword(Hash{Salt: *u.Salt, Iterations: iters, Hash: u.Password}, password) {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// FindByIdentifier returns the User whose Identifier (Guid) matches.
// Used on session-cookie validation: the cookie carries Identifier so
// a leaked or rebuilt DB won't auto-validate stale sessions across
// reinstalls. Returns ErrInvalidCredentials when no row matches —
// uniform failure mode with Authenticate.
func (s *Service) FindByIdentifier(ctx context.Context, identifier string) (*users.User, error) {
	u, err := s.repo.GetByIdentifier(ctx, identifier)
	switch {
	case errors.Is(err, users.ErrNotFound):
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, err
	}
	return u, nil
}

// findOne returns the singleton user row or (nil, nil) if no row
// exists. UserService.SingleOrDefault behaviour. We use All + first
// rather than a separate query because the table is at most one row
// in practice (UserService never adds more than one), and it lets
// the repo stay generic.
func (s *Service) findOne(ctx context.Context) (*users.User, error) {
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	u := rows[0]
	return &u, nil
}

// newGuid produces a string in the C# Guid.ToString("D") shape:
// `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`. The cookie path compares
// Identifier as opaque text, so the only requirement is "stable,
// 36 chars including hyphens, lowercase hex". 16 bytes from
// crypto/rand provide 128 bits — same entropy budget as Guid.NewGuid().
func newGuid() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth: read random guid: %w", err)
	}
	// Set version (random, RFC 4122 §4.4) and variant bits to match the
	// shape Guid.NewGuid() produces. Cookie-cookie validation does
	// byte-string compare so this isn't strictly required, but matching
	// the version bits keeps the value indistinguishable from C#
	// output for diagnostics.
	b[6] = (b[6] & 0x0F) | 0x40 // version 4
	b[8] = (b[8] & 0x3F) | 0x80 // RFC 4122 variant
	hexs := hex.EncodeToString(b[:])
	return hexs[0:8] + "-" + hexs[8:12] + "-" + hexs[12:16] + "-" + hexs[16:20] + "-" + hexs[20:32], nil
}
