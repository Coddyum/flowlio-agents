package effort_test

import (
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/effort"
)

// Valid admits exactly the four tiers, and nothing else — the empty string included, which the
// callers fold to the default themselves.
func TestValid(t *testing.T) {
	for _, tier := range []string{"low", "standard", "high", "max"} {
		if !effort.Valid(tier) {
			t.Errorf("Valid(%q) = false, want true", tier)
		}
	}
	for _, bad := range []string{"", "LOW", "urgent", "opus", "medium"} {
		if effort.Valid(bad) {
			t.Errorf("Valid(%q) = true, want false", bad)
		}
	}
}

// Rank orders the tiers low < standard < high < max, and folds anything unknown to standard so a
// rank is always answerable. FromRank is its inverse and clamps out-of-range ranks into the tiers.
func TestRankAndFromRankRoundTrip(t *testing.T) {
	order := []string{"low", "standard", "high", "max"}
	for want, tier := range order {
		if got := effort.Rank(tier); got != want {
			t.Errorf("Rank(%q) = %d, want %d", tier, got, want)
		}
		if got := effort.FromRank(want); got != tier {
			t.Errorf("FromRank(%d) = %q, want %q", want, got, tier)
		}
	}
	if got := effort.Rank("nonsense"); got != effort.Rank(effort.Standard) {
		t.Errorf("Rank(unknown) = %d, want standard's rank", got)
	}
	if got := effort.FromRank(-1); got != effort.Low {
		t.Errorf("FromRank(-1) = %q, want low", got)
	}
	if got := effort.FromRank(99); got != effort.Max {
		t.Errorf("FromRank(99) = %q, want max", got)
	}
}

// Clamp is the whole of "receiver disposes": the running tier is min(what was asked, the ceiling),
// with an unset want folding to the default and an unset ceiling meaning "no cap".
func TestClamp(t *testing.T) {
	cases := []struct {
		name          string
		want, ceiling string
		expect        string
	}{
		{"max asked, high ceiling → capped to high", "max", "high", "high"},
		{"low asked, high ceiling → the ask stands", "low", "high", "low"},
		{"high asked, no ceiling → the ask stands", "high", "", "high"},
		{"empty ask → the default", "", "max", "standard"},
		{"empty ask, no ceiling → the default", "", "", "standard"},
		{"invalid ceiling is treated as unset", "max", "nonsense", "max"},
		{"equal ask and ceiling → that tier", "standard", "standard", "standard"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effort.Clamp(tc.want, tc.ceiling); got != tc.expect {
				t.Errorf("Clamp(%q, %q) = %q, want %q", tc.want, tc.ceiling, got, tc.expect)
			}
		})
	}
}

// A hostile author cannot lift a wake above the receiver's ceiling: whatever tier it declares, the
// clamp holds it at the cap. This is the cost-amplification guard stated as a property.
func TestClampNeverExceedsCeiling(t *testing.T) {
	ceiling := effort.Standard
	for _, asked := range []string{"low", "standard", "high", "max", "", "nonsense"} {
		got := effort.Clamp(asked, ceiling)
		if effort.Rank(got) > effort.Rank(ceiling) {
			t.Errorf("Clamp(%q, %q) = %q, above the ceiling", asked, ceiling, got)
		}
	}
}
