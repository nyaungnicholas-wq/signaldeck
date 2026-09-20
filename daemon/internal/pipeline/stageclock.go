package pipeline

import (
	"fmt"
	"strings"
	"time"
)

// stageClock accumulates elapsed time per named stage within a single worker run.
// worker_runs.detail is the only per-run evidence the platform keeps, and until
// now a 30-minute run said only that it was 30 minutes. This records how much
// time each stage consumed so the row names the stage. It is deliberately
// additive and cheap enough to leave on permanently, because the runs worth
// diagnosing are rare and overnight, and an instrument you have to switch on
// is never on when the thing happens.
type stageClock struct {
	d     map[string]time.Duration
	n     map[string]int
	order []string
	seen  map[string]bool
}

// newStageClock returns an initialised stageClock ready for use.
func newStageClock() *stageClock {
	return &stageClock{
		d:    make(map[string]time.Duration),
		n:    make(map[string]int),
		seen: make(map[string]bool),
	}
}

// at starts timing a stage named name. It returns a stop function that, when
// called, adds the elapsed time to that stage and increments its count. Subsequent
// calls to the returned stop function are harmless. A nil receiver yields a
// no-op stop function.
func (c *stageClock) at(name string) func() {
	if c == nil {
		return func() {}
	}
	start := time.Now()
	called := false
	return func() {
		if called {
			return
		}
		called = true
		if !c.seen[name] {
			c.order = append(c.order, name)
			c.seen[name] = true
		}
		elapsed := time.Since(start)
		c.d[name] += elapsed
		c.n[name]++
	}
}

// add records a pre-measured duration d under the stage name. It follows the
// same ordering and counting rules as at(). A nil receiver is safe and does
// nothing.
func (c *stageClock) add(name string, d time.Duration) {
	if c == nil {
		return
	}
	if !c.seen[name] {
		c.order = append(c.order, name)
		c.seen[name] = true
	}
	c.d[name] += d
	c.n[name]++
}

// String returns a space-separated list of stages that have accumulated at
// least 100 ms, each shown as "name roundedDuration/count". Durations are
// rounded to the nearest 100 ms using time.Duration's String(). If no stage
// meets the threshold, or the receiver is nil, the result is the empty string.
func (c *stageClock) String() string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	for _, name := range c.order {
		dur := c.d[name]
		if dur < 100*time.Millisecond {
			continue
		}
		rounded := dur.Round(100 * time.Millisecond)
		fmt.Fprintf(&b, "%s %s/%d ", name, rounded.String(), c.n[name])
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return ""
	}
	return s
}
