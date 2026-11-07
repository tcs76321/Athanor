package capacity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func admissionReport() Report {
	return Report{
		Solo:       &Slot{Model: "solo", Context: 32768},
		SwarmPods:  &Swarm{Class: "pods", Slots: 3, Context: 8192},
		SwarmLight: &Swarm{Class: "light", Slots: 2, Context: 8192},
	}
}

func TestAdmissionLaneCap(t *testing.T) {
	a := NewAdmission(admissionReport())
	if got := a.MaxConcurrentJobs(); got != 3 {
		t.Errorf("MaxConcurrentJobs = %d, want 3", got)
	}
	ctx := context.Background()
	var releases []func()
	for i := 0; i < 3; i++ {
		rel, err := a.Acquire(ctx, LanePods)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, rel)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := a.Acquire(short, LanePods); err == nil {
		t.Fatal("fourth pod acquired, want a wait")
	}
	for _, rel := range releases {
		rel()
	}
	if rel, err := a.Acquire(ctx, LanePods); err != nil {
		t.Fatalf("acquire after release: %v", err)
	} else {
		rel()
	}
}

func TestAdmissionMixedUsesSmallerPlan(t *testing.T) {
	a := NewAdmission(admissionReport())
	ctx := context.Background()
	relPod, err := a.Acquire(ctx, LanePods)
	if err != nil {
		t.Fatal(err)
	}
	relLight, err := a.Acquire(ctx, LaneLight)
	if err != nil {
		t.Fatal(err)
	}
	// pods(3) + light(2) mixed caps at min(3,2)=2; a third mixed job waits.
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := a.Acquire(short, LaneLight); err == nil {
		t.Fatal("third mixed job acquired, want a wait")
	}
	relPod()
	relLight()
}

func TestAdmissionSoloReserved(t *testing.T) {
	a := NewAdmission(admissionReport())
	rel, err := a.Acquire(context.Background(), LaneSolo)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := a.Acquire(short, LaneSolo); err == nil {
		t.Fatal("second solo job acquired, want a wait")
	}
	rel()
}

func TestAdmissionNoLane(t *testing.T) {
	a := NewAdmission(Report{})
	_, err := a.Acquire(context.Background(), LaneLight)
	if !errors.Is(err, ErrNoLane) {
		t.Fatalf("err = %v, want ErrNoLane", err)
	}
}
