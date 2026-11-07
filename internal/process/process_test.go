package process

import "testing"

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		one  bool
		esc  bool
		res  bool
		wast bool
		reg  bool
	}{
		{"one-shot success", Input{FinalAccepted: true, Candidates: 1, Cycles: 1}, true, false, false, false, false},
		{"full panel no escalation", Input{FinalAccepted: true, Candidates: 3, Cycles: 1}, false, false, false, true, false},
		{"escalation rescued", Input{FinalAccepted: true, Candidates: 3, Cycles: 2, Reflections: 1}, false, true, true, false, false},
		{"failed without escalation", Input{FinalAccepted: false, Candidates: 1, Cycles: 1}, false, false, false, false, false},
		{"failed after escalation", Input{FinalAccepted: false, Candidates: 3, Cycles: 2, Replans: 1}, false, true, false, false, false},
		{"quality regression", Input{FinalAccepted: true, Candidates: 3, Cycles: 2, FinalScore: 0.5, BestCandidateScore: 0.9}, false, true, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.in)
			if got.OneShotSufficient != tc.one || got.Escalated != tc.esc ||
				got.Rescued != tc.res || got.WastedCompute != tc.wast || got.Regression != tc.reg {
				t.Fatalf("got %+v, want one=%v esc=%v res=%v waste=%v reg=%v",
					got, tc.one, tc.esc, tc.res, tc.wast, tc.reg)
			}
			if got.Verdict == "" {
				t.Fatal("empty verdict")
			}
		})
	}
}
