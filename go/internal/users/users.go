// Package users is the Go port of NzbDrone.Core.Authentication.User
// persistence. The Users table holds the (effectively-singleton) admin
// account: a single row created on first form-auth login and consulted
// on every subsequent authenticated request.
//
// Schema at HEAD (v230): Id, Identifier TEXT NOT NULL UNIQUE,
// Username TEXT NOT NULL UNIQUE, Password TEXT NOT NULL, Salt TEXT
// nullable, Iterations INTEGER nullable.
//
// Salt + Iterations carry the lazy SHA-256 → PBKDF2 upgrade-on-login
// state documented in analysis/subsystems/authentication-security.md.
// Old rows have NULL Salt+Iterations and a SHA-256 Password; on the next
// successful login the service re-hashes with PBKDF2 and writes a non-
// NULL Salt + Iterations alongside the new Password. Modeling them as
// *string and *int preserves the schema's NULL semantics so the auth
// service can branch on nil-vs-set rather than a sentinel value.
//
// Identifier is a Guid in the C# class; modeled here as plain string
// because Go's database/sql layer treats UUIDs as opaque text and the
// auth path does not need uuid.Parse — it compares byte-for-byte.
package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// User is one Users row.
type User struct {
	ID         int     `json:"id"`
	Identifier string  `json:"identifier"`
	Username   string  `json:"username"`
	Password   string  `json:"password"`
	Salt       *string `json:"salt"`
	Iterations *int    `json:"iterations"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("users: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[User]
}

var mapper = repo.Mapper[User]{
	Name:    "users",
	Table:   "Users",
	Columns: []string{"Identifier", "Username", "Password", "Salt", "Iterations"},
	OrderBy: "Id",
	Bind: func(u *User) []any {
		return []any{u.Identifier, u.Username, u.Password, nullableString(u.Salt), nullableInt(u.Iterations)}
	},
	Scan: func(u *User, s repo.RowScanner) error {
		var salt sql.NullString
		var iterations sql.NullInt64
		if err := s.Scan(&u.ID, &u.Identifier, &u.Username, &u.Password, &salt, &iterations); err != nil {
			return err
		}
		if salt.Valid {
			v := salt.String
			u.Salt = &v
		}
		if iterations.Valid {
			v := int(iterations.Int64)
			u.Iterations = &v
		}
		return nil
	},
	GetID: func(u *User) int { return u.ID },
	SetID: func(u *User, id int) { u.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// GetByUsername returns the User with the given Username or ErrNotFound.
// Username is UNIQUE so at most one row matches. This is the auth path's
// canonical lookup on a login attempt.
func (r *Repo) GetByUsername(ctx context.Context, username string) (*User, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Identifier","Username","Password","Salt","Iterations" FROM "Users" WHERE "Username" = %s`,
		datastore.Placeholder(db.Engine, 1))
	return r.scanByQuery(ctx, q, username, "getByUsername", username)
}

// GetByIdentifier returns the User with the given Identifier (Guid) or
// ErrNotFound. Used on session-cookie validation: the cookie carries the
// Identifier, not the Id, so a leaked or rebuilt DB won't auto-validate
// stale sessions.
func (r *Repo) GetByIdentifier(ctx context.Context, identifier string) (*User, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Identifier","Username","Password","Salt","Iterations" FROM "Users" WHERE "Identifier" = %s`,
		datastore.Placeholder(db.Engine, 1))
	return r.scanByQuery(ctx, q, identifier, "getByIdentifier", identifier)
}

func (r *Repo) scanByQuery(ctx context.Context, query string, arg any, op string, opArg any) (*User, error) {
	db := r.DB()
	var u User
	var salt sql.NullString
	var iterations sql.NullInt64
	switch err := db.SQL.QueryRowContext(ctx, query, arg).Scan(
		&u.ID, &u.Identifier, &u.Username, &u.Password, &salt, &iterations,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("users: %s(%v): %w", op, opArg, err)
	}
	if salt.Valid {
		v := salt.String
		u.Salt = &v
	}
	if iterations.Valid {
		v := int(iterations.Int64)
		u.Iterations = &v
	}
	return &u, nil
}

func nullableString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func nullableInt(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}
