package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/maintain"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// datastats serves the dataset-accounting snapshot (storage-permanence wave):
// per-table row counts + time spans and DB/WAL file sizes, so data growth is
// visible and provable on /quality. Read-only; gated like every other read
// via requiresAuth (public when SIGNALDECK_PUBLIC_READS=true).
//
// Tiered-storage wave: it also reports the cold-archive size on disk (so the
// "nothing is deleted, and it's bounded" claim is provable) and the ACTIVE
// tiered-retention windows (so operators can see exactly how long each tier
// stays hot before archive+prune). The store layer leaves these fields zero;
// the handler fills them because it owns the archive path + retention policy.
func (d Deps) datastats(w http.ResponseWriter, r *http.Request) {
	stats, err := d.St.DataStats(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	// ArchiveBytes is the PROOF that pruned data was archived rather than lost.
	// A discarded DirSize error left it 0, which /quality renders as
	// "cold archive: 0 B" -- i.e. the evidence of a catastrophe, produced by a
	// failed directory walk. Nil means unknown; 0 means measured and empty.
	if bytes, err := archive.DirSize(archive.Dir(d.Cfg.DBPath)); err == nil {
		stats.ArchiveBytes = bytes
	} else {
		stats.ArchiveBytesUnknown = true
	}
	stats.Retention = &store.RetentionWindows{
		SnapshotsHours: maintain.RetentionSnapsHours(),
		Bars1mDays:     maintain.Retention1mDays(),
		Bars1hDays:     maintain.Retention1hDays(),
		DailyForever:   true,
	}
	writeJSON(w, stats)
}
