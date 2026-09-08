package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	nurl "net/url"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"
	bluemonday "github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"

	"github.com/tcs76321/athanor/internal/airlock/scanner"
	"github.com/tcs76321/athanor/internal/store"
)

// Reader is the §21.5 responsibility 6 surface (M4-T6, ADR-0018 §4).
// It consumes the raw `*Response` a Client.Fetch returns and produces
// markdown via the closed pipeline:
//
//   fetch (T5) → content-type gate → readability extraction →
//   bluemonday sanitization → markdown rendering → prompt-injection scan
//
// Consumers (T7's fetch_url tool, M6's cloud mediation) take the
// interface, not the concrete type, so tests inject a fake exactly
// the way ADR-0017 §1 does for Client.
type Reader interface {
	// Extract runs the Reader Mode pipeline over a fetched Response
	// and returns the extracted markdown draft. Errors are typed
	// (ADR-0018 §3):
	//
	//   ErrReaderDisabled    — network.reader_mode_default=false.
	//   ErrNotReadable       — the Content-Type is not HTML/text.
	//   ErrNoReadableContent — readability found no main content.
	//   ErrPromptInjection   — the markdown tripped the injection
	//                          heuristic (fail-closed: the raw HTML
	//                          is never returned as a fallback).
	Extract(ctx context.Context, resp *Response) (*ReaderResult, error)
}

// ReaderMode names the extraction path that produced a ReaderResult.
type ReaderMode string

const (
	// ModeReadability: the response was HTML and went through the
	// readability/sanitize/markdown pipeline.
	ModeReadability ReaderMode = "readability"
	// ModePlain: the response was text/plain and passed through
	// verbatim (still injection-scanned).
	ModePlain ReaderMode = "plain"
)

// ReaderResult is the product of a Reader Mode extraction.
type ReaderResult struct {
	// Title, SiteName, Excerpt come from the readability Article
	// metadata. They are empty for ModePlain responses (the plain
	// path has no HTML to extract metadata from).
	Title    string
	SiteName string
	Excerpt  string
	// Markdown is the extracted content, ready to hand to an LLM
	// prompt. It has passed the prompt-injection scan.
	Markdown string
	// SourceURL is the original request URL, echoed for the caller's
	// audit trail.
	SourceURL string
	// Mode is which extraction path produced the result.
	Mode ReaderMode
	// Truncated mirrors Response.Truncated. ADR-0018 defers the
	// re-fetch decision to the caller (T7).
	Truncated bool
}

// ReaderOptions configures a Reader. All fields except Logger and
// Events are required.
type ReaderOptions struct {
	// Enabled is the §21.5 reader_mode_default flag. When false,
	// Extract returns ErrReaderDisabled without touching the body.
	// Required.
	Enabled bool
	// Heuristic is the prompt-injection scanner. Required; a nil
	// heuristic means "do not scan", which this package deliberately
	// does not support (fail-closed, ADR-0018 §3 step 5).
	Heuristic scanner.Scanner
	// Events is the audit-log surface (same seam as Client). A nil
	// Events means "no audit".
	Events EventLogger
	// Logger is the slog logger. nil → slog.Default().
	Logger *slog.Logger
}

// NewReader constructs the production Reader from ReaderOptions.
// It validates that the heuristic is present.
func NewReader(opts ReaderOptions) (Reader, error) {
	if opts.Heuristic == nil {
		return nil, errors.New("gateway: ReaderOptions.Heuristic is required (fail-closed)")
	}
	return &reader{
		enabled:   opts.Enabled,
		heuristic: opts.Heuristic,
		events:    opts.Events,
		logger:    opts.Logger,
	}, nil
}

// reader is the production Reader. It is stateless apart from the
// injected dependencies, so it is safe for concurrent Extract calls.
type reader struct {
	enabled   bool
	heuristic scanner.Scanner
	events    EventLogger
	logger    *slog.Logger
}

// Extract implements the Reader Mode pipeline (ADR-0018 §3). See
// Reader.Extract for the error contract.
func (r *reader) Extract(ctx context.Context, resp *Response) (*ReaderResult, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("gateway: Extract requires a fetched Response with a Body")
	}
	if !r.enabled {
		return nil, ErrReaderDisabled
	}

	// Step 1: content-type gate. The `response.Header` is the raw
	// `http.Header` from the fetch; Content-Type may carry a charset
	// parameter ("text/html; charset=utf-8").
	raw := resp.Header.Get("Content-Type")
	ctype := strings.ToLower(strings.TrimSpace(strings.Split(raw, ";")[0]))
	switch ctype {
	case "text/html", "application/xhtml+xml":
		return r.extractHTML(ctx, resp)
	case "text/plain":
		md := string(resp.Body)
		if err := r.scanAndReject(ctx, resp.URL, md); err != nil {
			return nil, err
		}
		r.auditApplied(ctx, resp, ModePlain, len(md))
		return &ReaderResult{
			Markdown:  md,
			SourceURL: resp.URL,
			Mode:      ModePlain,
			Truncated: resp.Truncated,
		}, nil
	default:
		// Non-HTML / non-text Content-Type (images, PDFs,
		// application/*) or an absent/unrecognized one. Fail closed.
		r.auditRejected(ctx, resp, "not_readable")
		return nil, ErrNotReadable
	}
}

