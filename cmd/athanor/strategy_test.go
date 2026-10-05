package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
	"github.com/tcs76321/athanor/migrations"
)

func TestInsightActivatorAndPromoter(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := strategy.NewRepo(s)

	mk := func() strategy.Insight {
		t.Helper()
		ins, err := repo.CreateInsight(ctx, strategy.Insight{
			Scope: strategy.ScopeGlobal, Polarity: strategy.PolarityWinning,
			Pattern:   strategy.Pattern{Feature: "diverging.persona", Value: "alternative", Context: "archetype=code"},
			Statement: "s", Status: strategy.InsightProposed,
		})
		if err != nil {
			t.Fatal(err)
		}
		return ins
	}

	// The HITL-approved side effect activates the insight.
	ins := mk()
	payload := fmt.Sprintf(`{"insight_id":%q}`, ins.ID)
	if err := (insightActivator{repo: repo}).Approve(ctx, hitl.Request{ID: "r1", PayloadJSON: payload}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got, _ := repo.GetInsight(ctx, ins.ID); got.Status != strategy.InsightActive {
		t.Errorf("status = %q, want active", got.Status)
	}

	// Without auto-promote, Promote files a request and leaves it proposed.
	ins2 := mk()
	promoter := insightPromoter{strategy: repo, hitl: hitl.NewRepo(s), autoPromote: false}
	activated, requestID, err := promoter.Promote(ctx, ins2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if activated || requestID == "" {
		t.Fatalf("promote = %v/%q, want a HITL request", activated, requestID)
	}
	if got, _ := repo.GetInsight(ctx, ins2.ID); got.Status != strategy.InsightProposed {
		t.Errorf("status = %q, want proposed before approval", got.Status)
	}

	// With auto-promote, it activates directly.
	promoter2 := insightPromoter{strategy: repo, hitl: hitl.NewRepo(s), autoPromote: true}
	activated2, _, err := promoter2.Promote(ctx, ins2.ID)
	if err != nil || !activated2 {
		t.Fatalf("auto-promote = %v, %v", activated2, err)
	}
	if got, _ := repo.GetInsight(ctx, ins2.ID); got.Status != strategy.InsightActive {
		t.Errorf("status = %q, want active", got.Status)
	}
}
