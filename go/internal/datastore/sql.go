package datastore

import "fmt"

// Placeholder returns the n-th (1-indexed) parameter placeholder for the
// given engine. SQLite uses '?' (positional), Postgres uses '$N'.
//
// Repository code reuses this helper so per-engine query construction
// stays consistent and tested in one place. Going through fmt.Sprintf
// once per query is fine; query strings are built outside hot paths.
func Placeholder(engine Engine, n int) string {
	if engine == EnginePostgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}
