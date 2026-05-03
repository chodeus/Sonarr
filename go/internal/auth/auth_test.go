package auth_test

import (
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/auth"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/users"
)

func TestHashPassword_RoundTrip(t *testing.T) {
	t.Parallel()
	h, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if h.Salt == "" || h.Hash == "" || h.Iterations != 10000 {
		t.Errorf("hash shape: %+v", h)
	}
	if !auth.VerifyPassword(h, "hunter2") {
		t.Errorf("VerifyPassword(correct) = false")
	}
	if auth.VerifyPassword(h, "wrong") {
		t.Errorf("VerifyPassword(wrong) = true")
	}
}

func TestHashPassword_FreshSaltEachTime(t *testing.T) {
	t.Parallel()
	a, _ := auth.HashPassword("same")
	b, _ := auth.HashPassword("same")
	if a.Salt == b.Salt {
		t.Errorf("salt not random: %q", a.Salt)
	}
	if a.Hash == b.Hash {
		t.Errorf("hash collision on fresh salts")
	}
}

func TestVerifyPassword_RejectsTamperedHash(t *testing.T) {
	t.Parallel()
	h, _ := auth.HashPassword("right")
	tampered := h
	if len(tampered.Hash) > 1 {
		tampered.Hash = "A" + tampered.Hash[1:]
	}
	if auth.VerifyPassword(tampered, "right") {
		t.Errorf("VerifyPassword accepted tampered hash")
	}
}

func TestSHA256Hex_ParityWithCSharp(t *testing.T) {
	t.Parallel()
	// SHA-256 of "hunter2" — verified independently against
	// `echo -n hunter2 | sha256sum`.
	got := auth.SHA256Hex("hunter2")
	want := "f52fbd32b2b3b86ff88ef6c490628285f482af15ddcb29541f94bcf526a3f6c7"
	if got != want {
		t.Errorf("SHA256Hex(hunter2) = %q, want %q", got, want)
	}
}

func TestGenerateAPIKey_Length(t *testing.T) {
	t.Parallel()
	k, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if len(k) != 64 { // 32 bytes hex-encoded
		t.Errorf("len = %d, want 64", len(k))
	}
}

func TestGenerateAPIKey_Unique(t *testing.T) {
	t.Parallel()
	seen := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		k, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey: %v", err)
		}
		if _, dup := seen[k]; dup {
			t.Fatalf("duplicate API key: %s", k)
		}
		seen[k] = struct{}{}
	}
}

func TestEqualAPIKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", true},
		{"abc", "abcd", false},
		{"abc", "abd", false},
		{"", "abc", false},
		{"abc", "", false},
		{"", "", false},
		{strings.Repeat("a", 64), strings.Repeat("a", 64), true},
	}
	for _, c := range cases {
		if got := auth.EqualAPIKey(c.a, c.b); got != c.want {
			t.Errorf("EqualAPIKey(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestDeriveCookieName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", "SonarrAuth"},
		{"Sonarr", "SonarrAuth"},
		{"Sonarr 4K", "Sonarr4KAuth"},
		{"---", "SonarrAuth"}, // all-stripped → fallback
		{"Café", "CafeAuth"},  // diacritic strip
		{"Sönarr-4K", "Sonarr4KAuth"},
		{"   ", "SonarrAuth"}, // whitespace-only → fallback
		{"my_instance.42", "myinstance42Auth"},
	}
	for _, c := range cases {
		if got := auth.DeriveCookieName(c.in); got != c.want {
			t.Errorf("DeriveCookieName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- Service tests against a real DB ---

func runServiceSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := users.NewRepo(db)
	if err != nil {
		t.Fatalf("users.NewRepo: %v", err)
	}
	svc := auth.NewService(repo)

	// Add lowercases the username and persists a PBKDF2 row.
	u, err := svc.Add(t.Context(), "Admin", "hunter2")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if u.Username != "admin" {
		t.Errorf("Username not lowercased: %q", u.Username)
	}
	if u.Salt == nil || u.Iterations == nil || *u.Iterations != 10000 {
		t.Errorf("PBKDF2 fields missing: %+v", u)
	}
	if len(u.Identifier) != 36 || strings.Count(u.Identifier, "-") != 4 {
		t.Errorf("Identifier shape: %q", u.Identifier)
	}

	// Authenticate happy path — also case-insensitive on username.
	got, err := svc.Authenticate(t.Context(), "ADMIN", "hunter2")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("Authenticate returned wrong user: %d vs %d", got.ID, u.ID)
	}

	// Authenticate failures: wrong pw, unknown user, empty input — all
	// return the same error (no enumeration side channel).
	for _, c := range []struct{ user, pw string }{
		{"admin", "wrong"},
		{"unknown", "hunter2"},
		{"", "hunter2"},
		{"admin", ""},
	} {
		if _, err := svc.Authenticate(t.Context(), c.user, c.pw); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("Authenticate(%q,%q): %v, want ErrInvalidCredentials", c.user, c.pw, err)
		}
	}

	// FindByIdentifier round-trips the cookie path.
	byID, err := svc.FindByIdentifier(t.Context(), u.Identifier)
	if err != nil {
		t.Fatalf("FindByIdentifier: %v", err)
	}
	if byID.ID != u.ID {
		t.Errorf("FindByIdentifier returned wrong user")
	}
	if _, err := svc.FindByIdentifier(t.Context(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("FindByIdentifier(missing): %v, want ErrInvalidCredentials", err)
	}
}

func TestService_SQLite(t *testing.T) {
	t.Parallel()
	runServiceSuite(t, datastoretest.OpenSQLite(t))
}

func TestService_Postgres(t *testing.T) {
	runServiceSuite(t, datastoretest.OpenPostgres(t))
}

// TestService_LegacySHA256Upgrade exercises the lazy SHA-256 → PBKDF2
// upgrade path. Seeds a row with Salt=NULL and a SHA-256-hex Password,
// authenticates, and verifies the row is now PBKDF2-shaped.
func TestService_LegacySHA256Upgrade(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, err := users.NewRepo(db)
	if err != nil {
		t.Fatalf("users.NewRepo: %v", err)
	}

	// Seed a legacy row by hand. Salt/Iterations stay nil.
	legacy := &users.User{
		Identifier: "0000aaaa-1111-2222-3333-44445555ffff",
		Username:   "legacy",
		Password:   auth.SHA256Hex("hunter2"),
	}
	if _, err := repo.Insert(t.Context(), legacy); err != nil {
		t.Fatalf("Insert legacy: %v", err)
	}

	svc := auth.NewService(repo)
	upgraded, err := svc.Authenticate(t.Context(), "legacy", "hunter2")
	if err != nil {
		t.Fatalf("Authenticate (legacy): %v", err)
	}
	if upgraded.Salt == nil || upgraded.Iterations == nil {
		t.Errorf("legacy row not upgraded: %+v", upgraded)
	}
	if upgraded.Password == auth.SHA256Hex("hunter2") {
		t.Errorf("Password column still SHA-256 after upgrade: %q", upgraded.Password)
	}

	// Wrong password against legacy row stays rejected.
	if _, err := svc.Authenticate(t.Context(), "legacy", "wrong"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("Authenticate(legacy,wrong): %v", err)
	}

	// Re-read from DB and confirm the upgraded shape persists.
	persisted, err := repo.GetByUsername(t.Context(), "legacy")
	if err != nil {
		t.Fatalf("GetByUsername post-upgrade: %v", err)
	}
	if persisted.Salt == nil || persisted.Iterations == nil || *persisted.Iterations != 10000 {
		t.Errorf("upgrade not persisted: %+v", persisted)
	}
	// And the new PBKDF2 hash is verifiable on its own.
	h := auth.Hash{Salt: *persisted.Salt, Iterations: *persisted.Iterations, Hash: persisted.Password}
	if !auth.VerifyPassword(h, "hunter2") {
		t.Errorf("PBKDF2 verify against persisted row failed")
	}
}

// TestService_UpsertCreatesThenUpdates exercises Upsert's
// create-or-rehash behaviour against the actual C# UserService.Upsert
// shape. First call creates the singleton row. Subsequent calls
// always rehash because the C# `if (user.Password != password)`
// guard compares the stored PBKDF2 base64 hash to the candidate
// plaintext — those are never equal post-Add, so every Upsert past
// the first rotates the salt. Sonarr v3/v4/v5 ship this behaviour;
// the Go port mirrors it under the conversion rules. (A future
// non-parity improvement could verify the candidate against the
// stored hash before deciding to rehash.)
func TestService_UpsertCreatesThenUpdates(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, err := users.NewRepo(db)
	if err != nil {
		t.Fatalf("users.NewRepo: %v", err)
	}
	svc := auth.NewService(repo)

	first, err := svc.Upsert(t.Context(), "Admin", "p1")
	if err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if first.Username != "admin" {
		t.Errorf("first.Username = %q", first.Username)
	}
	saltAfterFirst := *first.Salt

	// Subsequent Upsert with the same password still rotates the salt
	// (C# parity, not a Go-port bug). Username takes the new value.
	second, err := svc.Upsert(t.Context(), "ADMINISTRATOR", "p1")
	if err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	if second.Username != "administrator" {
		t.Errorf("second.Username = %q", second.Username)
	}
	if second.Salt == nil || *second.Salt == saltAfterFirst {
		t.Errorf("salt did not rotate on same-password Upsert (C# parity expected): before=%q after=%v", saltAfterFirst, second.Salt)
	}
	// Authenticate against the rotated hash still works.
	if _, err := svc.Authenticate(t.Context(), "administrator", "p1"); err != nil {
		t.Errorf("Authenticate post-Upsert(same pw): %v", err)
	}

	// Different password — hash also rotates.
	saltAfterSecond := *second.Salt
	third, err := svc.Upsert(t.Context(), "ADMINISTRATOR", "p2")
	if err != nil {
		t.Fatalf("Upsert third: %v", err)
	}
	if third.Salt == nil || *third.Salt == saltAfterSecond {
		t.Errorf("salt did not rotate on password change")
	}
	if _, err := svc.Authenticate(t.Context(), "administrator", "p2"); err != nil {
		t.Errorf("Authenticate post-rehash: %v", err)
	}
	if _, err := svc.Authenticate(t.Context(), "administrator", "p1"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("old password still works post-rehash: %v", err)
	}
}
