package main

import "testing"

func TestParsePS(t *testing.T) {
	raw := []byte(`{"models":[{"name":"ornith-1.5:9b","model":"ornith-1.5:9b"},{"model":"gemma4:12b-mlx"}]}`)
	names, err := parsePS(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "ornith-1.5:9b" || names[1] != "gemma4:12b-mlx" {
		t.Fatalf("names = %v", names)
	}
	empty, err := parsePS([]byte(`{"models":[]}`))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty ps = %v, %v", empty, err)
	}
}
