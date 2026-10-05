package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtendedOperationsPlanWithoutMutation(t *testing.T) {
	cleanEnv(t)
	mutations := 0
	run := func(_ context.Context, _ string, bin string, args ...string) ([]byte, error) {
		if bin == "git" {
			return nil, fmt.Errorf("outside Git")
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--from-json") {
			mutations++
			return nil, fmt.Errorf("unexpected mutation")
		}
		switch {
		case strings.Contains(joined, "repository get"):
			return []byte(`{"etag":"repo-etag","data":{"id":"ocid1.devopsrepository.test","repository-type":"FORKED"}}`), nil
		case strings.Contains(joined, "pull-request get"):
			return (&fakeOCI{}).run(context.Background(), "", bin, args...)
		case strings.Contains(joined, "get-pull-request-comment"):
			return []byte(`{"etag":"comment-etag","data":{"id":"comment"}}`), nil
		case strings.Contains(joined, "build-run get"):
			return []byte(`{"etag":"build-etag","data":{"build-pipeline-id":"pipeline","commit-info":{"commit-hash":"hash","repository-branch":"main","repository-url":"https://example.com/repository"}}}`), nil
		}
		return nil, fmt.Errorf("unexpected command %s", joined)
	}
	for _, args := range [][]string{{"repo", "edit", "--name", "new-name"}, {"repo", "delete"}, {"repo", "sync"}, {"repo", "fork", "parent", "--name", "fork", "--project-id", "project"}, {"pr", "comments", "edit", "ocid1.devopspullrequest.test", "comment", "--body", "edited"}, {"pr", "comments", "delete", "ocid1.devopspullrequest.test", "comment"}, {"workflow", "run", "pipeline", "-f", "environment=dev"}, {"run", "cancel", "run"}, {"run", "rerun", "run"}} {
		out, e := invoke(t, run, append(args, targetArgs()...)...)
		if e != nil {
			t.Fatalf("%v: %v", args, e)
		}
		if !strings.Contains(out, `"apply": false`) {
			t.Fatalf("missing plan: %s", out)
		}
	}
	if mutations != 0 {
		t.Fatal("planning mutated OCI")
	}
}

func TestBuildWatchSuccessAndFailure(t *testing.T) {
	cleanEnv(t)
	for _, terminal := range []string{"SUCCEEDED", "FAILED"} {
		calls := 0
		run := func(_ context.Context, _ string, bin string, args ...string) ([]byte, error) {
			if bin == "git" {
				return nil, fmt.Errorf("outside Git")
			}
			calls++
			state := "IN_PROGRESS"
			if calls > 1 {
				state = terminal
			}
			return jsonBytes(map[string]any{"data": map[string]any{"lifecycle-state": state}}), nil
		}
		_, e := invoke(t, run, append([]string{"run", "watch", "run", "--interval", "1ns", "--watch-timeout", "1s", "--exit-status"}, targetArgs()...)...)
		if (terminal == "FAILED") != (e != nil) || calls != 2 {
			t.Fatalf("watch %s: calls %d error %v", terminal, calls, e)
		}
	}
}

func TestIdentityDefaultsInheritEffectiveTarget(t *testing.T) {
	cleanEnv(t)
	run := func(ctx context.Context, _ string, bin string, args ...string) ([]byte, error) {
		if bin == "git" {
			return nil, fmt.Errorf("outside Git")
		}
		if bin != "fixture-idm" {
			return nil, fmt.Errorf("unexpected tool")
		}
		values, _ := ctx.Value(environmentKey{}).([]string)
		if !strings.Contains(strings.Join(values, " "), "OCI_CLI_PROFILE=test") || !strings.Contains(strings.Join(values, " "), "OCI_CLI_REGION=us-example-1") {
			t.Fatal("identity target not inherited")
		}
		return []byte(`{"issuer":"https://id.example.com"}`), nil
	}
	out, e := invoke(t, run, append([]string{"context", "--identity", "--idm-bin", "fixture-idm"}, targetArgs()...)...)
	if e != nil || !strings.Contains(out, "identityDefaults") {
		t.Fatalf("identity integration: %s %v", out, e)
	}
}

