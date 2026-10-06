package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIssueProvidersAreOptionalAndDoNotResolveOCI(t *testing.T) {
	cleanEnv(t)
	t.Setenv("OSCM_EXTENSION_DIR", t.TempDir())
	path := t.TempDir()
	path, _ = filepath.EvalSymlinks(path)
	executable := "issue-provider"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if err := os.WriteFile(filepath.Join(path, executable), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(path, "oscm-extension.json"), issueManifest{Schema: "oci-scm.extension.v1", Kind: "issue-provider", Executable: executable}); err != nil {
		t.Fatal(err)
	}
	f := &fakeOCI{}
	if _, err := invoke(t, f.run, "extension", "install", path, "--name", "tracker", "--apply"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	run := func(_ context.Context, _ string, bin string, args ...string) ([]byte, error) {
		if bin == "git" {
			return nil, fmt.Errorf("no repo")
		}
		if bin != filepath.Join(path, executable) {
			t.Fatalf("unexpected authentication/tool call %s", bin)
		}
		calls++
		var request map[string]any
		if err := json.Unmarshal([]byte(args[1]), &request); err != nil {
			t.Fatal(err)
		}
		if request["schema"] != "issue-provider.request.v1" || request["operation"] != "get" || request["offline"] != true {
			t.Fatalf("request %v", request)
		}
		return []byte(`{"schema":"issue-provider.response.v1","issue":{"key":"EX-1","title":"Cached issue"},"cache":{"source":"cache","stale":true}}`), nil
	}
	out, err := invoke(t, run, "issue", "view", "EX-1", "--provider", "tracker", "--offline", "--json", "title,key")
	if err != nil || calls != 1 || !strings.Contains(out, "Cached issue") {
		t.Fatalf("result %s %v", out, err)
	}
	if _, err = invoke(t, run, "issue", "view", "EX-1"); err == nil {
		t.Fatal("missing provider silently selected")
	}
	if _, err = invoke(t, run, "issue", "view", "EX-1", "--provider", "tracker", "--offline", "--refresh"); err == nil {
		t.Fatal("conflicting cache flags accepted")
	}
}

func TestIssueResponseValidation(t *testing.T) {
	for _, response := range []string{`{}`, `{"schema":"issue-provider.response.v1","issue":{}}`, `{"schema":"wrong","issue":{"key":"EX-1","title":"Title"}}`} {
		var value map[string]any
		_ = json.Unmarshal([]byte(response), &value)
		if validateIssueResponse(value, "get") == nil {
			t.Fatalf("accepted %s", response)
		}
	}
}
