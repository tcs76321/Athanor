package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"text/template"

	"github.com/tcs76321/athanor/internal/gateway"
	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// gatewayToolAdapter is the M4-T7 internalapi.ToolGateway production
// implementation (ADR-0019 §2): it bridges the internal API's
// fetch_url / search_web routes onto the GatewayParts bundle (the
// §21.5 Client + Reader) that startGateway constructs at boot. The
// adapter is the only consumer of the bundle; Gate G1 rule 6 keeps
// the outbound-HTTP call sites inside internal/gateway (fetch
// requests are built with gateway.NewRequest, never
// http.NewRequestWithContext).
//
// Error mapping (ADR-0019 §1): gateway policy refusals →
// internalapi.ErrFetchDenied; reader rejections (including the
// fail-closed prompt-injection path) → internalapi.ErrContentRejected;
// everything else → internalapi.ErrFetchFailed. The empty search
// template maps to internalapi.ErrSearchNotConfigured.
type gatewayToolAdapter struct {
	parts GatewayParts
	// searchTemplate is config.network.search_engine_url_template.
	// Empty means search_web is inert (ADR-0019 §4).
	searchTemplate string
	// maxSearchResults bounds ExtractSearchHits. A fixed constant:
	// result pages carry 10–20 organic links; more is chrome.
	maxSearchResults int
}

// newGatewayToolAdapter returns the internalapi.ToolGateway for the
// daemon's GatewayParts. searchTemplate is the raw config value
// (validated at config load); an empty value leaves search_web
// inert.
func newGatewayToolAdapter(parts GatewayParts, searchTemplate string) *gatewayToolAdapter {
	return &gatewayToolAdapter{parts: parts, searchTemplate: searchTemplate, maxSearchResults: 10}
}

// FetchURL implements internalapi.ToolGateway. Flow: build the
// request (gateway.NewRequest) → Client.Fetch (policy, rate limit,
// redirect hop loop, size cap) → Reader.Extract (readability →
// sanitize → markdown → injection scan). Reader Mode refusals are
// NOT errors: the response surfaces as mode=raw with empty markdown
// (ADR-0019 §1) — the fetched bytes are audited by the gateway, and
// a tool response is prompt material that must have passed the
// injection scan, so raw bytes are never inlined.
//
// Truncated re-fetch (ADR-0019 §6): when the extraction is truncated,
// the adapter retries once with a halved response cap and keeps
// whichever pass extracted more markdown; the response's Truncated
// flag is true if either pass truncated.
func (a *gatewayToolAdapter) FetchURL(ctx context.Context, req toolenvelope.FetchURLRequest) (*toolenvelope.FetchURLResponse, error) {
	httpReq, err := gateway.NewRequest(ctx, "GET", req.URL)
	if err != nil {
		return nil, fmt.Errorf("gateway tool: build request: %w", err)
	}
	resp, err := a.parts.Client.Fetch(ctx, httpReq)
	if err != nil {
		return nil, a.mapFetchError(err)
	}
	extracted, exErr := a.extract(ctx, resp)
	if exErr != nil {
		return nil, exErr
	}
	if !extracted.truncated {
		return a.fetchResponse(resp, extracted), nil
	}

	// One re-fetch at a halved cap (ADR-0019 §6). A nil
	// NewClientWithCap skips the retry (tests may construct the
	// adapter without the factory).
	if retry := a.retryWithHalvedCap(ctx, req.URL, extracted); retry != nil {
		extracted = *retry
	}
	return a.fetchResponse(resp, extracted), nil
}

// retryWithHalvedCap re-fetches the URL with a halved response cap
// and returns the better extraction, or nil when the retry is
// impossible or produced nothing larger.
func (a *gatewayToolAdapter) retryWithHalvedCap(ctx context.Context, rawURL string, first extraction) *extraction {
	if a.parts.NewClientWithCap == nil {
		return nil
	}
	halvedClient, err := a.parts.NewClientWithCap(first.capBytes / 2)
	if err != nil {
		return nil
	}
	httpReq, err := gateway.NewRequest(ctx, "GET", rawURL)
	if err != nil {
		return nil
	}
	resp, err := halvedClient.Fetch(ctx, httpReq)
	if err != nil {
		return nil
	}
	second, exErr := a.extract(ctx, resp)
	if exErr != nil {
		return nil
	}
	if len(second.markdown) > len(first.markdown) {
		second.truncated = true // either pass truncated — the flag is the audit trail
		return &second
	}
	return nil
}

// extract runs the Reader pipeline over a fetched Response. Reader
// Mode refusals (disabled, not readable, no main content) degrade to
// a raw-mode extraction with empty markdown; only the fail-closed
// prompt-injection rejection is an error (ErrContentRejected).
func (a *gatewayToolAdapter) extract(ctx context.Context, resp *gateway.Response) (extraction, error) {
	res, err := a.parts.Reader.Extract(ctx, resp)
	if err != nil {
		switch {
		case errors.Is(err, gateway.ErrReaderDisabled),
			errors.Is(err, gateway.ErrNotReadable),
			errors.Is(err, gateway.ErrNoReadableContent):
			return extraction{
				mode: "raw", contentType: contentTypeOf(resp),
				capBytes: a.capBytes(), markdown: "",
			}, nil
		default:
			// ErrPromptInjection and scan errors are fail-closed
			// rejections (ADR-0018 §3): no markdown leaves.
			return extraction{}, errors.Join(internalapi.ErrContentRejected, err)
		}
	}
	return extraction{
		markdown: res.Markdown, mode: string(res.Mode),
		title: res.Title, siteName: res.SiteName, excerpt: res.Excerpt,
		truncated: res.Truncated, capBytes: a.capBytes(),
		contentType: contentTypeOf(resp),
	}, nil
}

