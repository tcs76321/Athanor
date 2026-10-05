package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/strategy"
)

// insightActivator is the HITL-approved side effect for a strategy insight
// promotion (M6-T11, §13.4): only an approved request activates an insight.
type insightActivator struct{ repo *strategy.Repo }

func (a insightActivator) Approve(ctx context.Context, req hitl.Request) error {
	var p struct {
		InsightID string `json:"insight_id"`
	}
	if err := json.Unmarshal([]byte(req.PayloadJSON), &p); err != nil {
		return err
	}
	if p.InsightID == "" {
		return errors.New("strategy_insight request has no insight_id")
	}
	return a.repo.SetInsightStatus(ctx, p.InsightID, strategy.InsightActive)
}

// insightPromoter backs the API promote route: with auto_promote it activates
// directly, otherwise it files a HITL request and the insight stays proposed
// until approved.
type insightPromoter struct {
	strategy    *strategy.Repo
	hitl        *hitl.Repo
	autoPromote bool
}

func (p insightPromoter) Promote(ctx context.Context, insightID string) (bool, string, error) {
	if p.autoPromote {
		if err := p.strategy.SetInsightStatus(ctx, insightID, strategy.InsightActive); err != nil {
			return false, "", err
		}
		return true, "", nil
	}
	raw, err := json.Marshal(map[string]string{"insight_id": insightID})
	if err != nil {
		return false, "", err
	}
	req, err := p.hitl.Create(ctx, hitl.Request{
		Type: hitl.TypeStrategyInsight, Severity: hitl.SeverityMedium, PayloadJSON: string(raw),
	})
	if err != nil {
		return false, "", err
	}
	return false, req.ID, nil
}
