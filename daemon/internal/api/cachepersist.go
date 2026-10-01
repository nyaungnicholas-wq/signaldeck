package api

// Last-good-body persistence for the caches a member or /proof page reads
// first after a restart (step 4, 2026-10-01).
//
// The daemon restarts often (33 starts in 7 days, 16 on one deploy day) and
// every cache here lived only in memory, so each boot made the next visitor pay
// a cold build: /api/track-record measured 129.4s, /api/ledger/verify 2-36s.
// Each successful build of a persisted cache is now also written to
// <data dir>/apicache/<db>.<name>.f<format>.json (temp file + rename, so a
// reader never sees half a file), and the first read of that key after a boot
// serves it as STALE while the rebuild runs. The body carries its own computedAt, so a page
// can show how old it is.
//
// The file records the in-memory key it was built under, and its NAME carries
// the payload's persist format (cacheFile): a body is served only to that same
// key, only by a build whose format constant for that cache is the same, and
// only while it is under persistMaxAge old. The format is per cache, declared
// next to each builder, and bumped when that payload's shape changes. The
// BUILD is deliberately not part of it: every deploy is a restart, and keying on
// the build cold-started track-record, regimes and the volatility record on
// exactly the restart this file exists for. That is safe for the licence line
// because no member or anonymous strip is baked into a persisted payload: the
// regimes crypto filter (withoutCryptoForecasts) and the track-record thin-mean
// strip (withoutThinReturnMeans) run on every READ, on the typed build and on
// the decoded JSON a persisted payload comes back as, and the volatility record
// is one body for every caller. A body from a previous process is also only
// ever served for a bounded time (fromDiskMaxStale, slowcache.go), and the
// ledger verify is not persisted at all: it is a proof claim (ledger.go).

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// persistedBody is the on-disk record.
type persistedBody struct {
	Key        string          `json:"key"`
	ComputedAt time.Time       `json:"computedAt"`
	Body       json.RawMessage `json:"body"`
}

// persistMaxAge is the oldest persisted body a boot will serve. Past it the
// key builds cold: a day-old answer presented as the current one is worse than
// a "warming" while the real one builds.
const persistMaxAge = 24 * time.Hour

// cacheFile is where cache `name` persists for this daemon's database: next to
// it, the way backups/ and health.json are, as <db>.<name>.f<format>.json. A
// build with another format for that cache reads another file, so a payload of
// a shape it does not know is never served to it. "" (no DBPath, as in most
// tests) keeps the cache in memory only.
func (d Deps) cacheFile(name string, format int) string {
	if d.Cfg.DBPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(d.Cfg.DBPath), "apicache",
		filepath.Base(d.Cfg.DBPath)+"."+name+".f"+strconv.Itoa(format)+".json")
}

// persistBody writes body atomically. A failure costs only the next boot's warm
// start, so it is logged, never returned.
func persistBody(file, key string, at time.Time, body []byte) {
	if file == "" || len(body) == 0 {
		return
	}
	b, err := json.Marshal(persistedBody{Key: key, ComputedAt: at.UTC(), Body: body})
	if err == nil {
		err = writeFileAtomic(file, b)
	}
	if err != nil {
		slog.Warn("cache persist failed; the next boot builds this cache cold", "file", file, "err", err)
	}
}

// persistPayload is persistBody for a swrCache payload.
func persistPayload(file, key string, at time.Time, p map[string]any) {
	if file == "" {
		return
	}
	b, err := json.Marshal(p)
	if err != nil {
		slog.Warn("cache persist failed; the next boot builds this cache cold", "file", file, "err", err)
		return
	}
	persistBody(file, key, at, b)
}

func writeFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) //nolint:errcheck // already gone once renamed
	if _, err := f.Write(b); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// loadPersistedBody returns the body persisted for exactly this key, if it is
// younger than persistMaxAge. Anything else is a cold build.
func loadPersistedBody(file, key string) ([]byte, time.Time, bool) {
	if file == "" {
		return nil, time.Time{}, false
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, time.Time{}, false
	}
	var pb persistedBody
	if json.Unmarshal(raw, &pb) != nil || pb.Key != key || len(pb.Body) == 0 || pb.ComputedAt.IsZero() ||
		time.Since(pb.ComputedAt) > persistMaxAge {
		return nil, time.Time{}, false
	}
	return pb.Body, pb.ComputedAt, true
}

// loadPersistedPayload decodes the persisted body into the payload form.
// Numbers stay json.Number, so they re-encode exactly as they were written.
func loadPersistedPayload(file, key string) (map[string]any, time.Time, bool) {
	body, at, ok := loadPersistedBody(file, key)
	if !ok {
		return nil, time.Time{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var p map[string]any
	if dec.Decode(&p) != nil || p == nil {
		return nil, time.Time{}, false
	}
	return p, at, true
}