// SearchWeb implements internalapi.ToolGateway (ADR-0019 §4): render
// the configured engine template with the URL-escaped query, fetch
// the results page through the gateway (policy + rate limit + size
// cap + redirect hop loop — the template host gets no allowlist
// exemption), pass the page through the Reader pipeline, and extract
// the result links. No links is a valid outcome (surfaced, not
// hidden).
func (a *gatewayToolAdapter) SearchWeb(ctx context.Context, req toolenvelope.SearchWebRequest) (*toolenvelope.SearchWebResponse, error) {
	if strings.TrimSpace(a.searchTemplate) == "" {
		return nil, internalapi.ErrSearchNotConfigured
	}
	tpl, err := template.New("search_engine_url_template").Parse(a.searchTemplate)
	if err != nil {
		// Config load validates the template; a failure here is a
		// bug, surfaced as a fetch failure.
		return nil, errors.Join(internalapi.ErrFetchFailed, err)
	}
	var buf strings.Builder
	if err := tpl.Execute(&buf, map[string]string{"Query": url.QueryEscape(req.Query)}); err != nil {
		return nil, errors.Join(internalapi.ErrFetchFailed, err)
	}
	engineURL := buf.String()

	httpReq, err := gateway.NewRequest(ctx, "GET", engineURL)
	if err != nil {
		return nil, errors.Join(internalapi.ErrFetchFailed, err)
	}
	resp, err := a.parts.Client.Fetch(ctx, httpReq)
	if err != nil {
		return nil, a.mapFetchError(err)
	}
	extracted, exErr := a.extract(ctx, resp)
	if exErr != nil {
		return nil, exErr
	}
	hits, err := gateway.ExtractSearchHits(extracted.markdown, engineURL, a.maxSearchResults)
	if err != nil {
		return nil, errors.Join(internalapi.ErrFetchFailed, err)
	}
	results := make([]toolenvelope.SearchResult, 0, len(hits))
	for _, h := range hits {
		results = append(results, toolenvelope.SearchResult{Title: h.Title, URL: h.URL, Snippet: h.Snippet})
	}
	engine := ""
	if u, perr := url.Parse(engineURL); perr == nil {
		engine = u.Hostname()
	}
	return &toolenvelope.SearchWebResponse{Query: req.Query, Engine: engine, Results: results}, nil
}

// extraction is the adapter's intermediate result between
// Client.Fetch and the tool response.
type extraction struct {
	markdown    string
	mode        string
	title       string
	siteName    string
	excerpt     string
	truncated   bool
	capBytes    int64
	contentType string
}

// capBytes reports the primary client's response cap, used as the
// base for the halved re-fetch. The adapter cannot read the concrete
// client's cap through the interface; the §21.5 default (10 MiB,
// also the shipped config default) is the documented value. The
// factory closure in startGateway knows the real configured cap; the
// adapter uses the conservative default as the divisor seed.
func (a *gatewayToolAdapter) capBytes() int64 { return 10485760 }

// fetchResponse assembles the tool response from the fetch + the
// winning extraction.
func (a *gatewayToolAdapter) fetchResponse(resp *gateway.Response, ex extraction) *toolenvelope.FetchURLResponse {
	truncated := ex.truncated || resp.Truncated
	return &toolenvelope.FetchURLResponse{
		StatusCode:  resp.StatusCode,
		URL:         resp.URL,
		Title:       ex.title,
		SiteName:    ex.siteName,
		Excerpt:     ex.excerpt,
		Markdown:    ex.markdown,
		Mode:        ex.mode,
		Truncated:   truncated,
		ContentType: ex.contentType,
	}
}

// contentTypeOf returns the upstream response's Content-Type (empty
// when the response carries none).
func contentTypeOf(resp *gateway.Response) string {
	if resp == nil || resp.Header == nil {
		return ""
	}
	return resp.Header.Get("Content-Type")
}

// mapFetchError maps a gateway.Client.Fetch error onto the
// internalapi typed errors (ADR-0019 §1).
func (a *gatewayToolAdapter) mapFetchError(err error) error {
	switch {
	case errors.Is(err, gateway.ErrDeniedOffList),
		errors.Is(err, gateway.ErrDeniedDenyList),
		errors.Is(err, gateway.ErrDeniedPrivateIP),
		errors.Is(err, gateway.ErrIDNNotSupported),
		errors.Is(err, gateway.ErrInvalidURL):
		return errors.Join(internalapi.ErrFetchDenied, err)
	default:
		// ErrRateLimited, ErrTooManyRedirects, transport errors,
		// unknown failures — all fetch failures from the caller's
		// perspective.
		return errors.Join(internalapi.ErrFetchFailed, err)
	}
}