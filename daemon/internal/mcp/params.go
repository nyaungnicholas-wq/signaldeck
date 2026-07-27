// LAYER 2 (parameter half) — a closed input surface.
//
// No tool accepts code, SQL, an expression, a path, a filter, a date range, a
// projection or any other query structure. There are exactly three parameter
// shapes in this server:
//
//	topic       — a member of a fixed enum. Anything else is refused, and the
//	              refusal lists the enum, because a closed vocabulary is not a
//	              secret.
//	symbol      — an uppercase ticker matched against a strict pattern. It
//	              never becomes part of a database query: it filters an
//	              in-memory cache (cache.go), so even a validation mistake
//	              cannot reach the query planner.
//	description — the user's own prose about their own study. It is the one
//	              free-text parameter the tool surface needs, and it is treated
//	              as DATA at every step: never evaluated, never stored, never
//	              echoed back in the response. With no echo there is no
//	              reflection channel, which is what makes it safe to accept.
//
// Every parameter also passes looksExecutable, which refuses the obvious
// smuggling attempts outright rather than sanitising them. Sanitising an
// injection payload leaves you guessing whether you caught all of it.
package mcp

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// toolArgs is the union of every tool's validated arguments. A union rather
// than per-tool structs so the audit and anomaly layers can read a request's
// shape without knowing which tool produced it.
type toolArgs struct {
	Topic       string
	Symbol      string
	Description string
}

// ── topic enum ──────────────────────────────────────────────────────────────

// methodologyTopics is the complete vocabulary of explain_methodology.
var methodologyTopics = []string{
	"matched_nulls",
	"non_overlapping_sampling",
	"conviction_banding",
	"walk_forward",
	"survivorship",
	"pre_registration",
}

func parseTopic(raw json.RawMessage) (toolArgs, error) {
	var in struct {
		Topic string `json:"topic"`
	}
	if err := decodeArgs(raw, &in); err != nil {
		return toolArgs{}, err
	}
	t := strings.ToLower(strings.TrimSpace(in.Topic))
	t = strings.ReplaceAll(t, "-", "_")
	t = strings.ReplaceAll(t, " ", "_")
	for _, k := range methodologyTopics {
		if k == t {
			return toolArgs{Topic: k}, nil
		}
	}
	return toolArgs{}, errors.New("topic must be one of: " + strings.Join(methodologyTopics, ", "))
}

// ── symbol ──────────────────────────────────────────────────────────────────

// symbolPattern is deliberately narrower than what a market can contain. A
// ticker this server will not talk about is a smaller problem than a parameter
// that admits a character nobody thought about.
var symbolPattern = regexp.MustCompile(`^[A-Z0-9]{1,6}(?:[./-][A-Z0-9]{1,6})?$`)

func parseSymbol(raw json.RawMessage) (toolArgs, error) {
	var in struct {
		Symbol string `json:"symbol"`
	}
	if err := decodeArgs(raw, &in); err != nil {
		return toolArgs{}, err
	}
	s := strings.ToUpper(strings.TrimSpace(in.Symbol))
	if s == "" {
		return toolArgs{}, errors.New("symbol is required")
	}
	if len(s) > 13 || !symbolPattern.MatchString(s) {
		return toolArgs{}, errors.New("symbol must be a plain ticker such as AAPL or BTC/USD — " +
			"this parameter is not a filter, a pattern or an expression, and no wildcard, list, " +
			"range or query syntax is accepted in it")
	}
	return toolArgs{Symbol: s}, nil
}

// ── description ─────────────────────────────────────────────────────────────

const (
	minDescription = 20
	maxDescription = 4000
)

