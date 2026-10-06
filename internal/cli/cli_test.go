package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func cleanEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"OCI_CLI_PROFILE", "OCI_CLI_REGION", "OCI_REGION", "OCI_CLI_AUTH", "OCI_CLI_CONFIG_FILE", "OCI_COMPARTMENT_OCID", "OSCM_CONTEXT", "OSCM_REPOSITORY", "OSCM_PROJECT"} {
		t.Setenv(name, "")
	}
}

func invoke(t *testing.T, run runner, args ...string) (string, error) {
	t.Helper()
	a := newApp(run)
	var out bytes.Buffer
	a.root.SetOut(&out)
	a.root.SetErr(&out)
	a.root.SetArgs(args)
	err := a.root.Execute()
	return out.String(), err
}

func jsonBytes(value any) []byte { b, _ := json.Marshal(value); return b }

func TestContextPrecedenceAndNamedSelection(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()
	if e := writeJSON(filepath.Join(dir, ".oci-scm.json"), Config{Schema: "oci-scm.repo.v1", Profile: "repo", Region: "us-example-1", Repository: "repository"}); e != nil {
		t.Fatal(e)
	}
	calls := []string{}
	run := func(_ context.Context, _ string, bin string, args ...string) ([]byte, error) {
		calls = append(calls, bin+" "+strings.Join(args, " "))
		switch {
		case bin == "git":
			return []byte(dir), nil
		case args[0] == "get":
			return []byte(`[{"name":"central","profile":"context","region":"us-context-1","auth_method":"security_token"}]`), nil
		case args[0] == "paths":
			return []byte(`{"oci_config_path":"/example/config"}`), nil
		}
		return nil, fmt.Errorf("unexpected command")
	}
	t.Setenv("OCI_CLI_PROFILE", "environment")
	out, e := invoke(t, run, "context", "--context", "central", "--context-bin", "fixture-context", "--profile", "explicit")
	if e != nil {
		t.Fatal(e)
	}
	var value struct {
		Target Config `json:"target"`
	}
	if e = json.Unmarshal([]byte(out), &value); e != nil {
		t.Fatal(e)
	}
	if value.Target.Profile != "explicit" || value.Target.Region != "us-context-1" || value.Target.Auth != "security_token" || value.Target.Context != "central" {
		t.Fatalf("wrong precedence: %+v", value.Target)
	}
	for _, call := range calls {
		if strings.Contains(call, " use ") || strings.Contains(call, "set-current") {
			t.Fatalf("mutated context: %s", call)
		}
	}
	_, e = invoke(t, run, "context", "--context", "missing", "--context-bin", "fixture-context")
	if e == nil || !strings.Contains(e.Error(), "not found") {
		t.Fatalf("missing named context: %v", e)
	}
}

type fakeOCI struct {
	comments  []map[string]any
	mutations int
	payload   map[string]any
	calls     [][]string
}

