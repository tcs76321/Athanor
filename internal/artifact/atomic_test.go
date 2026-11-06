package artifact

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteContentAtomicReplacesSymlinkWithoutFollowing(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "content")
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := writeContentAtomic(target, []byte("artifact")); err != nil {
		t.Fatalf("writeContentAtomic: %v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "do-not-touch" {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "artifact" {
		t.Fatalf("target = %q, %v", got, err)
	}
	if st, err := os.Lstat(target); err != nil || st.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target is still a symlink: %v, %v", st, err)
	}
}
