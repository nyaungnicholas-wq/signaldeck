package api

// Publisher guardrails (plan step 6; docs/PUBLISHER_GUARDRAILS.md). The member
// product publishes the SAME forecasts to everyone and takes nothing about a
// member's money: that is what keeps it impersonal publishing rather than
// personalised advice. These two tests hold the line mechanically.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
)

// perUserRoutes are the member-reachable GETs whose body legitimately depends
// on who asks, each with why. Everything else must be byte-identical for every
// member.
// clockFields blanks a field stamped per request with the wall clock, which
// differs between any two reads: never a per-member value.
var clockFields = map[string]*regexp.Regexp{
	"/api/accuracy": regexp.MustCompile(`"generated_at":"[^"]*"`), // stamped per request (accuracy.go)
}

var perUserRoutes = map[string]string{
	"/api/watchlist":   "the member's own bookmark list; carries no forecast a non-watcher cannot read on /api/regimes",
	"/api/auth/me":     "the session's own identity",
	"/api/alert-prefs": "the member's own on/off delivery switches",
	"/api/journal":     "the member's own calls and their grade; never a SignalDeck forecast, and no price or return",
}

// TestMemberForecastsAreImpersonal: two members with different watchlists and
// different alert settings read every member-reachable GET and get the same
// bytes, except perUserRoutes. A body that changes between two reads by the
// SAME member (a clock in it) is retried; one that is stable per member but
// differs across members is personalised and fails.
func TestMemberForecastsAreImpersonal(t *testing.T) {
	ctx := context.Background()
	// FINRA routes open, so the comparison covers the widest member surface.
	srv, st, mb, _ := newProductionServer(t, func(c *config.Config) { c.MemberFINRA = true },
		writeRegistry(t, thinWindowRegistry))
	freshHeartbeat(t, st)
	sharedDashCache.mu.Lock()
	sharedDashCache.global = nil
	sharedDashCache.mu.Unlock()
	fx := seedSentinels(t, st, 0)

	mira := signupVerified(t, srv, mb, "mira", "mira@gmail.com")
	nils := signupVerified(t, srv, mb, "nils", "nils@gmail.com")
	uid := func(name string) int64 {
		u, _, err := st.GetUserByName(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.AddMemberSymbol(ctx, uid("mira"), fx.sntl.ID))
	must(st.AddMemberSymbol(ctx, uid("nils"), fx.sntw.ID))
	must(st.SetEmailDigest(ctx, uid("mira"), true))
	must(st.SetTelegramLinkCode(ctx, uid("nils"), "ABCD2345", time.Now().Add(time.Hour)))

	probes := memberProbes(fx)
	compared, perUser, labelled := 0, 0, 0
	for _, path := range memberAdmittedRoutes(t) {
		pr, ok := probes[path]
		if _, exempt := memberProbeExempt[path]; exempt || !ok || pr.method != "GET" {
			continue // POST probes write; the sentinel test already demands a probe for every route
		}
		if _, ok := perUserRoutes[path]; ok {
			perUser++
			continue
		}
		var a1, b, a2 string
		stable := false
		for try := 0; try < 4 && !stable; try++ {
			var c1, c2, c3 int
			c1, a1 = getAs(t, mira, srv.URL+pr.url)
			c2, b = getAs(t, nils, srv.URL+pr.url)
			c3, a2 = getAs(t, mira, srv.URL+pr.url)
			if re, ok := clockFields[path]; ok {
				a1, b, a2 = re.ReplaceAllString(a1, ""), re.ReplaceAllString(b, ""), re.ReplaceAllString(a2, "")
			}
			if c1 != c2 || c1 != c3 {
				t.Fatalf("%s: status %d/%d/%d across members", pr.url, c1, c2, c3)
			}
			stable = a1 == a2 && b == a1
			if a1 == a2 && b != a1 {
				break
			}
		}
		if !stable {
			if a1 == a2 {
				t.Errorf("%s answers two members differently: it is personalised (list it in perUserRoutes "+
					"with a reason only if it serves the member's own settings, never a forecast); see "+
					"docs/PUBLISHER_GUARDRAILS.md\nmira: %.300s\nnils: %.300s", pr.url, a1, b)
			} else {
				t.Errorf("%s never answered the same member twice in a row; cannot judge it\nA1 %.400s\nA2 %.400s", pr.url, a1, a2)
			}
			continue
		}
		compared++
		// Hypothetical-performance label: every forecast a member reads names
		// its accuracy as a backtest figure, in the payload itself.
		if path == "/api/regimes" || path == "/api/vol-regime" {
			if n, m := strings.Count(a1, `"historicalAccuracy"`), strings.Count(a1, `"evidence":"backtest"`); n == 0 || m < n {
				t.Errorf("%s: %d accuracy figures but %d backtest labels: %s", path, n, m, a1)
			}
			labelled++
		}
	}
	for p := range perUserRoutes {
		if !memberAllowed(p) {
			t.Errorf("perUserRoutes names %s, which a member cannot reach: stale", p)
		}
	}
	if labelled != 2 {
		t.Errorf("checked the backtest label on %d of the 2 forecast routes", labelled)
	}
	if compared < 30 || perUser != len(perUserRoutes) {
		t.Errorf("compared %d routes (floor 30), %d per-user of %d listed: the comparison has rotted",
			compared, perUser, len(perUserRoutes))
	}
	t.Logf("compared %d member GET routes byte-for-byte; %d per-user routes excluded", compared, perUser)
}

// memberInputs pins every query, path and JSON-body parameter name a handler of
// a member-reachable route reads, with what it is. A new name fails until it
// is listed here; a name describing the member's money fails outright.
var memberInputs = map[string]string{
	// What to look up: the same public answer for anyone who asks.
	"symbol": "ticker to look up", "market": "stocks/futures (crypto refused for members)",
	"q": "company name search", "ticker": "TradingView webhook ticker (shared-secret inbound, serves nothing)",
	"id": "claim, research-ledger or lineage id", "kind": "lineage node kind / model-health forecast kind",
	"horizon": "forecast horizon (1d, 21d...)", "feature": "evidence list filter", "status": "evidence list filter",
	"form": "SEC form type filter", "code": "Form 4 transaction-code filter", "manager": "13F manager filter",
	"chamber": "STOCK Act chamber filter", "member": "a member of Congress, by name (STOCK Act filter)",
	"exchange": "directory filter", "sector": "directory filter", "tracked": "directory filter",
	"mcapMin": "directory filter; ignored for members (companies handler)", "mcapMax": "directory filter; ignored for members",
	"history": "fundamentals: include history", "summary": "company profile: request the LLM summary (never run for members)",
	// Paging and proof options.
	"limit": "page size", "offset": "page offset", "from": "ledger range start seq", "days": "lookback window",
	"depth": "lineage depth", "full": "ledger verify/anchors: full recompute (self-gated to signed-in callers)",
	// Account flows: identity and anti-abuse, never money.
	"username": "account name", "password": "account password", "email": "account or waitlist address",
	"token": "emailed one-time link token", "turnstileToken": "Cloudflare human check",
	"website": "sign-up honeypot", "hp": "waitlist honeypot", "source": "waitlist referral tag",
	"credential":  "Google ID token",
	"emailDigest": "daily-read email on/off (alert-prefs)",
	// The member's OWN calls in the journal (plan step 8): what the member
	// predicts, graded against the tape. These never reach, change or
	// personalise SignalDeck's forecasts. symbol, market, horizon and id (listed
	// above) are read by the journal too: the called ticker, "stocks", the
	// call's horizon in sessions (1, 5, 21), and the member's own call id.
	"call": "journal: the member's own call, up or down",
	"note": "journal: the member's own note on their call (280 chars, shown only to them)",
	// TradingView inbound alert body (shared-secret webhook; operator's own alerts, nothing served back).
	"action": "TradingView alert action", "message": "TradingView alert text", "price": "TradingView alert price field",
	"secret": "TradingView shared secret",
	// Ask the data (plan step 10): the question goes to the model as the user
	// turn; rows come only from the copilot catalog, never from this text.
	"question": "ask the data: the member's question (500 chars)",
}

var personalInput = regexp.MustCompile(`(?i)capital|size|portfolio|position|risk|account|balance|equity|amount|qty|quantity|leverage`)

// TestMemberRoutesTakeNoPersonalInputs scans the api package's source: for
// every route a member session can reach, the handler and everything it
// references in this package, collecting the parameter names it reads.
func TestMemberRoutesTakeNoPersonalInputs(t *testing.T) {
	scan := scanMemberInputs(t)
	if scan.routes < 40 || scan.funcs < 60 || len(scan.params) < 15 {
		t.Fatalf("scanned %d routes, %d functions, %d parameter names: the scanner is not seeing the handlers",
			scan.routes, scan.funcs, len(scan.params))
	}
	for _, must := range []string{"symbol", "market", "emailDigest", "token", "id"} {
		if _, ok := scan.params[must]; !ok {
			t.Errorf("the scanner missed %q, which a member route is known to read: it is blind", must)
		}
	}
	names := make([]string, 0, len(scan.params))
	for n := range scan.params {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		where := strings.Join(scan.params[n], ", ")
		if personalInput.MatchString(n) {
			t.Errorf("member route input %q (read in %s) names a personal financial input; members are served "+
				"impersonal forecasts only. See docs/PUBLISHER_GUARDRAILS.md before changing this.", n, where)
			continue
		}
		if _, ok := memberInputs[n]; !ok {
			t.Errorf("member route input %q (read in %s) is not in memberInputs: list it with what it is, "+
				"after checking it against docs/PUBLISHER_GUARDRAILS.md", n, where)
		}
	}
	for n := range memberInputs {
		if _, ok := scan.params[n]; !ok {
			t.Errorf("memberInputs lists %q, which no member route reads any more: stale", n)
		}
	}
}

type inputScan struct {
	routes, funcs int
	params        map[string][]string // name -> functions reading it
}

func scanMemberInputs(t *testing.T) inputScan {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	funcs := map[string][]*ast.FuncDecl{} // name (method or func) -> decls
	isMethod, isFunc := map[string]bool{}, map[string]bool{}
	types := map[string]*ast.StructType{}
	var files []*ast.File
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				funcs[d.Name.Name] = append(funcs[d.Name.Name], d)
				if d.Recv != nil {
					isMethod[d.Name.Name] = true
				} else {
					isFunc[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					if ts, ok := s.(*ast.TypeSpec); ok {
						if st, ok := ts.Type.(*ast.StructType); ok {
							types[ts.Name.Name] = st
						}
					}
				}
			}
		}
	}
	lit := func(e ast.Expr) (string, bool) {
		if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
			s, err := strconv.Unquote(b.Value)
			return s, err == nil
		}
		return "", false
	}
	// readerArg: functions that read a request parameter whose NAME is one of
	// their own arguments (queryBool(r, "x")), with that argument's index.
	readerArg := map[string]int{}
	isRead := func(sel *ast.SelectorExpr) bool {
		switch sel.Sel.Name {
		case "FormValue", "PostFormValue", "PathValue":
			return true
		case "Get", "Has":
			// url.Values reads; a header read is not a parameter.
			if x, ok := sel.X.(*ast.SelectorExpr); ok && x.Sel.Name == "Header" {
				return false
			}
			return true
		}
		return false
	}
	for name, decls := range funcs {
		for _, fd := range decls {
			if fd.Body == nil || fd.Type.Params == nil {
				continue
			}
			var params []string
			for _, f := range fd.Type.Params.List {
				for _, n := range f.Names {
					params = append(params, n.Name)
				}
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				id, isID := call.Args[0].(*ast.Ident)
				if ok && isID && isRead(sel) {
					for i, p := range params {
						if p == id.Name {
							readerArg[name] = i
						}
					}
				}
				return true
			})
		}
	}

	out := inputScan{params: map[string][]string{}}
	add := func(name, where string) {
		for _, w := range out.params[name] {
			if w == where {
				return
			}
		}
		out.params[name] = append(out.params[name], where)
	}
	visited := map[string]bool{}
	var visit func(name string)
	scanBody := func(where string, body ast.Node) {
		// Declared types of this body's variables, so a decode target resolves,
		// and the variables holding the request body (or a reader over it).
		declared := map[string]ast.Expr{}
		fromBody := map[string]bool{}
		mentionsBody := func(e ast.Node) bool {
			found := false
			ast.Inspect(e, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if x, ok := n.X.(*ast.Ident); ok && x.Name == "r" && n.Sel.Name == "Body" {
						found = true
					}
				case *ast.Ident:
					if fromBody[n.Name] {
						found = true
					}
				}
				return !found
			})
			return found
		}
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				if n.Type != nil {
					for _, id := range n.Names {
						declared[id.Name] = n.Type
					}
				}
			case *ast.AssignStmt: // body, err := io.ReadAll(r.Body); dec := json.NewDecoder(r.Body)
				for _, rhs := range n.Rhs {
					if mentionsBody(rhs) {
						for _, l := range n.Lhs {
							if id, ok := l.(*ast.Ident); ok && id.Name != "_" && id.Name != "err" {
								fromBody[id.Name] = true
							}
						}
					}
				}
			}
			return true
		})
		var inspect func(n ast.Node) bool
		inspect = func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				var callee string
				switch f := n.Fun.(type) {
				case *ast.SelectorExpr:
					callee = f.Sel.Name
					if isRead(f) && len(n.Args) == 1 {
						if s, ok := lit(n.Args[0]); ok {
							add(s, where)
						}
					}
					// json.NewDecoder(r.Body).Decode(&body), json.Unmarshal(raw, &body)
					// over the REQUEST body: the target's json tags are inputs. A decode
					// of a file, a cache or an upstream reply is not.
					src := ast.Node(f.X)
					if f.Sel.Name == "Unmarshal" && len(n.Args) == 2 {
						src = n.Args[0]
					}
					if (f.Sel.Name == "Decode" || f.Sel.Name == "Unmarshal") && len(n.Args) > 0 && mentionsBody(src) {
						if u, ok := n.Args[len(n.Args)-1].(*ast.UnaryExpr); ok && u.Op == token.AND {
							if id, ok := u.X.(*ast.Ident); ok {
								switch ty := declared[id.Name].(type) {
								case *ast.StructType:
									tags(ty, where, add)
								case *ast.Ident:
									if st, ok := types[ty.Name]; ok {
										tags(st, where, add)
									}
								}
							}
						}
					}
				case *ast.Ident:
					callee = f.Name
				}
				if i, ok := readerArg[callee]; ok && i < len(n.Args) {
					if s, ok := lit(n.Args[i]); ok {
						add(s, where)
					}
				}
			case *ast.SelectorExpr: // d.method, called or passed as a value: follow it
				if isMethod[n.Sel.Name] {
					visit(n.Sel.Name)
				}
				ast.Inspect(n.X, inspect) // never read Sel as a bare package func name
				return false
			case *ast.Ident: // a package func, called or passed; never a local variable
				if isFunc[n.Name] && (n.Obj == nil || n.Obj.Kind == ast.Fun) {
					visit(n.Name)
				}
			}
			return true
		}
		ast.Inspect(body, inspect)
	}
	visit = func(name string) {
		decls, ok := funcs[name]
		if !ok || visited[name] {
			return
		}
		visited[name] = true
		out.funcs++
		for _, fd := range decls {
			if fd.Body != nil {
				scanBody(name, fd.Body)
			}
		}
	}

	route := regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH) (/api/\S+)$`)
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
				return true
			}
			s, ok := lit(call.Args[0])
			m := route.FindStringSubmatch(s)
			if !ok || m == nil || !memberAllowed(m[2]) {
				return true
			}
			out.routes++
			scanBody(m[2], call.Args[1])
			return true
		})
	}
	return out
}

func tags(st *ast.StructType, where string, add func(name, where string)) {
	for _, f := range st.Fields.List {
		if f.Tag == nil {
			continue
		}
		tag, err := strconv.Unquote(f.Tag.Value)
		if err != nil {
			continue
		}
		name := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
		if name != "" && name != "-" {
			add(name, where)
		}
	}
}
