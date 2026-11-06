package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCappedStreamKeepsHeadAndMarksTruncation(t *testing.T) {
	c := &cappedStream{limit: 8}
	if _, err := c.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := string(c.Bytes())
	if !strings.HasPrefix(got, "01234567") {
		t.Fatalf("retained prefix = %q, want 01234567", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("truncated stream missing marker: %q", got)
	}
	if bytes.Contains(c.Bytes(), []byte("89abcdef")) {
		t.Fatalf("stream retained more than its limit: %q", got)
	}
}

func TestCappedStreamReturnsFullWrite(t *testing.T) {
	c := &cappedStream{limit: 4}
	n, err := c.Write([]byte("abcdef"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 6 {
		t.Fatalf("Write returned %d, want 6 (a short write would signal EPIPE)", n)
	}
}