// extractHTML runs the readability → sanitize → markdown →
// injection-scan path for an HTML response.
func (r *reader) extractHTML(ctx context.Context, resp *Response) (*ReaderResult, error) {
	// Step 2: readability extraction from the already-fetched bytes.
	// The gateway stays the single fetch point: FromReader parses an
	// io.Reader; only FromURL would fetch, and it is never called
	// (ADR-0018 §2).
	pageURL, err := nurl.Parse(resp.URL)
	if err != nil {
		// The URL came from a successful Client.Fetch, so it parsed
		// at the policy layer; a failure here is a genuine invariant
		// break and is treated as not-read-able.
		r.auditRejected(ctx, resp, "not_readable")
		return nil, ErrNotReadable
	}
	article, err := readability.FromReader(strings.NewReader(string(resp.Body)), pageURL)
	if err != nil {
		r.auditRejected(ctx, resp, "extraction_failed")
		return nil, fmt.Errorf("gateway: readability extraction failed: %w", err)
	}
	if article.Node == nil {
		// No main content (JS-rendered page, tiny page below the
		// CharThresholds, empty body). Fallback to raw HTML is
		// deliberately refused (ADR-0018 §3 step 2).
		r.auditRejected(ctx, resp, "no_readable_content")
		return nil, ErrNoReadableContent
	}

	// Step 3: render the extracted article to HTML, then sanitize
	// with bluemonday's UGCPolicy (the maintained "untrusted HTML that
	// will be re-rendered" allowlist).
	var articleHTML bytes.Buffer
	if err := article.RenderHTML(&articleHTML); err != nil {
		r.auditRejected(ctx, resp, "render_failed")
		return nil, fmt.Errorf("gateway: article render failed: %w", err)
	}
	sanitized := bluemonday.UGCPolicy().Sanitize(articleHTML.String())

	// Step 4: markdown rendering from the sanitized tree.
	doc, err := html.Parse(strings.NewReader(sanitized))
	if err != nil {
		// bluemonday's output is well-formed; a parse failure here
		// means the sanitizer's contract changed. Fail closed.
		r.auditRejected(ctx, resp, "md_parse_failed")
		return nil, fmt.Errorf("gateway: markdown parse failed: %w", err)
	}
	md := renderMarkdown(doc)

	// Step 5: prompt-injection scan over the exact bytes handed to
	// the caller. Non-clean → no markdown leaves the reader.
	if err := r.scanAndReject(ctx, resp.URL, md); err != nil {
		return nil, err
	}

	r.auditApplied(ctx, resp, ModeReadability, len(md))
	return &ReaderResult{
		Title:     article.Title(),
		SiteName:  article.SiteName(),
		Excerpt:   article.Excerpt(),
		Markdown:  md,
		SourceURL: resp.URL,
		Mode:      ModeReadability,
		Truncated: resp.Truncated,
	}, nil
}

// scanAndReject runs the injection heuristic over `md`. A non-clean
// verdict audits reader_mode_rejected and returns ErrPromptInjection.
// The scanner may return an error (treated as fail-closed, matching
// the registry's error → VerdictUncertain rule).
func (r *reader) scanAndReject(ctx context.Context, rawURL, md string) error {
	res, err := r.heuristic.Scan(ctx, scanner.ScanInput{
		Path:  "",
		Bytes: []byte(md),
		Size:  int64(len(md)),
	})
	if err != nil {
		r.auditRejectedURL(ctx, rawURL, "scan_error")
		return fmt.Errorf("gateway: injection scan failed: %w", err)
	}
	if res.Verdict != scanner.VerdictClean {
		r.auditRejectedURL(ctx, rawURL, "prompt_injection")
		return ErrPromptInjection
	}
	return nil
}

// auditApplied writes one `reader_mode_applied` network event.
func (r *reader) auditApplied(ctx context.Context, resp *Response, mode ReaderMode, markdownBytes int) {
	if r.events == nil {
		return
	}
	r.appendEvent(ctx, readerAppliedEvent(resp, mode, markdownBytes))
}

// auditRejected writes one `reader_mode_rejected` network event.
func (r *reader) auditRejected(ctx context.Context, resp *Response, reason string) {
	if r.events == nil {
		return
	}
	r.appendEvent(ctx, readerRejectedEvent(resp, reason))
}

// auditRejectedURL writes one `reader_mode_rejected` event from a URL
// when the scan step only has the URL (no Response).
func (r *reader) auditRejectedURL(ctx context.Context, rawURL, reason string) {
	if r.events == nil {
		return
	}
	r.appendEvent(ctx, readerRejectedEvent(&Response{URL: rawURL}, reason))
}

// appendEvent maps a Reader Mode event to a level and delegates to
// the shared audit helper.
func (r *reader) appendEvent(ctx context.Context, payload auditPayload) {
	var level store.EventLevel
	switch payload.Event {
	case string(EventReaderApplied):
		level = store.EventInfo
	case string(EventReaderRejected):
		level = store.EventWarn
	default:
		level = store.EventInfo
	}
	appendAudit(ctx, r.logger, r.events, level, payload)
}