func (f *fakeOCI) run(_ context.Context, _ string, bin string, args ...string) ([]byte, error) {
	if bin == "git" {
		return nil, fmt.Errorf("outside Git")
	}
	if bin != "oci" {
		return nil, fmt.Errorf("unexpected tool %s", bin)
	}
	f.calls = append(f.calls, append([]string{}, args...))
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "raw-request") && strings.Contains(joined, "--http-method GET") {
		return jsonBytes(map[string]any{"status": "200 OK", "headers": map[string]any{"ETag": "etag-1"}, "data": map[string]any{"id": "ocid1.devopspullrequest.test", "repositoryId": "ocid1.devopsrepository.test", "sourceBranch": "feature", "destinationBranch": "main", "displayName": "Example", "reviewStatus": "READY", "lifecycleDetails": "OPEN"}}), nil
	}
	if strings.Contains(joined, "pull-request get") {
		return jsonBytes(map[string]any{"etag": "etag-1", "data": map[string]any{"id": "ocid1.devopspullrequest.test", "repository-id": "ocid1.devopsrepository.test", "source-branch": "feature", "destination-branch": "main", "display-name": "Example", "description": "Original", "lifecycle-details": "OPEN"}}), nil
	}
	if strings.Contains(joined, "list-pull-request-comments") {
		return jsonBytes(map[string]any{"data": map[string]any{"items": f.comments}}), nil
	}
	if strings.Contains(joined, "list-pull-requests") {
		return []byte(`{"data":{"items":[]}}`), nil
	}
	if strings.Contains(joined, "--from-json") {
		for i, arg := range args {
			if arg == "--from-json" {
				b, e := os.ReadFile(strings.TrimPrefix(args[i+1], "file://"))
				if e != nil {
					return nil, e
				}
				if e = json.Unmarshal(b, &f.payload); e != nil {
					return nil, e
				}
			}
		}
		f.mutations++
		if strings.Contains(joined, "create-pull-request-comment") {
			f.comments = append(f.comments, map[string]any{"id": "reply", "parent-id": f.payload["parentId"], "data": f.payload["data"], "lifecycle-state": "ACTIVE", "file-path": f.payload["filePath"], "commit-id": f.payload["commitId"], "file-type": f.payload["fileType"], "line-number": f.payload["lineNumber"]})
			return jsonBytes(map[string]any{"data": f.comments[len(f.comments)-1]}), nil
		}
		return []byte(`{"data":{}}`), nil
	}
	return nil, fmt.Errorf("unexpected OCI command %s", joined)
}

func targetArgs() []string {
	return []string{"--no-context", "--profile", "test", "--region", "us-example-1", "-R", "ocid1.devopsrepository.test"}
}

func TestPRPlansNeverMutate(t *testing.T) {
	cleanEnv(t)
	for _, args := range [][]string{{"pr", "create", "--title", "Title", "--head", "feature", "--base", "main"}, {"pr", "edit", "ocid1.devopspullrequest.test", "--body", "updated\nbody"}, {"pr", "close", "ocid1.devopspullrequest.test"}, {"pr", "merge", "ocid1.devopspullrequest.test", "--squash"}, {"pr", "review", "ocid1.devopspullrequest.test", "--approve"}} {
		f := &fakeOCI{}
		out, e := invoke(t, f.run, append(args, targetArgs()...)...)
		if e != nil {
			t.Fatal(e)
		}
		if f.mutations != 0 || !strings.Contains(out, `"apply": false`) {
			t.Fatalf("unexpected mutation/plan: %s", out)
		}
	}
}

func TestReplyReadbackAndIdempotency(t *testing.T) {
	cleanEnv(t)
	f := &fakeOCI{comments: []map[string]any{{"id": "parent", "data": "Review", "lifecycle-state": "ACTIVE"}}}
	args := append([]string{"pr", "comment", "ocid1.devopspullrequest.test", "--parent", "parent", "--body", "Fixed\nTested", "--apply"}, targetArgs()...)
	out, e := invoke(t, f.run, args...)
	if e != nil {
		t.Fatal(e)
	}
	if f.mutations != 1 || !strings.Contains(out, `"verified": true`) {
		t.Fatalf("readback failed: %s", out)
	}
	out, e = invoke(t, f.run, args...)
	if e != nil {
		t.Fatal(e)
	}
	if f.mutations != 1 || !strings.Contains(out, `"alreadyPosted": true`) {
		t.Fatalf("duplicate reply: %s", out)
	}
	if f.payload["data"] != "Fixed\nTested" {
		t.Fatal("newlines lost")
	}
	for _, call := range f.calls {
		if len(call) < 6 || call[0] != "--profile" || call[1] != "test" || call[2] != "--region" || call[3] != "us-example-1" {
			t.Fatal("ambient OCI target used")
		}
	}
}

func TestReplyRejectsMissingParent(t *testing.T) {
	cleanEnv(t)
	f := &fakeOCI{}
	args := append([]string{"pr", "comment", "ocid1.devopspullrequest.test", "--parent", "missing", "--body", "Reply", "--apply"}, targetArgs()...)
	_, e := invoke(t, f.run, args...)
	if e == nil || f.mutations != 0 {
		t.Fatal("posted to missing parent")
	}
}