func parseDescription(raw json.RawMessage) (toolArgs, error) {
	var in struct {
		Description string `json:"description"`
	}
	if err := decodeArgs(raw, &in); err != nil {
		return toolArgs{}, err
	}
	d := strings.TrimSpace(in.Description)
	if len(d) < minDescription {
		return toolArgs{}, errors.New("description must be at least 20 characters of plain prose " +
			"describing your own proposed study")
	}
	if len(d) > maxDescription {
		return toolArgs{}, errors.New("description exceeds 4000 characters")
	}
	for _, r := range d {
		if r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		if unicode.IsControl(r) {
			return toolArgs{}, errors.New("description must be plain prose; control characters are not accepted")
		}
	}
	if why := looksExecutable(d); why != "" {
		return toolArgs{}, errors.New("refused: the description contains " + why +
			". This parameter is prose describing a study design — it is never executed, and " +
			"anything resembling code, a query or a path is rejected rather than sanitised. " +
			"This server has no query, evaluation or file-access capability under any tool.")
	}
	return toolArgs{Description: d}, nil
}

// executableSignals are the shapes that have no business in a sentence about
// research design. Each is deliberately specific enough that ordinary prose
// discussing statistics does not trip it — "I select a sample" is fine;
// "SELECT ... FROM" is not.
var executableSignals = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?is)\bselect\b.{0,200}\bfrom\b`), "an SQL SELECT"},
	{regexp.MustCompile(`(?i)\b(insert\s+into|update\s+\w+\s+set|delete\s+from|drop\s+table|alter\s+table|create\s+table|union\s+select|pragma\s+\w+)\b`), "an SQL statement"},
	{regexp.MustCompile(`(?i)(^|\s)(--|/\*)\s*[a-z]`), "an SQL comment"},
	{regexp.MustCompile(`(?i)\b(eval|exec|system|popen|subprocess|os\.system|__import__)\s*\(`), "a code-execution call"},
	{regexp.MustCompile("(?s)(\\$\\(|\\${|`|\\{\\{|<%|<\\?php)"), "a template or shell expansion"},
	{regexp.MustCompile(`(?i)(\.\./|/etc/|/var/|/proc/|~/|[a-z]:\\|file://|\bdata/[\w-]+\.db\b)`), "a filesystem path"},
	{regexp.MustCompile(`(?i)<\s*(script|iframe|img|svg)\b`), "markup"},
	{regexp.MustCompile(`(?i)\b(SIGNALDECK_[A-Z_]+|Authorization:\s*Bearer)\b`), "a credential or configuration reference"},
	{regexp.MustCompile(`(?i)\b(ignore (all )?(previous|prior|above) instructions|disregard (your|the) (rules|instructions)|you are now|system prompt)\b`), "an instruction-override attempt"},
	{regexp.MustCompile(`(?i)\b(get_bars|raw bars|\.db\b|sqlite|dump the|export the (dataset|table|database))\b`), "a raw-data request"},
}

// looksExecutable returns a human description of the first executable-looking
// construct found, or "" when the string is prose.
func looksExecutable(s string) string {
	for _, sig := range executableSignals {
		if sig.re.MatchString(s) {
			return sig.what
		}
	}
	return ""
}

// ── no-argument tools ───────────────────────────────────────────────────────

// parseNone accepts an empty object and nothing else. A tool that takes no
// arguments must REFUSE arguments: silently ignoring them is how a parameter
// nobody validates ends up being read by a later version.
func parseNone(raw json.RawMessage) (toolArgs, error) {
	if len(raw) == 0 {
		return toolArgs{}, nil
	}
	var m map[string]json.RawMessage
	if err := decodeArgs(raw, &m); err != nil {
		return toolArgs{}, err
	}
	if len(m) > 0 {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		return toolArgs{}, errors.New("this tool takes no arguments; received: " + strings.Join(keys, ", "))
	}
	return toolArgs{}, nil
}

// decodeArgs unmarshals with unknown fields REFUSED. An argument this build
// does not know is an attempt to reach a parameter that does not exist here,
// and answering it as though the extra field were harmless is how a smuggled
// parameter survives to the next version.
func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if len(raw) > maxArgBytes {
		return errors.New("arguments exceed the size cap")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid arguments: " + trimJSONErr(err.Error()) +
			". Every tool on this server takes typed, enumerated parameters only.")
	}
	return nil
}

// maxArgBytes caps one tool call's arguments, independent of the HTTP body cap.
const maxArgBytes = 16 << 10

func trimJSONErr(s string) string {
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
