package autotagging

import (
	"context"
	"fmt"
)

// Typed-spec loader. Converts persisted `AutoTag` rows (where
// `Specifications` is opaque JSON text) into in-memory `*Tag`
// values with typed `[]Specification` slices. The future
// AutoTaggingService delegates to this loader on its initial
// hydration.

// LoadFromRow converts a single persisted AutoTag row to a typed
// Tag. The Specifications JSON is decoded via UnmarshalSpecs.
//
// Returns an error if the JSON is malformed or contains an unknown
// discriminator. The persisted row is NOT mutated.
func LoadFromRow(row *AutoTag) (*Tag, error) {
	if row == nil {
		return nil, nil
	}
	specs, err := UnmarshalSpecs([]byte(row.Specifications))
	if err != nil {
		return nil, fmt.Errorf("autotagging: load %d (%q): %w", row.ID, row.Name, err)
	}
	return &Tag{
		ID:                      row.ID,
		Name:                    row.Name,
		RemoveTagsAutomatically: row.RemoveTagsAutomatically,
		Tags:                    row.Tags,
		Specifications:          specs,
	}, nil
}

// LoadAll loads every AutoTag row from the repo and converts each
// to a typed Tag. Failures on individual rows are surfaced as the
// first error (no per-row partial success); callers that need
// best-effort loading can iterate Repo.All themselves.
func LoadAll(ctx context.Context, r *Repo) ([]*Tag, error) {
	rows, err := r.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("autotagging: load all: %w", err)
	}
	out := make([]*Tag, 0, len(rows))
	for i := range rows {
		t, err := LoadFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