func TestJSONSelectionAndUnknownFields(t *testing.T) {
	cleanEnv(t)
	f := &fakeOCI{}
	args := append([]string{"pr", "view", "ocid1.devopspullrequest.test", "--json", "id,title,headRefName"}, targetArgs()...)
	out, e := invoke(t, f.run, args...)
	if e != nil {
		t.Fatal(e)
	}
	var value map[string]any
	_ = json.Unmarshal([]byte(out), &value)
	if !reflect.DeepEqual(value, map[string]any{"id": "ocid1.devopspullrequest.test", "title": "Example", "headRefName": "feature"}) {
		t.Fatalf("bad projection: %s", out)
	}
	_, e = invoke(t, f.run, append([]string{"pr", "view", "ocid1.devopspullrequest.test", "--json", "unknown"}, targetArgs()...)...)
	if e == nil {
		t.Fatal("unknown JSON field accepted")
	}
}

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	b, e := execute(context.Background(), dir, "git", args...)
	if e != nil {
		t.Fatal(e)
	}
	return strings.TrimSpace(string(b))
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitTest(t, dir, "init", "-b", "main")
	gitTest(t, dir, "config", "user.name", "Example Developer")
	gitTest(t, dir, "config", "user.email", "developer@example.com")
	gitTest(t, dir, "config", "core.autocrlf", "false")
	return dir
}

func commitFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if e := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	gitTest(t, dir, "add", name)
	gitTest(t, dir, "commit", "-m", "Change "+name)
	return gitTest(t, dir, "rev-parse", "HEAD")
}

