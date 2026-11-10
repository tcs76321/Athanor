package main

import (
	"context"
	"errors"
	"testing"

	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

type fakeGatewayFetch struct {
	calls int
	res   *toolenvelope.FetchURLResponse
	err   error
}

func (f *fakeGatewayFetch) FetchURL(context.Context, toolenvelope.FetchURLRequest) (*toolenvelope.FetchURLResponse, error) {
	f.calls++
	return f.res, f.err
}

type fakeEnvelopes struct {
	env toolenvelope.Envelope
	err error
}

func (f fakeEnvelopes) EnvelopeFor(context.Context, string, toolenvelope.Envelope) (toolenvelope.Envelope, error) {
	return f.env, f.err
}

func fetchEnvelope(t *testing.T, tools ...string) toolenvelope.Envelope {
	t.Helper()
	env, err := toolenvelope.Parse(tools)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestCoreFetchRespectsEnvelope(t *testing.T) {
	gw := &fakeGatewayFetch{res: &toolenvelope.FetchURLResponse{Markdown: "hello"}}
	c := &coreFetch{gateway: gw, envelopes: fakeEnvelopes{env: fetchEnvelope(t)}}
	_, err := c.FetchURL(context.Background(), "job-1", toolenvelope.FetchURLRequest{URL: "https://example.com"})
	if !errors.Is(err, toolenvelope.ErrToolDisallowed) {
		t.Fatalf("err = %v, want ErrToolDisallowed", err)
	}
	if gw.calls != 0 {
		t.Errorf("gateway called %d times for a disallowed tool", gw.calls)
	}

	c.envelopes = fakeEnvelopes{env: fetchEnvelope(t, "fetch_url")}
	res, err := c.FetchURL(context.Background(), "job-1", toolenvelope.FetchURLRequest{URL: "https://example.com"})
	if err != nil {
		t.Fatalf("allowed fetch: %v", err)
	}
	if res.Markdown != "hello" || gw.calls != 1 {
		t.Errorf("res = %+v calls=%d", res, gw.calls)
	}
}

func TestCoreFetchMapsPolicyDenial(t *testing.T) {
	gw := &fakeGatewayFetch{err: internalapi.ErrFetchDenied}
	c := &coreFetch{gateway: gw, envelopes: fakeEnvelopes{env: fetchEnvelope(t, "fetch_url")}}
	_, err := c.FetchURL(context.Background(), "job-1", toolenvelope.FetchURLRequest{URL: "https://off.test"})
	if !errors.Is(err, toolenvelope.ErrPolicyDenied) {
		t.Fatalf("err = %v, want ErrPolicyDenied", err)
	}
}

func TestCoreFetchWithoutGatewayFails(t *testing.T) {
	c := &coreFetch{envelopes: fakeEnvelopes{env: fetchEnvelope(t, "fetch_url")}}
	if _, err := c.FetchURL(context.Background(), "job-1", toolenvelope.FetchURLRequest{}); err == nil {
		t.Fatal("nil gateway accepted")
	}
}

func TestCoreFetchSurfacesEnvelopeErrors(t *testing.T) {
	gw := &fakeGatewayFetch{res: &toolenvelope.FetchURLResponse{}}
	c := &coreFetch{gateway: gw, envelopes: fakeEnvelopes{err: errors.New("task missing")}}
	if _, err := c.FetchURL(context.Background(), "job-1", toolenvelope.FetchURLRequest{}); err == nil {
		t.Fatal("envelope error swallowed")
	}
	if gw.calls != 0 {
		t.Errorf("gateway called after an envelope error")
	}
}
