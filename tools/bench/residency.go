// residency.go implements the F4 follow-up D2 check: single-model residency
// (ARCHITECTURE §12.5). Ollama's server variable OLLAMA_MAX_LOADED_MODELS is
// read by the Ollama process, which the probe does not control, so the probe
// verifies rather than sets it: it reads /api/ps and fails an arm that had
// more than one model resident at once.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// parsePS extracts the resident model names from an /api/ps response.
func parsePS(raw []byte) ([]string, error) {
	var body struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		if m.Name != "" {
			names = append(names, m.Name)
			continue
		}
		if m.Model != "" {
			names = append(names, m.Model)
		}
	}
	return names, nil
}

// loadedModels returns the names Ollama currently has resident.
func loadedModels(baseURL string) ([]string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/api/ps")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/api/ps returned %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parsePS(raw)
}
