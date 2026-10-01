package api

// Last-good-body persistence for the caches a member or /proof page reads
// first after a restart (step 4, 2026-10-01).
//
// The daemon restarts often (33 starts in 7 days, 16 on one deploy day) and
// every cache here lived only in memory, so each boot made the next visitor pay
// a cold build: /api/track-record measured 129.4s, /api/ledger/verify 2-36s.
// Each successful build of a persisted cache is now also written to
// <data dir>/apicache/<db>.<name>.json (temp file + rename, so a reader never
// sees half a file), and the first read of that key after a boot serves it as
// STALE while the rebuild runs. The body carries its own computedAt, so a page
// can show how old it is.
//
// The file records the in-memory key it was built under and the BUILD that
// wrote it, and is served only to that same key in that same build, and only
// while it is under persistMaxAge old (review #7). The key alone could not tell
// processes apart: CacheKey is a per-process counter that is "st2" on every boot
// of the daemon, so a deploy that changed how a payload is graded served the
// previous binary's body as current, indefinitely if the new build kept failing.
// The ledger verify is not persisted at all: it is a proof claim (ledger.go).

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
)

// persistedBody is the on-disk record.
type persistedBody struct {
	Key        string          `json:"key"`
	Build      string          `json:"build"`
	ComputedAt time.Time       `json:"computedAt"`
	Body       json.RawMessage `json:"body"`
}

// persistMaxAge is the oldest persisted body a boot will serve. Past it the
// key builds cold: a day-old answer presented as the current one is worse than
// a "warming" while the real one builds.
const persistMaxAge = 24 * time.Hour

// persistBuild identifies the running binary. A var only so tests can play
// another build.
var persistBuild = buildIdentity()

// buildIdentity is the VCS revision the binary was built from ("+dirty" when
// the tree was modified). A dirty or unstamped build does not pin the code, so
// the executable's mtime is added; "" (nothing identifies it) turns
// persistence off.
func buildIdentity() string {
	id := lineage.RevisionStamp()
	if id == "" || strings.HasSuffix(id, "+dirty") {
		exe, err := os.Executable()
		if err != nil {
			return ""
		}
		fi, err := os.Stat(exe)
		if err != nil {
			return ""
		}
		id += "@" + fi.ModTime().UTC().Format(time.RFC3339Nano)
	}
	return id
}

// cacheFile is where cache `name` persists for this daemon's database: next to
// it, the way backups/ and health.json are. "" (no DBPath, as in most tests)
// keeps the cache in memory only.
func (d Deps) cacheFile(name string) string {
	if d.Cfg.DBPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(d.Cfg.DBPath), "apicache", filepath.Base(d.Cfg.DBPath)+"."+name+".json")
}

// persistBody writes body atomically. A failure costs only the next boot's warm
// start, so it is logged, never returned.
func persistBody(file, key string, at time.Time, body []byte) {
	if file == "" || len(body) == 0 || persistBuild == "" {
		return
	}
	b, err := json.Marshal(persistedBody{Key: key, Build: persistBuild, ComputedAt: at.UTC(), Body: body})
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

// loadPersistedBody returns the body persisted for exactly this key by exactly
// this build, if it is younger than persistMaxAge. Anything else is a cold build.
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
		pb.Build == "" || pb.Build != persistBuild || time.Since(pb.ComputedAt) > persistMaxAge {
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
