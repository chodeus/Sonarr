// Package delayprofiles is the Go port of NzbDrone.Core.Profiles.Delay.
// DelayProfiles control how long Sonarr waits for a preferred-protocol
// release before falling back to the alternative. The schema mirrors
// what migrations 070, 097, 156, 182 produce: { Id, EnableUsenet,
// EnableTorrent, PreferredProtocol, UsenetDelay, TorrentDelay, Order,
// Tags, BypassIfHighestQuality, BypassIfAboveCustomFormatScore,
// MinimumCustomFormatScore (nullable) }.
//
// Persistence-layer notes (load-bearing patterns this package exercises):
//
//   - Bool columns round-trip through database/sql without any wrapper:
//     modernc/sqlite handles the INTEGER↔bool affinity, pgx handles
//     boolean↔bool natively. Just declare struct fields as bool.
//   - Tags is an embedded JSON int-array stored in the TEXT column "Tags".
//     Marshal/Unmarshal happens inside the Mapper's Bind/Scan funcs via
//     datastore.MarshalEmbedded / UnmarshalEmbedded. Empty slice serialises
//     as "[]"; nil serialises as "null" but the schema's NOT NULL would
//     reject that, so the Mapper coerces nil → [] on Bind.
//   - MinimumCustomFormatScore is C# int? — modeled as *int. The Mapper
//     converts to/from sql.NullInt64 at the database/sql boundary.
//   - "Order" is a SQL reserved keyword. Always quoted; the generic
//     repo.Mapper does this automatically by quoting every Columns
//     entry.
package delayprofiles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Profile is one persisted DelayProfile row.
type Profile struct {
	ID                             int   `json:"id"`
	EnableUsenet                   bool  `json:"enableUsenet"`
	EnableTorrent                  bool  `json:"enableTorrent"`
	PreferredProtocol              int   `json:"preferredProtocol"`
	UsenetDelay                    int   `json:"usenetDelay"`
	TorrentDelay                   int   `json:"torrentDelay"`
	Order                          int   `json:"order"`
	Tags                           []int `json:"tags"`
	BypassIfHighestQuality         bool  `json:"bypassIfHighestQuality"`
	BypassIfAboveCustomFormatScore bool  `json:"bypassIfAboveCustomFormatScore"`
	MinimumCustomFormatScore       *int  `json:"minimumCustomFormatScore,omitempty"`
}

// ErrNotFound is returned by Get when no row matches.
var ErrNotFound = errors.New("delayprofiles: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Profile]
}

var mapper = repo.Mapper[Profile]{
	Name:  "delayprofiles",
	Table: "DelayProfiles",
	Columns: []string{
		"EnableUsenet", "EnableTorrent", "PreferredProtocol",
		"UsenetDelay", "TorrentDelay", "Order", "Tags",
		"BypassIfHighestQuality", "BypassIfAboveCustomFormatScore",
		"MinimumCustomFormatScore",
	},
	OrderBy: "Order",
	Bind: func(p *Profile) []any {
		tags := p.Tags
		if tags == nil {
			tags = []int{} // schema is NOT NULL; persist [] not null.
		}
		tagsJSON, err := datastore.MarshalEmbedded(tags)
		if err != nil {
			// Marshal of []int can only fail on impossibly-deep recursion;
			// returning an obviously-broken value lets the SQL layer fail
			// with a clear "invalid JSON" error instead of panicking here.
			tagsJSON = `<marshal-error: ` + err.Error() + `>`
		}
		return []any{
			p.EnableUsenet, p.EnableTorrent, p.PreferredProtocol,
			p.UsenetDelay, p.TorrentDelay, p.Order, tagsJSON,
			p.BypassIfHighestQuality, p.BypassIfAboveCustomFormatScore,
			nullableInt(p.MinimumCustomFormatScore),
		}
	},
	Scan: func(p *Profile, s repo.RowScanner) error {
		var tagsJSON string
		var minScore sql.NullInt64
		if err := s.Scan(
			&p.ID, &p.EnableUsenet, &p.EnableTorrent, &p.PreferredProtocol,
			&p.UsenetDelay, &p.TorrentDelay, &p.Order, &tagsJSON,
			&p.BypassIfHighestQuality, &p.BypassIfAboveCustomFormatScore,
			&minScore,
		); err != nil {
			return err
		}
		if minScore.Valid {
			v := int(minScore.Int64)
			p.MinimumCustomFormatScore = &v
		}
		if err := datastore.UnmarshalEmbedded(tagsJSON, &p.Tags); err != nil {
			return fmt.Errorf("unmarshal tags: %w", err)
		}
		return nil
	},
	GetID: func(p *Profile) int { return p.ID },
	SetID: func(p *Profile, id int) { p.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// Count returns the number of persisted DelayProfile rows. Mirrors the
// C# `IDelayProfileRepository.Count()` used by `DelayProfileService.Add`
// to assign the next Order value.
func (r *Repo) Count(ctx context.Context) (int, error) {
	var n int
	if err := r.DB().SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM "DelayProfiles"`).Scan(&n); err != nil {
		return 0, fmt.Errorf("delayprofiles: count: %w", err)
	}
	return n, nil
}

// nullableInt converts a *int into a value the database/sql layer
// understands as either NULL or the underlying int.
func nullableInt(p *int) any {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}
