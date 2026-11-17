package gateway

import (
	"context"
	"errors"
	"testing"
)

func TestParseBlocklistSplitsHostsAndNets(t *testing.T) {
	hosts, nets, err := ParseBlocklist([]byte(`
# comment
example.com
// another comment
10.0.0.0/8
203.0.113.5  # inline
bad.example.org
`))
	if err != nil {
		t.Fatalf("ParseBlocklist: %v", err)
	}
	if len(hosts) != 2 || hosts[0] != "example.com" || hosts[1] != "bad.example.org" {
		t.Errorf("hosts = %v", hosts)
	}
	if len(nets) != 2 || nets[0] != "10.0.0.0/8" || nets[1] != "203.0.113.5" {
		t.Errorf("nets = %v", nets)
	}
}

func TestParseBlocklistRejectsBadCIDR(t *testing.T) {
	if _, _, err := ParseBlocklist([]byte("10.0.0.0/99\n")); err == nil {
		t.Fatal("bad CIDR accepted")
	}
}

func TestPolicyBlocklistDeniesResolvedIP(t *testing.T) {
	p, err := NewPolicyWithOptions(PolicyOptions{
		DefaultPolicy: "deny",
		AllowList:     []string{"blocked.test"},
		DenyNets:      []string{"1.1.1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Eval(context.Background(), "https://blocked.test/a", &fixedResolver{})
	if !errors.Is(err, ErrDeniedBlocklist) {
		t.Fatalf("Eval = %#v, %v; want ErrDeniedBlocklist", res, err)
	}
	if res.Decision != DecisionDeniedBlocklist || res.Reason != "denied_blocklist" {
		t.Errorf("result = %#v", res)
	}
}
