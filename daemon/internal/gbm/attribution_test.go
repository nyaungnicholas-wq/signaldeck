package gbm

import (
	"math"
	"testing"
)

// hand-built 2-tree fixture with known attributions.
//
// Tree 1 (splits feature 0 at 0.5): leaves L=-1, R=+1.
//   root value = 0 (unweighted leaf mean).
// Tree 2 (root splits feature 1 at 0.0; right child splits feature 0 at 0.5):
//   leaves: left=0, right-left=2, right-right=4.
//   right node value = 3, root value = (0+2+4)/3 = 2.
//
// For x = {0.7, 1.0} with LR=0.5, Bias=0.25:
//   tree1: path root→R. contrib f0 += 0.5*(1-0) = 0.5. base += 0.5*0.
//   tree2: root→right: contrib f1 += 0.5*(3-2) = 0.5;
//          right→right-right: contrib f0 += 0.5*(4-3) = 0.5. base += 0.5*2 = 1.
//   Base = 0.25 + 0 + 1 = 1.25; contribs = {f0: 1.0, f1: 0.5}.
//   Raw score = 0.25 + 0.5*1 + 0.5*4 = 2.75 = 1.25 + 1.0 + 0.5. Identity holds.
func fixtureModel() *Model {
	t1 := &treeNode{
		Feature: 0, Threshold: 0.5,
		Left:  &treeNode{Leaf: true, Value: -1},
		Right: &treeNode{Leaf: true, Value: 1},
	}
	t2 := &treeNode{
		Feature: 1, Threshold: 0.0,
		Left: &treeNode{Leaf: true, Value: 0},
		Right: &treeNode{
			Feature: 0, Threshold: 0.5,
			Left:  &treeNode{Leaf: true, Value: 2},
			Right: &treeNode{Leaf: true, Value: 4},
		},
	}
	return &Model{Bias: 0.25, LR: 0.5, NFeat: 2, Trees: []*treeNode{t1, t2}}
}

func TestAttribute_HandBuiltFixture(t *testing.T) {
	m := fixtureModel()
	x := []float64{0.7, 1.0}
	a, ok := m.Attribute(x)
	if !ok {
		t.Fatal("Attribute refused a valid vector")
	}
	if math.Abs(a.Base-1.25) > 1e-12 {
		t.Errorf("base = %v, want 1.25", a.Base)
	}
	want := []float64{1.0, 0.5}
	for i, w := range want {
		if math.Abs(a.Contribs[i]-w) > 1e-12 {
			t.Errorf("contrib[%d] = %v, want %v", i, a.Contribs[i], w)
		}
	}
	// Other path: x = {0.2, -1} → tree1 left (f0: 0.5*(-1-0)), tree2 left
	// (f1: 0.5*(0-2) = -1).
	a2, _ := m.Attribute([]float64{0.2, -1})
	if math.Abs(a2.Contribs[0]-(-0.5)) > 1e-12 || math.Abs(a2.Contribs[1]-(-1.0)) > 1e-12 {
		t.Errorf("left-path contribs = %v, want [-0.5 -1]", a2.Contribs)
	}
}

// The sum identity: Base + Σ Contribs == RawScore == logit(Predict), exactly
// within float tolerance, on a real trained model over many inputs.
func TestAttribute_SumIdentityOnTrainedModel(t *testing.T) {
	// Deterministic synthetic data: y depends non-linearly on 3 features.
	var samples []Sample
	for i := 0; i < 300; i++ {
		f := []float64{
			math.Sin(float64(i) * 0.7),
			math.Cos(float64(i) * 1.3),
			float64(i%7)/7.0 - 0.5,
		}
		y := 0.0
		if f[0]*f[1] > 0 || f[2] > 0.2 {
			y = 1.0
		}
		samples = append(samples, Sample{Ts: int64(i), LabelEnd: int64(i + 1), Feat: f, Y: y})
	}
	m, err := Train(samples, Defaults())
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	for i := 0; i < 300; i += 7 {
		x := samples[i].Feat
		a, ok := m.Attribute(x)
		if !ok {
			t.Fatal("Attribute refused a valid vector")
		}
		sum := a.Base
		for _, c := range a.Contribs {
			sum += c
		}
		score := m.RawScore(x)
		if math.Abs(sum-score) > 1e-9 {
			t.Fatalf("identity broken at row %d: base+Σcontribs=%v, raw score=%v", i, sum, score)
		}
		if p := sigmoid(score); math.Abs(p-m.Predict(x)) > 1e-12 {
			t.Fatalf("RawScore disagrees with Predict at row %d", i)
		}
	}
}

func TestAttribute_RefusesWrongDimension(t *testing.T) {
	m := fixtureModel()
	if _, ok := m.Attribute([]float64{1}); ok {
		t.Error("Attribute accepted a wrong-dimension vector")
	}
	if got := m.RawScore([]float64{1, 2, 3}); got != m.Bias {
		t.Errorf("RawScore on wrong dim = %v, want bias %v", got, m.Bias)
	}
}
