// broken.js — deliberate syntax error for property P5 (fallback path).
// The unclosed template literal below leaves the brace/string scanner
// unbalanced at EOF; every strategy must still round-trip the bytes.

import fs from "fs";

export function brokenTemplate() {
  const text = `this template literal is never closed
  so the scanner hits EOF mid-string;

  function fakeInsideUnclosed() {
    return 42;
  }
