package jobpod

import "testing"

func TestAdaptiveLimitsScaleByCapability(t *testing.T) {
	base := Limits{PidsLimit: 512, MemoryMB: 8192, CPUs: 4, TmpfsMB: 4096}
	cases := []struct {
		capability string
		memory     int
		cpus       float64
		tmpfs      int
		pids       int
	}{
		{"code", 8192, 4, 4096, 512},
		{"media", 8192, 4, 4096, 512},
		{"data", 6144, 3, 3072, 384},
		{"document", 4096, 2, 2048, 256},
		{"text", 3072, 1.5, 1536, 204},
		{"", 6144, 3, 3072, 384},
	}
	for _, tc := range cases {
		got := AdaptiveLimits(base, tc.capability)
		if got.MemoryMB != tc.memory || got.CPUs != tc.cpus || got.TmpfsMB != tc.tmpfs || got.PidsLimit != tc.pids {
			t.Errorf("AdaptiveLimits(%q) = %+v, want memory=%d cpus=%v tmpfs=%d pids=%d",
				tc.capability, got, tc.memory, tc.cpus, tc.tmpfs, tc.pids)
		}
	}
}

func TestAdaptiveLimitsKeepFloors(t *testing.T) {
	got := AdaptiveLimits(Limits{MemoryMB: 512, CPUs: 1, TmpfsMB: 256, PidsLimit: 64}, "text")
	if got.MemoryMB < 512 || got.CPUs < 0.5 || got.TmpfsMB < 256 || got.PidsLimit < 64 {
		t.Errorf("adaptive limits fell below the floors: %+v", got)
	}
}
