package papertrade

import "testing"

// EnterLong and ExitLong set Fill.Reason only when the ADV participation cap
// bound the order -- the partial-fill notice and the multi-bar liquidation
// notice. All three PaperTrade construction sites then assigned their own
// Reason over the top, so both notices were written and discarded, and a capped
// fill read exactly like an ordinary one in the trade log.
func TestFillWithReasonKeepsTheCapNotice(t *testing.T) {
	const capNotice = "partial fill: capped at 5.0% of $1000000 average daily dollar volume"

	for _, tc := range []struct {
		name string
		fill Fill
		why  string
		want string
	}{
		{
			name: "uncapped fill keeps the caller's reason alone",
			fill: Fill{Reason: ""}, why: "manual buy order", want: "manual buy order",
		},
		{
			name: "capped fill with no caller reason keeps the notice",
			fill: Fill{Reason: capNotice}, why: "", want: capNotice,
		},
		{
			name: "capped fill keeps BOTH the sizing rationale and the notice",
			fill: Fill{Reason: capNotice}, why: "net_ev 0.1234 (rank 2/40)",
			want: "net_ev 0.1234 (rank 2/40) · " + capNotice,
		},
		{
			name: "nothing to say stays empty",
			fill: Fill{Reason: ""}, why: "", want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fill.WithReason(tc.why)
			if got == tc.want {
				return
			}
			if tc.fill.Reason != "" {
				t.Errorf("WithReason() = %q, want %q -- the cap notice is the only record that "+
					"this fill was truncated by the participation limit, and paper_trades stores "+
					"no other diagnostic, so dropping it makes a capped fill indistinguishable "+
					"from an ordinary one", got, tc.want)
				return
			}
			t.Errorf("WithReason() = %q, want %q -- the caller's own reason must survive unchanged "+
				"when the fill has nothing to add", got, tc.want)
		})
	}
}
