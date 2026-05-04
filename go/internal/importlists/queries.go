package importlists

import (
	"context"
	"fmt"
)

// AllQualityProfileIDs returns the QualityProfileId of every persisted
// ImportListDefinition. Used by
// `qualityprofiles.Service.Delete` to validate "is this profile in
// use by an import list?" — satisfies the
// `qualityprofiles.ImportListProfileChecker` interface signature
// exactly.
//
// Mirrors the relevant slice of the C#
// `_importListFactory.All().Any(c => c.QualityProfileId == id)`
// check at `QualityProfileService.cs:67` — but instead of dragging in
// the full ImportListDefinition typed view, the projection is the
// minimal int slice the validator actually needs.
//
// Returns an empty slice when no rows exist.
func (r *Repo) AllQualityProfileIDs(ctx context.Context) ([]int, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, `SELECT "QualityProfileId" FROM "ImportLists"`)
	if err != nil {
		return nil, fmt.Errorf("importlists: AllQualityProfileIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]int, 0)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("importlists: AllQualityProfileIDs: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("importlists: AllQualityProfileIDs: rows: %w", err)
	}
	return out, nil
}
