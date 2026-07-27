// gates_test.go makes the package's opening sentence — "the ONE cluster-robust
// statistics module every surface on this platform must call" — an enforced
// invariant instead of a convention.
//
// A1's root cause was a wiring gap, not a math bug: clusterstat existed and was
// correct, but decision gates built their intervals from raw Wilson math and
// nobody's build went red. The finishing move of that sweep (2026-07-26) was to
// make the invariant TREE-WIDE: exactly one Wilson implementation exists, in
// this package, and every other surface — the canary registry-parity reference,
// the attribution band, the research lab's corrected-alpha floor, researchx's
// weekly gate, the confidence uncertainty — either delegates to it or converts
// its sample to an effective N first. This test walks EVERY internal package
// with go/packages and fails, with the offending file:line, on any function
// that constructs a binomial interval by hand — so "a new gate next month
// hand-rolls its own interval" is a failing test the day it is written, not a
// future audit finding.
//
// The fingerprint is deliberately narrow: the binomial variance kernel x*(1-x)
// in a function that also calls math.Sqrt. Every hand-rolled proportion
// interval (Wilson, Wald, Agresti-Coull) needs both; a kernel WITHOUT a square
// root (e.g. the Brier reference variance baseRate*(1-baseRate) in api/predict,
// or DesignEffect's own binomial variance) is a variance, not an interval, and
// is intentionally not flagged.
package clusterstat_test

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const internalPrefix = "github.com/nyaungnicholas-wq/signaldeck/internal/"

// sanctionedWilson is the ONE function in the tree allowed to spell out the
// Wilson arithmetic. WilsonEff, canary.WilsonInterval, api.wilson,
// attribution.Wilson, researchx.wilsonLower and every gate all delegate here;
// a second spelling anywhere is a second chance to feed a raw row count to an
// interval, which is exactly how A1 shipped.
const sanctionedWilson = "clusterstat.WilsonEffAt"

func TestExactlyOneWilsonImplementationInTree(t *testing.T) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		// Tests:false scans only production files: test files may restate the
		// formula to cross-check the sanctioned implementation against it.
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, internalPrefix+"...")
	if err != nil {
		t.Fatalf("loading internal packages: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("internal packages did not load cleanly; cannot certify the invariant")
	}
	if len(pkgs) < 10 {
		t.Fatalf("loaded only %d internal packages — the tree has moved; fix the load pattern", len(pkgs))
	}

	var violations []string
	sanctionedSeen := false

	for _, pkg := range pkgs {
		short := strings.TrimPrefix(pkg.PkgPath, internalPrefix)
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				kernels, sqrt := scanFunc(pkg, fn)
				if len(kernels) == 0 || !sqrt {
					continue
				}
				key := short + "." + fn.Name.Name
				if key == sanctionedWilson {
					sanctionedSeen = true
					continue
				}
				pos := pkg.Fset.Position(kernels[0])
				violations = append(violations, fmt.Sprintf(
					"%s:%d: %s constructs a binomial interval by hand (p*(1-p) kernel under math.Sqrt); "+
						"the tree holds ONE Wilson implementation — delegate to clusterstat.WilsonEffAt, and pass an "+
						"EFFECTIVE sample size unless the caller can state why its design effect is 1",
					pos.Filename, pos.Line, key))
			}
		}
	}

	// The other half of "exactly one": the one implementation must still exist
	// and still be recognizable. If WilsonEffAt is renamed, split, or loses its
	// kernel, this fails rather than silently certifying an empty invariant.
	if !sanctionedSeen {
		violations = append(violations, fmt.Sprintf(
			"%s no longer contains the Wilson kernel — the sanctioned implementation has moved; update sanctionedWilson", sanctionedWilson))
	}

	// The gate A1 actually hit: canary's verdicts must come from clusterstat.
	for _, pkg := range pkgs {
		if strings.TrimPrefix(pkg.PkgPath, internalPrefix) != "canary" {
			continue
		}
		if _, ok := pkg.Imports[internalPrefix+"clusterstat"]; !ok {
			violations = append(violations, "internal/canary no longer imports clusterstat — the promotion gate has been unwired from the ONE statistics module (this is A1 again)")
		}
	}

	for _, v := range violations {
		t.Error(v)
	}
}

// scanFunc reports the positions of binomial variance kernels x*(1-x) in fn's
// body, and whether the function calls math.Sqrt anywhere.
func scanFunc(pkg *packages.Package, fn *ast.FuncDecl) (kernels []token.Pos, sqrt bool) {
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.BinaryExpr:
			if isBinomialKernel(e) {
				kernels = append(kernels, e.Pos())
			}
		case *ast.SelectorExpr:
			if f, ok := pkg.TypesInfo.Uses[e.Sel].(*types.Func); ok && f.FullName() == "math.Sqrt" {
				sqrt = true
			}
		}
		return true
	})
	return kernels, sqrt
}

// isBinomialKernel matches x*(1-x) / (1-x)*x with 1 as any literal spelling of
// one, comparing the two x's structurally so float64(k)/float64(n) forms match.
func isBinomialKernel(e *ast.BinaryExpr) bool {
	if e.Op != token.MUL {
		return false
	}
	return isOneMinus(e.Y, e.X) || isOneMinus(e.X, e.Y)
}

func isOneMinus(candidate, x ast.Expr) bool {
	sub, ok := unparen(candidate).(*ast.BinaryExpr)
	if !ok || sub.Op != token.SUB || !isLitOne(sub.X) {
		return false
	}
	return types.ExprString(unparen(x)) == types.ExprString(unparen(sub.Y))
}

func isLitOne(e ast.Expr) bool {
	lit, ok := unparen(e).(*ast.BasicLit)
	if !ok || (lit.Kind != token.INT && lit.Kind != token.FLOAT) {
		return false
	}
	v, err := strconv.ParseFloat(lit.Value, 64)
	return err == nil && v == 1
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}
