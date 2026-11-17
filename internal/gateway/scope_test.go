package gateway

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"
)

func TestGrantMatchesPathPrefix(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	g := Grant{Scheme: "https", Host: "example.com", PathPrefix: "/v1/"}

	cases := []struct {
		raw  string
		want bool
	}{
		{"https://example.com/v1/", true},
		{"https://example.com/v1/items", true},
		{"https://example.com/v1", true},
		{"https://example.com/v10/items", false},
		{"https://example.com/v1/../admin", false},
		{"https://example.com/v1/%2e%2e/admin", false},
		{"https://example.com/", false},
		{"http://example.com/v1/a", false},
		{"https://api.example.com/v1/a", false}, // exact host, not a subdomain
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		if got := g.Matches(u, now); got != tc.want {
			t.Errorf("Matches(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestGrantExpires(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	u, _ := url.Parse("https://example.com/")
	if !(Grant{Scheme: "https", Host: "example.com", ExpiresAt: now.Add(time.Hour)}).Matches(u, now) {
		t.Error("unexpired grant did not match")
	}
	if (Grant{Scheme: "https", Host: "example.com", ExpiresAt: now}).Matches(u, now) {
		t.Error("expired grant matched")
	}
}

func TestEvalScopedPrecedence(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	resolver := &fixedResolver{}

	globalAllow, err := NewPolicy("deny", []string{"example.com"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A project deny overrides a global allow.
	res, err := globalAllow.EvalScoped(ctx, "https://example.com/a", Scope{Deny: []string{"example.com"}}, now, resolver)
	if !errors.Is(err, ErrDeniedDenyList) || res.Reason != "denied_project_denylist" {
		t.Fatalf("project deny = %#v, %v", res, err)
	}

	// A grant allows an otherwise off-list host.
	denyAll, _ := NewPolicy("deny", nil, nil)
	res, err = denyAll.EvalScoped(ctx, "https://granted.test/a", Scope{
		Grants: []Grant{{Scheme: "https", Host: "granted.test", PathPrefix: "/a"}},
	}, now, resolver)
	if err != nil || res.Decision != DecisionAllowed || res.Reason != "allowed_grant" {
		t.Fatalf("grant = %#v, %v", res, err)
	}

	// A global deny cannot be overridden by a grant.
	globalDeny, _ := NewPolicy("deny", []string{"blocked.test"}, []string{"blocked.test"})
	res, err = globalDeny.EvalScoped(ctx, "https://blocked.test/a", Scope{
		Grants: []Grant{{Scheme: "https", Host: "blocked.test"}},
	}, now, resolver)
	if !errors.Is(err, ErrDeniedDenyList) {
		t.Fatalf("global deny overridden: %#v, %v", res, err)
	}

	// An expired grant falls through to default deny.
	res, err = denyAll.EvalScoped(ctx, "https://expired.test/a", Scope{
		Grants: []Grant{{Scheme: "https", Host: "expired.test", ExpiresAt: now.Add(-time.Minute)}},
	}, now, resolver)
	if !errors.Is(err, ErrDeniedOffList) {
		t.Fatalf("expired grant = %#v, %v", res, err)
	}

	// The private-IP guard still runs after a grant.
	res, err = denyAll.EvalScoped(ctx, "https://private.test/a", Scope{
		Grants: []Grant{{Scheme: "https", Host: "private.test"}},
	}, now, &fixedResolver{ips: map[string][]net.IP{"private.test": {net.ParseIP("127.0.0.1")}}})
	if !errors.Is(err, ErrDeniedPrivateIP) {
		t.Fatalf("grant bypassed the private-IP guard: %#v, %v", res, err)
	}
}

func TestProfilesAreStableAndOptIn(t *testing.T) {
	local, ok := ProfileByName("local-only")
	if !ok || len(local.Allow) != 0 {
		t.Fatalf("local-only = %#v, %v", local, ok)
	}
	research, ok := ProfileByName("research")
	if !ok {
		t.Fatal("research profile missing")
	}
	hasGoogle := false
	for _, h := range research.Allow {
		if h == "google.com" {
			hasGoogle = true
		}
	}
	if !hasGoogle {
		t.Errorf("research profile does not allow the Google search host: %+v", research.Allow)
	}
	for _, p := range Profiles() {
		for _, d := range p.Deny {
			if d == "169.254.169.254" || d == "metadata.google.internal" {
				return
			}
		}
	}
	t.Error("no profile carries the cloud-metadata deny entries")
}
