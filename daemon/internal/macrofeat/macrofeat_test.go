package macrofeat

import (
	"math"
	"testing"
)

func TestFromVIX_Regimes(t *testing.T) {
	cases := []struct {
		vix        float64
		wantReg    int
		wantHigh   float64
		regimeName string
	}{
		{10, 0, 0, "calm"},
		{14.9, 0, 0, "calm"},
		{15, 1, 0, "normal"},
		{24.9, 1, 0, "normal"},
		{25, 2, 1, "elevated"},
		{34.9, 2, 1, "elevated"},
		{35, 3, 1, "stressed"},
		{80, 3, 1, "stressed"},
	}
	for _, c := range cases {
		f := FromVIX(c.vix)
		if got := int(math.Round(f.Regime * 3)); got != c.wantReg {
			t.Errorf("VIX %.1f (%s): regime ordinal = %d, want %d", c.vix, c.regimeName, got, c.wantReg)
		}
		if f.HighVol != c.wantHigh {
			t.Errorf("VIX %.1f (%s): highVol = %.0f, want %.0f", c.vix, c.regimeName, f.HighVol, c.wantHigh)
		}
	}
}

func TestFromVIX_LevelScaled(t *testing.T) {
	f := FromVIX(20)
	if math.Abs(f.Level-0.20) > 1e-9 {
		t.Fatalf("level for VIX 20 = %.4f, want 0.20 (scaled /100)", f.Level)
	}
}

func TestMap_HasAllKeys(t *testing.T) {
	m := FromVIX(30).Map()
	for _, k := range []string{"vix_level", "vix_regime", "vix_high_vol"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("Map missing key %q", k)
		}
	}
	// VIX 30 => elevated => high vol.
	if m["vix_high_vol"] != 1 {
		t.Fatalf("VIX 30 should be high vol, got %.0f", m["vix_high_vol"])
	}
	// regime 2 of 3 => 0.6667.
	if math.Abs(m["vix_regime"]-2.0/3.0) > 1e-9 {
		t.Fatalf("VIX 30 regime scaled = %.4f, want 0.6667", m["vix_regime"])
	}
}
