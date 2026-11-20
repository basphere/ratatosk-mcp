package advisory

// Which values in a change's advisories are advisory ids, and when the ids of
// one change name one advisory. The counting itself is tested through the
// briefing (internal/briefing), which is where the count is answered.

import (
	"slices"
	"testing"
)

func TestRecognisedAdvisoryIDs(t *testing.T) {
	got := RecognisedIDs([]string{" ghsa-3v3m-wc6v-x4x3", "CVE-2026-42880", "GO-2026-1234", "RUSTSEC-2026-0001", "PYSEC-2026-7",
		"GHSA-aaaa-bbbb-cccc", "CVE-26-1", "N/A", "", "#30040", "CVE-2026-42880"})
	want := []string{"CVE-2026-42880", "GHSA-3V3M-WC6V-X4X3", "GO-2026-1234", "PYSEC-2026-7", "RUSTSEC-2026-0001"}
	if !slices.Equal(got, want) {
		t.Errorf("recognised %v, want %v", got, want)
	}
	if namesOneAdvisory([]string{"CVE-2026-1", "GHSA-2222-3333-4444"}) != true || namesOneAdvisory([]string{"CVE-2026-1111", "CVE-2026-2222"}) {
		t.Error("one id per family names one advisory; two of a family name two")
	}
}
