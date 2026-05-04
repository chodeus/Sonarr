package customformats

import (
	"context"
	"fmt"
)

// Typed-spec loader. Converts persisted `CustomFormat` rows (where
// `Specifications` is opaque JSON text) into in-memory `*Format`
// values with typed `[]Specification` slices. The future
// CustomFormatService delegates to this loader on its initial
// hydration.

// LoadFromRow converts a single persisted CustomFormat row to a
// typed Format. The Specifications JSON is decoded via
// UnmarshalSpecs.
//
// Returns an error if the JSON is malformed or contains an unknown
// discriminator. The persisted row is NOT mutated.
func LoadFromRow(row *CustomFormat) (*Format, error) {
	if row == nil {
		return nil, nil
	}
	specs, err := UnmarshalSpecs([]byte(row.Specifications))
	if err != nil {
		return nil, fmt.Errorf("customformats: load %d (%q): %w", row.ID, row.Name, err)
	}
	return &Format{
		ID:                              row.ID,
		Name:                            row.Name,
		IncludeCustomFormatWhenRenaming: row.IncludeCustomFormatWhenRenaming,
		Specifications:                  specs,
	}, nil
}

// LoadAll loads every CustomFormat row from the repo and converts
// each to a typed Format. Failures on individual rows are surfaced
// as the first error.
func LoadAll(ctx context.Context, r *Repo) ([]*Format, error) {
	rows, err := r.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("customformats: load all: %w", err)
	}
	out := make([]*Format, 0, len(rows))
	for i := range rows {
		f, err := LoadFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}
