// protocol-json renders the YAML run protocol as JSON so the stdlib-only
// Python analyzer can read the claim vector without a YAML dependency (or a
// second, divergent parser). It is also the bridge the verify gate uses.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func runProtocolJSON(args []string) {
	fs := flag.NewFlagSet("protocol-json", flag.ExitOnError)
	protocolPath := fs.String("protocol", "eval/bench/v5/protocol.yaml", "path to the run protocol")
	outPath := fs.String("out", "", "output JSON path (default stdout)")
	_ = fs.Parse(args)

	p, err := loadProtocol(*protocolPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "protocol-json:", err)
		os.Exit(1)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "protocol-json: marshal:", err)
		os.Exit(1)
	}
	data = append(data, '\n')
	if *outPath == "" {
		if _, err := os.Stdout.Write(data); err != nil {
			fmt.Fprintln(os.Stderr, "protocol-json: write:", err)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "protocol-json: write:", err)
		os.Exit(1)
	}
}
