package onboard

import (
	"testing"

	"github.com/tcs76321/athanor/internal/capacity"
)

const gib = int64(1 << 30)

func TestCapacityReportFromAudit(t *testing.T) {
	a := Audit{
		HostBytes: 32 * gib, ReserveBytes: 6 * gib,
		Roles: []AuditRole{
			{Role: "main", Model: "small3b", ContextTarget: 32768, FileBytes: 2 * gib, LoadedBytes: 2*gib + 96*1024*32768, Fits: true},
			{Role: "security", Model: "judge3b", ContextTarget: 8192, FileBytes: 2 * gib, LoadedBytes: 2*gib + 96*1024*8192, Fits: true},
			{Role: "tall", Model: "big9b", ContextTarget: 32768, FileBytes: 6 * gib, LoadedBytes: 6*gib + 128*1024*32768, Fits: true},
		},
	}
	rep := CapacityReport(a, CapacityOptions{
		RuntimeBytes: 12 * gib, CoreReserveBytes: 2 * gib, PerPodBytes: 2 * gib,
		MaxJobs: 3, MaxLLMCalls: 3, Podman: true, JobPodImage: "athanor-jobpod:latest",
	})
	if rep.Solo == nil {
		t.Fatal("solo = nil")
	}
	if rep.Solo.Model != "big9b" {
		t.Errorf("solo = %q, want big9b", rep.Solo.Model)
	}
	if rep.SwarmPods == nil || rep.SwarmPods.Slots < 2 {
		t.Fatalf("SwarmPods = %+v, want a multi-slot plan", rep.SwarmPods)
	}
	if !rep.Coverage.CodeExecution {
		t.Error("CodeExecution = false, want true")
	}
	if rep.Coverage.SingleModel {
		t.Error("SingleModel = true, want false")
	}
}

func TestCapacityReportDegradesWithOneModel(t *testing.T) {
	a := Audit{
		HostBytes: 8 * gib, ReserveBytes: 4 * gib,
		Roles: []AuditRole{{Role: "main", Model: "only", ContextTarget: 8192, FileBytes: 2 * gib, LoadedBytes: 2*gib + 96*1024*8192, Fits: true}},
	}
	rep := CapacityReport(a, CapacityOptions{MaxJobs: 1, MaxLLMCalls: 1})
	if !rep.Coverage.SingleModel {
		t.Error("SingleModel = false, want true")
	}
	if rep.Coverage.CodeExecution {
		t.Error("CodeExecution = true without Podman, want false")
	}
	if !hasCapReason(rep, "single_model") {
		t.Error("missing single_model reason")
	}
}

func hasCapReason(rep capacity.Report, code string) bool {
	for _, r := range rep.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}
