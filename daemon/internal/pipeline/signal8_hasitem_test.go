package pipeline

import "testing"

func TestHasItem(t *testing.T) {
	for _, c := range []struct {
		items string
		want  bool
	}{{"2.02,9.01", true}, {"9.01, 2.02", true}, {"2.020", false}, {"12.02", false}, {"", false}} {
		if got := hasItem(c.items, "2.02"); got != c.want {
			t.Fatalf("hasItem(%q)=%v want %v", c.items, got, c.want)
		}
	}
}
