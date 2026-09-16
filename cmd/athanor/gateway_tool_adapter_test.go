package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/gateway"
	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// fakeGatewayClient records the URLs Fetch was called with and
// returns canned responses (or an error) in sequence.
type fakeGatewayClient struct {
	urls      []string
	responses []*gateway.Response
	err       error
}

func (f *fakeGatewayClient) Fetch(_ context.Context, req *http.Request) (*gateway.Response, error) {
	f.urls = append(f.urls, req.URL.String())
	if f.err != nil {
		return nil, f.err
	}
	if len(f.responses) == 0 {
		return &gateway.Response{StatusCode: 200, URL: req.URL.String(), Header: http.Header{}, Body: []byte("ok")}, nil
	}
	res := f.responses[0]
	f.responses = f.responses[1:]
	return res, nil
}

// fakeReader returns a canned ReaderResult or error.
type fakeReader struct {
	result *gateway.ReaderResult
	err    error
}

func (f *fakeReader) Extract(_ context.Context, resp *gateway.Response) (*gateway.ReaderResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &gateway.ReaderResult{Markdown: string(resp.Body), Mode: gateway.ModePlain, SourceURL: resp.URL}, nil
}

func newAdapter(parts GatewayParts, template string) *gatewayToolAdapter {
	return newGatewayToolAdapter(parts, template)
}

func TestAdapterFetchURL_HappyPath(t *testing.T) {
	client := &fakeGatewayClient{responses: []*gateway.Response{{
		StatusCode: 200, URL: "https://a.test/page", Header: http.Header{"Content-Type": []string{"text/html"}},
		Body: []byte("<html><body>ignored raw</body></html>"),
	}}}
	reader := &fakeReader{result: &gateway.ReaderResult{
		Title: "A Page", Markdown: "# extracted", Mode: gateway.ModeReadability, SourceURL: "https://a.test/page",
	}}
	a := newAdapter(GatewayParts{Client: client, Reader: reader}, "")

	res, err := a.FetchURL(context.Background(), toolenvelope.FetchURLRequest{URL: "https://a.test/page"})
	if err != nil {
		t.Fatalf("FetchURL: %v", err)
	}
	if res.Markdown != "# extracted" || res.Mode != "readability" || res.Title != "A Page" {
		t.Errorf("res = %+v, want the extracted markdown", res)
	}
	if res.StatusCode != 200 || res.ContentType != "text/html" {
		t.Errorf("res = %+v, want status + content type echoed", res)
	}
	if len(client.urls) != 1 || client.urls[0] != "https://a.test/page" {
		t.Errorf("client fetched %v, want one fetch of the request URL", client.urls)
	}
}

func TestFetchURL_NoRawBytesEver(t *testing.T) {
	// The raw body must never surface as markdown, even when the
	// reader refuses extraction (ADR-0019 §1: a tool response is
	// prompt material; it must have passed the injection scan).
	client := &fakeGatewayClient{responses: []*gateway.Response{{
		StatusCode: 200, URL: "https://a.test/x", Header: http.Header{"Content-Type": []string{"application/pdf"}},
		Body: []byte("%PDF-1.4 hostile bytes"),
	}}}
	reader := &fakeReader{err: gateway.ErrNotReadable}
	a := newAdapter(GatewayParts{Client: client, Reader: reader}, "")

	res, err := a.FetchURL(context.Background(), toolenvelope.FetchURLRequest{URL: "https://a.test/x"})
	if err != nil {
		t.Fatalf("FetchURL: %v", err)
	}
	if res.Markdown != "" || res.Mode != "raw" {
		t.Errorf("res = %+v, want empty markdown with mode=raw", res)
	}
	if strings.Contains(res.Markdown, "PDF") {
		t.Error("raw bytes leaked into the tool response")
	}
}

func TestFetchURL_InjectionFailsClosed(t *testing.T) {
	client := &fakeGatewayClient{}
	reader := &fakeReader{err: gateway.ErrPromptInjection}
	a := newAdapter(GatewayParts{Client: client, Reader: reader}, "")

	_, err := a.FetchURL(context.Background(), toolenvelope.FetchURLRequest{URL: "https://a.test/"})
	if !errors.Is(err, internalapi.ErrContentRejected) {
		t.Fatalf("err = %v, want ErrContentRejected", err)
	}
}

func TestFetchURL_PolicyDeniedMapsToFetchDenied(t *testing.T) {
	client := &fakeGatewayClient{err: gateway.ErrDeniedOffList}
	a := newAdapter(GatewayParts{Client: client, Reader: &fakeReader{}}, "")

	_, err := a.FetchURL(context.Background(), toolenvelope.FetchURLRequest{URL: "https://off.test/"})
	if !errors.Is(err, internalapi.ErrFetchDenied) {
		t.Fatalf("err = %v, want ErrFetchDenied", err)
	}
}

