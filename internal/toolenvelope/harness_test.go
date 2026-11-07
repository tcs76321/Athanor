package toolenvelope

import "testing"

func TestIsHarnessPath(t *testing.T) {
	harness := []string{
		"tests/test_main.py", "test/test_api.go", "spec/foo_spec.rb",
		"__tests__/x.test.js", "conftest.py", "pkg/conftest.py",
		"pytest.ini", "pyproject.toml", "requirements.txt", "Makefile",
		"package.json", "yarn.lock", "go.mod", "go.sum", "Cargo.toml",
		"pom.xml", "build.gradle.kts", "Gemfile.lock", "sitecustomize.py",
		"test_solution.py", "solution_test.py", "solution_test.go",
		"widget.test.ts", "widget.spec.jsx", "parser_test.rs",
	}
	for _, p := range harness {
		if !IsHarnessPath(p) {
			t.Errorf("IsHarnessPath(%q) = false, want true", p)
		}
	}

	source := []string{
		"solution.py", "src/main.go", "lib/parser.py", "app/index.ts",
		"README.md", "internal/widget/widget.go", "cmd/main.rs",
		"contest.py", "latest.py", "testimony.md",
	}
	for _, p := range source {
		if IsHarnessPath(p) {
			t.Errorf("IsHarnessPath(%q) = true, want false", p)
		}
	}
}

func TestSplitHarnessFilesPreservesOrder(t *testing.T) {
	files := []File{
		{Path: "solution.py", Content: "x"},
		{Path: "tests/test_solution.py", Content: "y"},
		{Path: "lib/helper.py", Content: "z"},
		{Path: "pytest.ini", Content: "w"},
	}
	impl, harness := SplitHarnessFiles(files)
	if len(impl) != 2 || impl[0].Path != "solution.py" || impl[1].Path != "lib/helper.py" {
		t.Fatalf("implementation = %+v", impl)
	}
	if len(harness) != 2 || harness[0].Path != "tests/test_solution.py" || harness[1].Path != "pytest.ini" {
		t.Fatalf("harness = %+v", harness)
	}
}
