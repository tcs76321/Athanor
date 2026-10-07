package toolenvelope

import (
	"errors"
	"reflect"
	"testing"
)

func TestSanitizeRelPath(t *testing.T) {
	valid := []struct{ in, want string }{
		{"solution.py", "solution.py"},
		{"bank/account.py", "bank/account.py"},
		{"./x.py", "x.py"},
		{"a//b.py", "a/b.py"},
		{"a/./b.py", "a/b.py"},
	}
	for _, tc := range valid {
		got, err := SanitizeRelPath(tc.in)
		if err != nil {
			t.Errorf("SanitizeRelPath(%q) error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("SanitizeRelPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	invalid := []struct {
		in   string
		want error
	}{
		{"", ErrEmptyPath},
		{"/etc/passwd", ErrAbsolutePath},
		{`\windows\system32`, ErrAbsolutePath},
		{"C:autoexec.bat", ErrAbsolutePath},
		{"../x.py", ErrPathTraversal},
		{"a/../../x.py", ErrPathTraversal},
		{"..", ErrPathTraversal},
		{"a\x00b.py", ErrNullByte},
	}
	for _, tc := range invalid {
		if _, err := SanitizeRelPath(tc.in); !errors.Is(err, tc.want) {
			t.Errorf("SanitizeRelPath(%q) error = %v, want %v", tc.in, err, tc.want)
		}
	}
}

func TestValidateFilesRejectsDuplicate(t *testing.T) {
	_, err := ValidateFiles([]File{{Path: "a.py"}, {Path: "./a.py"}})
	if !errors.Is(err, ErrDuplicatePath) {
		t.Errorf("duplicate path error = %v, want ErrDuplicatePath", err)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := []File{{Path: "b.py", Content: "b"}, {Path: "a/x.py", Content: "x"}}
	blob, err := EncodeFiles(in)
	if err != nil {
		t.Fatalf("EncodeFiles: %v", err)
	}
	got, err := DecodeFiles(blob)
	if err != nil {
		t.Fatalf("DecodeFiles: %v", err)
	}
	// Canonical order: sorted by path.
	want := []File{{Path: "a/x.py", Content: "x"}, {Path: "b.py", Content: "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
	// Encoding is deterministic (same tree → same bytes).
	blob2, _ := EncodeFiles([]File{{Path: "a/x.py", Content: "x"}, {Path: "b.py", Content: "b"}})
	if string(blob) != string(blob2) {
		t.Error("EncodeFiles is not order-independent")
	}
}

func TestDecodeFilesRejectsTraversal(t *testing.T) {
	if _, err := DecodeFiles([]byte(`{"files":[{"path":"../evil","content":"x"}]}`)); !errors.Is(err, ErrPathTraversal) {
		t.Errorf("decoding a traversal path error = %v, want ErrPathTraversal", err)
	}
}

func TestIsTreeManifest(t *testing.T) {
	manifest, _ := EncodeFiles([]File{{Path: "a.py", Content: "x"}})
	if !IsTreeManifest(manifest) {
		t.Error("IsTreeManifest(manifest) = false, want true")
	}
	if IsTreeManifest([]byte("import os\nprint(1)\n")) {
		t.Error("IsTreeManifest(raw source) = true, want false")
	}
	if IsTreeManifest([]byte(`{"not":"a manifest"}`)) {
		t.Error("IsTreeManifest(JSON without files) = true, want false")
	}
}
