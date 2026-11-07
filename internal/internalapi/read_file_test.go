package internalapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

func readFileRequest(path string) []byte {
	body, _ := json.Marshal(toolenvelope.ReadFileRequest{Tool: toolenvelope.ToolReadFile, Path: path})
	return body
}

func TestReadFileRejectsWithoutToken(t *testing.T) {
	env := newHandlerTestEnv(t)
	_, taskID := env.seedProject(t)
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/read_file", bytes.NewReader(readFileRequest("main.py")))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestReadFileRejectsWhenNotInEnvelope(t *testing.T) {
	env := newHandlerTestEnvWithExecutor(t, &fakePodExecutor{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/read_file", bytes.NewReader(readFileRequest("main.py")))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestReadFileAllowedReturnsBoundedContent(t *testing.T) {
	env := newHandlerTestEnvWithExecutor(t, &fakePodExecutor{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, string(toolenvelope.ToolReadFile)))

	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/read_file", bytes.NewReader(readFileRequest("main.py")))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var body toolenvelope.ReadFileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Path != "main.py" || body.Content == "" {
		t.Fatalf("body = %+v", body)
	}
}

func TestReadFileRejectsTraversal(t *testing.T) {
	env := newHandlerTestEnvWithExecutor(t, &fakePodExecutor{})
	_, taskID := env.seedProject(t)
	env.tokens.WithToken(taskID, goodToken)
	env.tools.WithAllow(taskID, mustParseTools(t, string(toolenvelope.ToolReadFile)))
	req := httptest.NewRequest("POST", "/internal/v1/jobs/"+taskID+"/read_file", bytes.NewReader(readFileRequest("../etc/passwd")))
	req.Header.Set("Authorization", "Bearer "+goodToken)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
