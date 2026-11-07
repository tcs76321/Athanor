package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/capacity"
	"github.com/tcs76321/athanor/internal/onboard"
)

func TestCapacityViewFromAudit(t *testing.T) {
	rep := capacity.PlanMachine(capacity.Inputs{
		HostBytes: 32 << 30, RuntimeBytes: 12 << 30, HostReserveBytes: 6 << 30,
		CoreReserveBytes: 2 << 30, PerPodBytes: 2 << 30, MaxJobs: 3, MaxLLMCalls: 3,
		Podman: true, JobPodImage: "jobpod",
		Models: []capacity.ModelFit{{
			Name: "small3b", Family: "beta", WeightsBytes: 2 << 30,
			KVPerToken: 96 * 1024, MaxContext: 32768, Score: 0.7,
		}},
	})
	audit := onboard.Audit{HostBytes: 32 << 30, Capacity: &rep}
	raw, err := json.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	u := &UI{deps: Deps{ModelAudit: string(raw)}}
	v := u.capacityView()
	if v == nil {
		t.Fatal("capacityView = nil")
	}
	if v.Solo == "none" || !strings.Contains(v.Pods, "small3b") {
		t.Errorf("view = %+v", v)
	}
	if !strings.Contains(v.Coverage, "capabilities:") {
		t.Errorf("coverage = %q", v.Coverage)
	}
}

func TestCapacityViewWithoutAuditIsNil(t *testing.T) {
	u := &UI{deps: Deps{}}
	if v := u.capacityView(); v != nil {
		t.Errorf("capacityView = %+v, want nil without an audit", v)
	}
}
