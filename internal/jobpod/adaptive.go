package jobpod

import "math"

// AdaptiveLimits scales the configured sandbox ceiling to the task's primary
// capability. The configured job_pod limits are a ceiling for the largest task,
// not a fixed size for every pod: a code task needs the full build/test
// sandbox, while a text or document task should not reserve it. The result is
// still bounded by the configured ceiling.
func AdaptiveLimits(base Limits, capability string) Limits {
	l := withDefaults(base)
	fraction := 0.75 // unknown/legacy tasks get a conservative share
	switch capability {
	case "code", "media":
		fraction = 1
	case "data":
		fraction = 0.75
	case "document":
		fraction = 0.5
	case "text":
		fraction = 0.4
	}
	l.MemoryMB = roundStep(int(float64(l.MemoryMB)*fraction), 512, 512)
	l.CPUs = roundFloatStep(l.CPUs*fraction, 0.25, 0.5)
	l.TmpfsMB = roundStep(int(float64(l.TmpfsMB)*fraction), 256, 256)
	l.PidsLimit = roundStep(int(float64(l.PidsLimit)*fraction), 1, 64)
	return l
}

func roundStep(n, step, min int) int {
	if n < min {
		n = min
	}
	return int(math.Round(float64(n)/float64(step))) * step
}

func roundFloatStep(v, step, min float64) float64 {
	if v < min {
		v = min
	}
	return math.Round(v/step) * step
}