func TestHandoffRoundTripPreservesCheckoutAndSkipsAppliedCommits(t *testing.T) {
	cleanEnv(t)
	source := newGitRepo(t)
	start := commitFile(t, source, "first.txt", "first")
	gitTest(t, source, "branch", "-m", "codex/feature")
	tip := commitFile(t, source, "fix.txt", "fixed")
	dir := filepath.Join(t.TempDir(), "handoff")
	args := append([]string{"handoff", "create", dir, "--since", start, "--source", "feature", "--base", "main", "-C", source}, targetArgs()...)
	if _, e := invoke(t, execute, args...); e != nil {
		t.Fatal(e)
	}
	var m Manifest
	if e := readJSON(filepath.Join(dir, "manifest.json"), &m); e != nil {
		t.Fatal(e)
	}
	if m.Tip != tip || m.SourceBranch != "feature" {
		t.Fatal("incorrect handoff")
	}
	receiver := filepath.Join(t.TempDir(), "receiver")
	gitTest(t, filepath.Dir(receiver), "clone", source, receiver)
	gitTest(t, receiver, "checkout", "-b", "codex/review", start)
	refsBefore := gitTest(t, receiver, "show-ref")
	headBefore := gitTest(t, receiver, "rev-parse", "HEAD")
	args = append([]string{"handoff", "plan", dir, "-C", receiver}, targetArgs()...)
	out, e := invoke(t, execute, args...)
	if e != nil {
		t.Fatal(e)
	}
	var p HandoffPlan
	if e = json.Unmarshal([]byte(out), &p); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(p.Missing, []string{tip}) || !p.FastForward {
		t.Fatalf("unexpected plan %+v", p)
	}
	if gitTest(t, receiver, "show-ref") != refsBefore || gitTest(t, receiver, "rev-parse", "HEAD") != headBefore {
		t.Fatal("planning changed receiver")
	}
	args = append([]string{"handoff", "apply", dir, "--apply", "-C", receiver}, targetArgs()...)
	if _, e = invoke(t, execute, args...); e != nil {
		t.Fatal(e)
	}
	if gitTest(t, receiver, "rev-parse", "HEAD") != tip || gitTest(t, source, "rev-parse", "HEAD") != tip {
		t.Fatal("checkout not preserved")
	}
	if _, e = invoke(t, execute, args...); e != nil {
		t.Fatal(e)
	} // rerun applies no duplicate commit
	if count := gitTest(t, receiver, "rev-list", "--count", "HEAD"); count != "2" {
		t.Fatal("duplicate commits")
	}
	if e = os.WriteFile(filepath.Join(receiver, "dirty.txt"), []byte("local"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = invoke(t, execute, args...); e == nil {
		t.Fatal("dirty worktree accepted")
	}
	if b, e := os.ReadFile(filepath.Join(receiver, "dirty.txt")); e != nil || string(b) != "local" {
		t.Fatal("lost local changes")
	}
	if e = os.WriteFile(filepath.Join(dir, "changes.bundle"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e = invoke(t, execute, append([]string{"handoff", "verify", dir, "-C", receiver}, targetArgs()...)...)
	if e == nil || !strings.Contains(e.Error(), "checksum") {
		t.Fatalf("corruption accepted: %v", e)
	}
}

func TestHandoffDetectsEquivalentCherryPick(t *testing.T) {
	cleanEnv(t)
	source := newGitRepo(t)
	start := commitFile(t, source, "base", "base")
	tip := commitFile(t, source, "fix", "fix")
	dir := filepath.Join(t.TempDir(), "handoff")
	if _, e := invoke(t, execute, append([]string{"handoff", "create", dir, "--since", start, "--source", "feature", "-C", source}, targetArgs()...)...); e != nil {
		t.Fatal(e)
	}
	gitTest(t, source, "checkout", "-b", "codex/laptop", start)
	commitFile(t, source, "local", "local")
	gitTest(t, source, "cherry-pick", tip)
	out, e := invoke(t, execute, append([]string{"handoff", "plan", dir, "-C", source}, targetArgs()...)...)
	if e != nil {
		t.Fatal(e)
	}
	var p HandoffPlan
	_ = json.Unmarshal([]byte(out), &p)
	if len(p.Missing) != 0 || !reflect.DeepEqual(p.Equivalent, []string{tip}) {
		t.Fatalf("bad patch-equivalence plan: %s", out)
	}
}

func TestConfinedPathsAndChecksums(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	_ = os.WriteFile(outside, []byte("secret"), 0600)
	if _, e := confined(dir, outside); e == nil {
		t.Fatal("absolute path accepted")
	}
	if _, e := confined(dir, "../outside"); e == nil {
		t.Fatal("traversal accepted")
	}
	if e := os.Symlink(outside, filepath.Join(dir, "link")); e == nil {
		if _, e = confined(dir, "link"); e == nil {
			t.Fatal("symlink escape accepted")
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}"), 0600)
	hash, _ := digest(filepath.Join(dir, "manifest.json"))
	_ = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(hash+"  manifest.json\n"), 0600)
	if e := verifySums(dir, "manifest.json", "changes.bundle"); e == nil {
		t.Fatal("missing bundle checksum accepted")
	}
}

func TestAPIPlanningAndEndpointIsolation(t *testing.T) {
	cleanEnv(t)
	run := func(_ context.Context, _ string, _ string, _ ...string) ([]byte, error) {
		return nil, fmt.Errorf("must not call remote")
	}
	out, e := invoke(t, run, append([]string{"api", "/20210630/repositories", "-X", "POST"}, targetArgs()...)...)
	if e != nil || !strings.Contains(out, `"apply": false`) {
		t.Fatalf("mutation plan failed %s %v", out, e)
	}
	_, e = invoke(t, run, append([]string{"api", "https://example.com/steal"}, targetArgs()...)...)
	if e == nil {
		t.Fatal("external signing target accepted")
	}
}

func TestRootHelpAndCompletion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"pr", "--help"}, {"handoff", "--help"}, {"completion", "bash"}} {
		out, e := invoke(t, execute, args...)
		if e != nil || out == "" {
			t.Fatalf("help/completion failed: %v", e)
		}
	}
}
