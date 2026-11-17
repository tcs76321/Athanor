package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/netgrant"
)

// networkGrantApprover turns an approved network-access HITL request into a
// project-scoped, time-bounded egress grant (ADR-0139). It runs only on
// approval; a rejected or expired request leaves the policy unchanged.
type networkGrantApprover struct {
	grants *netgrant.Repo
	ttl    time.Duration
}

// networkAccessPayload is the request payload the UI and the engine write.
// Every field is optional except host; the defaults are https and the site
// root.
type networkAccessPayload struct {
	Scheme     string `json:"scheme"`
	Host       string `json:"host"`
	PathPrefix string `json:"path_prefix"`
	Reason     string `json:"reason"`
	TTLHours   int    `json:"ttl_hours"`
}

func (a networkGrantApprover) Approve(ctx context.Context, req hitl.Request) error {
	var p networkAccessPayload
	if err := json.Unmarshal([]byte(req.PayloadJSON), &p); err != nil {
		return fmt.Errorf("network grant: decoding payload: %w", err)
	}
	if p.Host == "" {
		return fmt.Errorf("network grant: request has no host")
	}
	ttl := time.Duration(p.TTLHours) * time.Hour
	if ttl <= 0 {
		ttl = a.ttl
	}
	var expires *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl).UTC()
		expires = &t
	}
	reason := p.Reason
	if reason == "" {
		reason = "operator approved network access"
		if req.DecisionNote != "" {
			reason += ": " + req.DecisionNote
		}
	}
	if _, err := a.grants.Create(ctx, netgrant.CreateInput{
		ProjectID:  req.ProjectID,
		Scheme:     p.Scheme,
		Host:       p.Host,
		PathPrefix: p.PathPrefix,
		Reason:     reason,
		ExpiresAt:  expires,
	}); err != nil {
		return fmt.Errorf("network grant: %w", err)
	}
	return nil
}