func TestHandoffFailureSavesPartialEvidence(t *testing.T) {
	cleanEnv(t)
	repo := newGitRepo(t)
	start := commitFile(t, repo, "base", "base")
	commitFile(t, repo, "fix", "fix")
	parent := t.TempDir()
	checks := filepath.Join(parent, "checks.json")
	if e := writeJSON(checks, []Check{{Name: "intentional failure", Argv: []string{"git", "not-a-command"}, Evidence: "ordinary"}}); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(parent, "handoff")
	if _, e := invoke(t, execute, append([]string{"handoff", "create", dir, "--since", start, "--source", "feature", "--checks-file", checks, "-C", repo}, targetArgs()...)...); e != nil {
		t.Fatal(e)
	}
	_, e := invoke(t, execute, append([]string{"handoff", "apply", dir, "--apply", "--run-checks", "-C", repo}, targetArgs()...)...)
	if e == nil {
		t.Fatal("failed check accepted")
	}
	paths, e := filepath.Glob(filepath.Join(dir, "runs", "*", "results.json"))
	if e != nil || len(paths) != 1 {
		t.Fatal("partial report missing")
	}
	b, e := os.ReadFile(paths[0])
	if e != nil || !strings.Contains(string(b), `"success": false`) || !strings.Contains(string(b), `"evidence": "ordinary"`) || !strings.Contains(string(b), "not-a-command") {
		t.Fatalf("incomplete report %s %v", b, e)
	}
}

func TestGitRemoteMustMatchOCIRepository(t *testing.T) {
	cleanEnv(t)
	run := func(_ context.Context, _ string, bin string, args ...string) ([]byte, error) {
		if bin == "git" {
			return []byte("git@example.com:wrong/repo.git\n"), nil
		}
		return []byte(`{"data":{"ssh-url":"ssh://scm.example.com/expected/repo"}}`), nil
	}
	a := newApp(run)
	a.dir = t.TempDir()
	a.timeout = 1e9
	c := a.root
	c.SetContext(context.Background())
	cfg := Config{Profile: "test", Region: "us-example-1", Auth: "api_key", Repository: "repository", Remote: "origin"}
	if e := a.validateRemote(c, cfg); e == nil {
		t.Fatal("wrong push remote accepted")
	}
	if canonicalRemote("git@example.com:team/repo.git") != canonicalRemote("ssh://git@example.com/team/repo") {
		t.Fatal("equivalent remote normalization failed")
	}
}

func TestCommentUnknownReadbackNeverRetries(t *testing.T) {
	cleanEnv(t)
	f := &fakeOCI{comments: []map[string]any{{"id": "parent", "data": "Review", "lifecycle-state": "ACTIVE"}}}
	lists := 0
	run := func(ctx context.Context, dir, bin string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "list-pull-request-comments") {
			lists++
			if lists > 1 {
				return nil, fmt.Errorf("temporary readback failure")
			}
		}
		return f.run(ctx, dir, bin, args...)
	}
	_, e := invoke(t, run, append([]string{"pr", "comment", "ocid1.devopspullrequest.test", "--parent", "parent", "--body", "Reply", "--apply"}, targetArgs()...)...)
	if e == nil || f.mutations != 1 || !strings.Contains(e.Error(), "inspect before retrying") {
		t.Fatalf("unsafe retry: mutations %d, %v", f.mutations, e)
	}
}

func TestAPINonSuccessIsAnError(t *testing.T) {
	cleanEnv(t)
	run := func(_ context.Context, _ string, bin string, _ ...string) ([]byte, error) {
		if bin == "git" {
			return nil, fmt.Errorf("outside Git")
		}
		return []byte(`{"status":"403 Forbidden","data":{"message":"denied"}}`), nil
	}
	_, e := invoke(t, run, append([]string{"api", "/20210630/repositories"}, targetArgs()...)...)
	if e == nil || !strings.Contains(e.Error(), "403") {
		t.Fatal("API error treated as success")
	}
}

func TestSeparatePushURLIsValidated(t *testing.T) {
	run := func(_ context.Context, _ string, bin string, args ...string) ([]byte, error) {
		if bin == "git" {
			if strings.Contains(strings.Join(args, " "), "--push") {
				return []byte("ssh://other.example.com/wrong"), nil
			}
			return []byte("ssh://scm.example.com/repository"), nil
		}
		return []byte(`{"data":{"ssh-url":"ssh://scm.example.com/repository"}}`), nil
	}
	a := newApp(run)
	a.timeout = 1e9
	a.root.SetContext(context.Background())
	cfg := Config{Profile: "test", Region: "us-example-1", Auth: "api_key", Remote: "origin", Repository: "repository"}
	if e := a.validateRemote(a.root, cfg); e != nil {
		t.Fatal(e)
	}
	if e := a.validateRemote(a.root, cfg, true); e == nil {
		t.Fatal("separate wrong push URL accepted")
	}
}
