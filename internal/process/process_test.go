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
		{"quality regression", Input{FinalAccepted: true, FinalGrounded: true, Candidates: 3, Cycles: 2, FinalScore: 0.5, BestCandidateScore: 0.9}, false, true, true, false, true},
		{"judge-only final is not a regression", Input{FinalAccepted: true, FinalGrounded: false, Candidates: 3, FinalScore: 0, BestCandidateScore: 0.9}, false, false, false, true, false},
		{"completed with previous", Input{Completed: true, FinalAccepted: false, Candidates: 3, Cycles: 1}, false, false, false, false, false},
		{"cancelled", Input{Cancelled: true, Candidates: 1, Cycles: 1}, false, false, false, false, false},
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

func TestEvaluateVerdicts(t *testing.T) {
	cases := []struct {
		in   Input
		want string
	}{
		{Input{FinalAccepted: true, FinalGrounded: true, Candidates: 1, Cycles: 1}, "one-shot sufficient"},
		{Input{FinalAccepted: true, FinalGrounded: true, Candidates: 3, Cycles: 2, Reflections: 1}, "escalation rescued the job"},
		{Input{FinalAccepted: true, FinalGrounded: true, Candidates: 3, Cycles: 1}, "panel sufficient, no escalation needed"},
		{Input{Completed: true, Candidates: 1, Cycles: 1}, "completed without accepting new work"},
		{Input{Cancelled: true, Candidates: 1, Cycles: 1}, "cancelled"},
		{Input{Candidates: 1, Cycles: 1}, "failed without escalation"},
		{Input{Candidates: 1, Cycles: 2, Reflections: 1}, "failed after escalation"},
	}
	for _, tc := range cases {
		if got := Evaluate(tc.in).Verdict; got != tc.want {
			t.Errorf("verdict = %q, want %q", got, tc.want)
		}
	}
}
