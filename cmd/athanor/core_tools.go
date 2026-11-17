package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// gatewayFetcher is the Core's direct fetch surface (the concrete type is
// *gatewayToolAdapter; the interface exists so the routing is testable).
type gatewayFetcher interface {
	FetchURL(ctx context.Context, req toolenvelope.FetchURLRequest) (*toolenvelope.FetchURLResponse, error)
	SearchWeb(ctx context.Context, req toolenvelope.SearchWebRequest) (*toolenvelope.SearchWebResponse, error)
}

// envelopeLookup resolves a job's tool envelope. *project.Repo implements it.
type envelopeLookup interface {
	EnvelopeFor(ctx context.Context, jobID string, defaultEnv toolenvelope.Envelope) (toolenvelope.Envelope, error)
}

// coreFetch executes fetch_url in the Core. fetch_url is a Core-executed
// gateway tool (ADR-0019 §5): it must not require a Job Pod. The pod-token
// client is still the executor for code tools, so this runner keeps the same
// envelope check and the same typed errors the internal API would return.
type coreFetch struct {
	gateway    gatewayFetcher
	envelopes  envelopeLookup
	defaultEnv toolenvelope.Envelope
}

func (c *coreFetch) FetchURL(ctx context.Context, jobID string, req toolenvelope.FetchURLRequest) (toolenvelope.FetchURLResponse, error) {
	if c.gateway == nil {
		return toolenvelope.FetchURLResponse{}, errors.New("core fetch: gateway is not configured")
	}
	if c.envelopes != nil {
		env, err := c.envelopes.EnvelopeFor(ctx, jobID, c.defaultEnv)
		if err != nil {
			return toolenvelope.FetchURLResponse{}, fmt.Errorf("core fetch: tool envelope: %w", err)
		}
		if !env.Allows(toolenvelope.ToolFetchURL) {
			return toolenvelope.FetchURLResponse{}, toolenvelope.ErrToolDisallowed
		}
	}
	req.JobID = jobID
	res, err := c.gateway.FetchURL(ctx, req)
	switch {
	case errors.Is(err, internalapi.ErrFetchDenied):
		return toolenvelope.FetchURLResponse{}, toolenvelope.ErrPolicyDenied
	case err != nil:
		return toolenvelope.FetchURLResponse{}, err
	case res == nil:
		return toolenvelope.FetchURLResponse{}, errors.New("core fetch: empty response")
	}
	return *res, nil
}

// SearchWeb executes search_web in the Core, with the same envelope check and
// typed-error mapping as FetchURL.
func (c *coreFetch) SearchWeb(ctx context.Context, jobID string, req toolenvelope.SearchWebRequest) (toolenvelope.SearchWebResponse, error) {
	if c.gateway == nil {
		return toolenvelope.SearchWebResponse{}, errors.New("core search: gateway is not configured")
	}
	if c.envelopes != nil {
		env, err := c.envelopes.EnvelopeFor(ctx, jobID, c.defaultEnv)
		if err != nil {
			return toolenvelope.SearchWebResponse{}, fmt.Errorf("core search: tool envelope: %w", err)
		}
		if !env.Allows(toolenvelope.ToolSearchWeb) {
			return toolenvelope.SearchWebResponse{}, toolenvelope.ErrToolDisallowed
		}
	}
	req.JobID = jobID
	res, err := c.gateway.SearchWeb(ctx, req)
	switch {
	case errors.Is(err, internalapi.ErrFetchDenied):
		return toolenvelope.SearchWebResponse{}, toolenvelope.ErrPolicyDenied
	case errors.Is(err, internalapi.ErrSearchNotConfigured):
		return toolenvelope.SearchWebResponse{}, err
	case err != nil:
		return toolenvelope.SearchWebResponse{}, err
	case res == nil:
		return toolenvelope.SearchWebResponse{}, errors.New("core search: empty response")
	}
	return *res, nil
}

// hybridRunner sends Core tools to the Core and pod tools to the Job Pod.
// Without this split, a text-only cycle (research, fetch, condense) fails every
// fetch with "no active pod" even though no pod is needed.
type hybridRunner struct {
	pod  engine.ToolRunner
	core *coreFetch
}

func (h hybridRunner) RunCode(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return h.pod.RunCode(ctx, jobID, req)
}

func (h hybridRunner) RunTests(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return h.pod.RunTests(ctx, jobID, req)
}

func (h hybridRunner) RunLint(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	return h.pod.RunLint(ctx, jobID, req)
}

func (h hybridRunner) ReadFile(ctx context.Context, jobID string, req toolenvelope.ReadFileRequest) (toolenvelope.ReadFileResponse, error) {
	return h.pod.ReadFile(ctx, jobID, req)
}

func (h hybridRunner) FetchURL(ctx context.Context, jobID string, req toolenvelope.FetchURLRequest) (toolenvelope.FetchURLResponse, error) {
	return h.core.FetchURL(ctx, jobID, req)
}

func (h hybridRunner) SearchWeb(ctx context.Context, jobID string, req toolenvelope.SearchWebRequest) (toolenvelope.SearchWebResponse, error) {
	return h.core.SearchWeb(ctx, jobID, req)
}