// multiResultReader hands each Extract call the next canned result
// (used by the truncated-re-fetch test to give the retry a different
// outcome than the first pass).
type multiResultReader struct {
	results []*gateway.ReaderResult
	calls   int
}

func (m *multiResultReader) Extract(_ context.Context, _ *gateway.Response) (*gateway.ReaderResult, error) {
	idx := m.calls
	if idx >= len(m.results) {
		idx = len(m.results) - 1
	}
	m.calls++
	return m.results[idx], nil
}

// capFactory records the caps requested through the NewClientWithCap
// seam and returns a client carrying the second canned response.
type capFactory struct {
	caps   []int64
	second *gateway.Response
}

func (f *capFactory) NewClientWithCap(capBytes int64) (gateway.Client, error) {
	f.caps = append(f.caps, capBytes)
	return &fakeGatewayClient{responses: []*gateway.Response{f.second}}, nil
}

func TestFetchURL_TruncatedRefetchChoosesBetter(t *testing.T) {
	primary := &fakeGatewayClient{responses: []*gateway.Response{{
		StatusCode: 200, URL: "https://a.test/big", Header: http.Header{}, Truncated: true,
	}}}
	cf := &capFactory{second: &gateway.Response{StatusCode: 200, URL: "https://a.test/big", Header: http.Header{}}}
	a := newAdapter(GatewayParts{
		Client: primary,
		Reader: &multiResultReader{results: []*gateway.ReaderResult{
			{Markdown: "short", Mode: gateway.ModePlain, Truncated: true},
			{Markdown: "much longer second extraction", Mode: gateway.ModePlain},
		}},
		NewClientWithCap: cf.NewClientWithCap,
	}, "")

	res, err := a.FetchURL(context.Background(), toolenvelope.FetchURLRequest{URL: "https://a.test/big"})
	if err != nil {
		t.Fatalf("FetchURL: %v", err)
	}
	if len(cf.caps) != 1 || cf.caps[0] != 10485760/2 {
		t.Errorf("caps = %v, want one retry at half the default", cf.caps)
	}
	if res.Markdown != "much longer second extraction" {
		t.Errorf("markdown = %q, want the retry's larger extraction", res.Markdown)
	}
	if !res.Truncated {
		t.Error("Truncated = false, want true (either pass truncated)")
	}
}

func TestSearchWeb_NotConfigured(t *testing.T) {
	a := newAdapter(GatewayParts{Client: &fakeGatewayClient{}, Reader: &fakeReader{}}, "   ")
	_, err := a.SearchWeb(context.Background(), toolenvelope.SearchWebRequest{Query: "q"})
	if !errors.Is(err, internalapi.ErrSearchNotConfigured) {
		t.Fatalf("err = %v, want ErrSearchNotConfigured", err)
	}
}

func TestSearchWeb_HappyPathEscapesQuery(t *testing.T) {
	client := &fakeGatewayClient{responses: []*gateway.Response{{
		StatusCode: 200, URL: "engine", Header: http.Header{},
	}}}
	reader := &fakeReader{result: &gateway.ReaderResult{
		Markdown: "- [Result One](https://r.test/1) — first.\n- [Result Two](https://r.test/2) second",
		Mode:     gateway.ModeReadability,
	}}
	a := newAdapter(GatewayParts{Client: client, Reader: reader}, "https://html.duckduckgo.com/html/?q={{.Query}}")

	res, err := a.SearchWeb(context.Background(), toolenvelope.SearchWebRequest{Query: "local first & agents"})
	if err != nil {
		t.Fatalf("SearchWeb: %v", err)
	}
	if len(client.urls) != 1 {
		t.Fatalf("fetches = %v, want exactly one", client.urls)
	}
	// The query must be URL-escaped before templating: spaces become
	// + (or %20), & becomes %26, and no raw syntax survives.
	fetched := client.urls[0]
	if !strings.HasPrefix(fetched, "https://html.duckduckgo.com/html/?q=") {
		t.Errorf("fetched URL = %q, want the template prefix", fetched)
	}
	if strings.Contains(fetched, "local first") || strings.Count(fetched, "&") > 1 {
		t.Errorf("fetched URL = %q, want the query escaped (raw & would inject URL syntax)", fetched)
	}
	if res.Engine != "html.duckduckgo.com" {
		t.Errorf("engine = %q, want the template host", res.Engine)
	}
	if len(res.Results) != 2 || res.Results[0].URL != "https://r.test/1" {
		t.Errorf("results = %+v, want two extracted hits", res.Results)
	}
}