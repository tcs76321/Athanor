package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
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

// networkRequester files a network-access HITL request when a research fetch
// is denied. It dedupes by (project, host) against the pending queue so a
// cycle that hits the same host on every source files one request, not eight.
type networkRequester struct {
	repo *hitl.Repo
	ttl  time.Duration
}

func (r networkRequester) RequestNetworkAccess(ctx context.Context, projectID, jobID, rawURL, reason string) error {
	if r.repo == nil {
		return fmt.Errorf("network request: HITL repository is not configured")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("network request: invalid URL %q", rawURL)
	}
	host := strings.ToLower(u.Hostname())
	if pending, perr := r.repo.Pending(ctx); perr == nil {
		for _, req := range pending {
			if req.ProjectID != projectID || req.Type != hitl.TypeNetworkAccess {
				continue
			}
			var p networkAccessPayload
			if json.Unmarshal([]byte(req.PayloadJSON), &p) == nil && strings.EqualFold(p.Host, host) {
				return nil
			}
		}
	}
	scheme := u.Scheme
	if scheme != "http" && scheme != "https" {
		scheme = "https"
	}
	prefix := u.Path
	if prefix == "" {
		prefix = "/"
	}
	ttl := r.ttl
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	payload, _ := json.Marshal(networkAccessPayload{
		Scheme: scheme, Host: host, PathPrefix: prefix,
		Reason: reason, TTLHours: int(ttl / time.Hour),
	})
	expires := time.Now().Add(24 * time.Hour)
	_, err = r.repo.Create(ctx, hitl.Request{
		ProjectID: projectID, JobID: jobID, Type: hitl.TypeNetworkAccess,
		Severity: hitl.SeverityMedium, PayloadJSON: string(payload),
		ExpiresAt: &expires,
	})
	if err != nil {
		return fmt.Errorf("network request: %w", err)
	}
	return nil
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
