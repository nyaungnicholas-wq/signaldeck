package wikimedia

import (
	"slices"
	"testing"
)

// SEC's capitalised names must also yield the title-cased article.
func TestArticleCandidatesTitleCasesCapitalisedNames(t *testing.T) {
	got := ArticleCandidates("APPLE INC")
	for _, want := range []string{"APPLE", "Apple", "Apple_(company)", "Apple_Inc"} {
		if !slices.Contains(got, want) {
			t.Fatalf("candidates %v lack %q", got, want)
		}
	}
	if got := ArticleCandidates("NVIDIA Corp"); len(got) != 2 {
		t.Fatalf("mixed-case name should keep the two original candidates, got %v", got)
	}
}
