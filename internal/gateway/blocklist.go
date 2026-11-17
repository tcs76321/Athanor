package gateway

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"
)

// maxBlocklistBytes bounds the operator's local threat-intel file so a stray
// large file cannot stall boot.
const maxBlocklistBytes = 4 << 20

// LoadBlocklistFile reads a local blocklist and splits it into host suffixes
// and IP/CIDR networks.
//
// Format: one entry per line; blank lines and `#`/`//` comments are ignored.
// A bare IP or CIDR is an IP entry; everything else is a host suffix (the same
// `.`-boundary match as the allowlist, so `example.com` covers
// `bad.example.com`). Entries are lowercased; the policy validates them at
// construction.
//
// The file is explicit and local. Athanor does not download threat intel:
// automatic feed fetching would add an outbound dependency and a supply-chain
// surface to the very component that is supposed to be the egress boundary.
func LoadBlocklistFile(path string) (hosts []string, nets []string, err error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("gateway: blocklist %s: %w", path, err)
	}
	if info.Size() > maxBlocklistBytes {
		return nil, nil, fmt.Errorf("gateway: blocklist %s exceeds %d bytes", path, maxBlocklistBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("gateway: reading blocklist %s: %w", path, err)
	}
	return ParseBlocklist(data)
}

// ParseBlocklist is the pure parser behind LoadBlocklistFile.
func ParseBlocklist(data []byte) (hosts []string, nets []string, err error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		entry := strings.TrimSpace(sc.Text())
		if entry == "" || strings.HasPrefix(entry, "#") || strings.HasPrefix(entry, "//") {
			continue
		}
		// Inline comments are a convenience for a hand-maintained list.
		if i := strings.Index(entry, " #"); i >= 0 {
			entry = strings.TrimSpace(entry[:i])
		}
		if entry == "" {
			continue
		}
		switch {
		case strings.Contains(entry, "/"):
			if _, _, cerr := net.ParseCIDR(entry); cerr != nil {
				return nil, nil, fmt.Errorf("gateway: blocklist line %d: %q is not a CIDR", line, entry)
			}
			nets = append(nets, entry)
		case net.ParseIP(entry) != nil:
			nets = append(nets, entry)
		default:
			if !isASCII(entry) {
				return nil, nil, fmt.Errorf("gateway: blocklist line %d: %q contains non-ASCII characters", line, entry)
			}
			hosts = append(hosts, strings.ToLower(entry))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("gateway: reading blocklist: %w", err)
	}
	return hosts, nets, nil
}
