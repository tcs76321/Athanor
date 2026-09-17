package broken

// This file contains a deliberate syntax error. go/parser (strategy B)
// rejects it and must fall back; tree-sitter (strategy A) recovers and
// may still find real boundaries. Either way the bytes must survive
// intact (property P5).

func Broken() int {
	x := 1
	return x

func MissingCloseBrace() int {
	// the previous function never closed
	y := 2
	return x + y
}

func AlsoBroken() string {
	return "unterminated
}
