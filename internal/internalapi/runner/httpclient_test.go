package runner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// fakeTokens is a fixed-per-job TokenLookup for the status-mapping
// tests. The client never sends the token anywhere but the httptest
// server, which ignores it.
type fakeTokens struct{ tok string }

func (f fakeTokens) TokenFor(string) (string, error) { return f.tok, nil }

// statusServer returns an httptest server that answers every request
// with the given status and a JSON object body (so a 200 decodes).
func statusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPostTool_StatusMapping proves the O1 contract at the wire
// boundary: 451 maps to ErrPolicyDenied, 403 to ErrToolDisallowed,
// and any other non-200 to a generic error carrying neither sentinel.
func TestPostTool_StatusMapping(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr error
	}{
		{"ok", http.StatusOK, nil},
		{"envelope 403", http.StatusForbidden, toolenvelope.ErrToolDisallowed},
		{"policy 451", http.StatusUnavailableForLegalReasons, toolenvelope.ErrPolicyDenied},
		{"other 500", http.StatusInternalServerError, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(statusServer(t, tc.status).URL, fakeTokens{tok: "deadbeef"})
			_, err := c.FetchURL(context.Background(), "job-1", toolenvelope.FetchURLRequest{URL: "https://example.org/"})
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			case tc.wantErr == nil && tc.status == http.StatusOK && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case tc.wantErr == nil && tc.status != http.StatusOK:
				if err == nil {
					t.Fatalf("err = nil, want a generic error")
				}
				if errors.Is(err, toolenvelope.ErrToolDisallowed) || errors.Is(err, toolenvelope.ErrPolicyDenied) {
					t.Fatalf("err = %v, want neither sentinel", err)
				}
			}
		})
	}
}

// TestPost_StatusMapping proves the pod-tool path (execute_code /
// run_tests / lint) maps 451 to ErrPolicyDenied and 403 to
// ErrToolDisallowed too, so the two sentinels are consistent across
// both post shapes.
func TestPost_StatusMapping(t *testing.T) {
	for _, tc := range []struct {
		status  int
		wantErr error
	}{
		{http.StatusForbidden, toolenvelope.ErrToolDisallowed},
		{http.StatusUnavailableForLegalReasons, toolenvelope.ErrPolicyDenied},
	} {
		c := New(statusServer(t, tc.status).URL, fakeTokens{tok: "deadbeef"})
		_, err := c.RunTests(context.Background(), "job-1", toolenvelope.ExecuteRequest{Command: "true"})
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.wantErr)
		}
	}
}
