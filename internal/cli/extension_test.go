package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func extensionFixture(t *testing.T, kind string) string {
	t.Helper()
	t.Setenv("OSCM_EXTENSION_DIR", filepath.Join(t.TempDir(), "extensions"))
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "neovim" {
		if err := os.Mkdir(filepath.Join(root, "lua"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(root, "oscm-extension.json"), map[string]string{"schema": "oci-scm.extension.v1", "kind": kind}); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.WriteFile(filepath.Join(root, "oscm-fixture"), []byte("#!/bin/sh\nprintf '%s\\n' \"$1\"\ncat\nexit 7\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestExtensionPluginLifecycleWithoutOCIOrExecution(t *testing.T) {
	root := extensionFixture(t, "neovim")
	noCommands := func(context.Context, string, string, ...string) ([]byte, error) {
		t.Fatal("plugin installation must not execute commands or require OCI")
		return nil, nil
	}
	args := []string{"extension", "install", root, "--name", "review-mode"}
	out, err := invoke(t, noCommands, args...)
	if err != nil || !strings.Contains(out, `"apply": false`) {
		t.Fatalf("plan: %s %v", out, err)
	}
	dir, _ := extensionDir("review-mode")
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("plan created extension files")
	}
	if _, err = invoke(t, noCommands, append(args, "--apply")...); err != nil {
		t.Fatal(err)
	}
	out, err = invoke(t, noCommands, "extension", "path", "review-mode")
	if err != nil || strings.TrimSpace(out) != root {
		t.Fatalf("path: %s %v", out, err)
	}
	out, err = invoke(t, noCommands, "extension", "list", "--json", "all")
	if err != nil || !strings.Contains(out, `"kind": "neovim"`) {
		t.Fatalf("list: %s %v", out, err)
	}
	out, err = invoke(t, noCommands, "extension", "list", "--json", "name,path")
	if err != nil || !strings.Contains(out, `"name": "review-mode"`) || strings.Contains(out, `"kind"`) {
		t.Fatalf("selected extension fields: %s %v", out, err)
	}
	if _, err = invoke(t, noCommands, "extension", "exec", "review-mode"); err == nil {
		t.Fatal("Neovim plugin executed as a command")
	}
	if _, err = invoke(t, noCommands, "extension", "upgrade", "review-mode", "--apply"); err == nil {
		t.Fatal("local plugin upgraded")
	}
	if _, err = invoke(t, noCommands, "extension", "remove", "review-mode"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadExtension("review-mode"); err != nil {
		t.Fatal("remove plan removed extension")
	}
	if _, err = invoke(t, noCommands, "extension", "remove", "review-mode", "--apply"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "oscm-extension.json")); err != nil {
		t.Fatal("remove deleted local source")
	}
}

func TestExtensionExecPreservesArgumentsStreamsAndExitStatus(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX script fixture")
	}
	root := extensionFixture(t, "command")
	if _, err := invoke(t, execute, "extension", "install", root, "--name", "fixture", "--apply"); err != nil {
		t.Fatal(err)
	}
	a := newApp(execute)
	var stdout, stderr bytes.Buffer
	a.root.SetOut(&stdout)
	a.root.SetErr(&stderr)
	a.root.SetIn(strings.NewReader("stdin content\n"))
	a.root.SetArgs([]string{"extension", "exec", "fixture", "--", "--flag with spaces"})
	err := a.root.Execute()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || stdout.String() != "--flag with spaces\nstdin content\n" {
		t.Fatalf("exec: output %q error %v", stdout.String(), err)
	}
}

func TestExtensionRejectsUnsafePathsAndInvalidPackage(t *testing.T) {
	root := extensionFixture(t, "neovim")
	for _, extra := range [][]string{
		{"--name", "../outside"}, {"--name", "fixture", "--subdir", "../outside"}, {"--name", "fixture", "--pin", "--help"},
	} {
		args := append([]string{"extension", "install", root, "--apply"}, extra...)
		if _, err := invoke(t, execute, args...); err == nil {
			t.Fatalf("accepted %v", extra)
		}
	}
	if _, err := invoke(t, execute, "extension", "install", "https://user:secret@example.com/oscm-bad", "--apply"); err == nil {
		t.Fatal("embedded credentials accepted")
	}
	if _, err := invoke(t, execute, "extension", "install", t.TempDir(), "--name", "empty", "--apply"); err == nil {
		t.Fatal("invalid package installed")
	}
	dir, _ := extensionDir("empty")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("failed installation not cleaned up")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Skip(err)
	}
	if _, err := invoke(t, execute, "extension", "install", root, "--name", "escape", "--subdir", "escape", "--apply"); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestExtensionRepositoryNameDoesNotSelectExistingDirectory(t *testing.T) {
	t.Setenv("OSCM_EXTENSION_DIR", filepath.Join(t.TempDir(), "extensions"))
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "owner", "oscm-fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	noCommands := func(context.Context, string, string, ...string) ([]byte, error) {
		t.Fatal("install plan executed a command")
		return nil, nil
	}
	out, err := invoke(t, noCommands, "-C", dir, "extension", "install", "owner/oscm-fixture", "--pin", "v1")
	if err != nil || !strings.Contains(out, "https://github.com/owner/oscm-fixture.git") || !strings.Contains(out, `"local": false`) {
		t.Fatalf("remote repository shadowed by local directory: %s %v", out, err)
	}
	out, err = invoke(t, noCommands, "-C", dir, "extension", "install", "./owner/oscm-fixture")
	if err != nil || !strings.Contains(out, `"local": true`) {
		t.Fatalf("explicit local selection: %s %v", out, err)
	}
}

func TestExtensionRemotePinAndFastForwardUpgrade(t *testing.T) {
	extensionFixture(t, "neovim")
	repo := newGitRepo(t)
	if err := os.Mkdir(filepath.Join(repo, "lua"), 0755); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "lua/provider.lua", "return {}")
	if err := writeJSON(filepath.Join(repo, "oscm-extension.json"), map[string]string{"schema": "oci-scm.extension.v1", "kind": "neovim"}); err != nil {
		t.Fatal(err)
	}
	gitTest(t, repo, "add", ".")
	gitTest(t, repo, "commit", "-m", "plugin")
	gitTest(t, repo, "-c", "tag.gpgSign=false", "tag", "v1")
	clones := 0
	run := func(ctx context.Context, dir, bin string, args ...string) ([]byte, error) {
		if bin != "git" {
			t.Fatalf("unexpected command %s", bin)
		}
		if len(args) > 0 && args[0] == "clone" {
			clones++
			return execute(ctx, dir, "git", "clone", "--", repo, args[len(args)-1])
		}
		return execute(ctx, dir, bin, args...)
	}
	if _, err := invoke(t, run, "extension", "install", "owner/oscm-fixture"); err != nil {
		t.Fatal(err)
	}
	if clones != 0 {
		t.Fatal("plan cloned remote")
	}
	if _, err := invoke(t, run, "extension", "install", "owner/oscm-fixture", "--pin", "v1", "--apply"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, run, "extension", "upgrade", "fixture", "--apply"); err == nil {
		t.Fatal("pinned extension upgraded")
	}
	if _, err := invoke(t, run, "extension", "remove", "fixture", "--apply"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, run, "extension", "install", "owner/oscm-fixture", "--apply"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "lua/provider.lua", "return {updated=true}")
	if _, err := invoke(t, run, "extension", "upgrade", "fixture", "--apply"); err != nil {
		t.Fatal(err)
	}
	e, _, err := loadExtension("fixture")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(e.Path, "lua/provider.lua"))
	if !strings.Contains(string(data), "updated=true") {
		t.Fatal("upgrade did not reach new commit")
	}
	if err := os.WriteFile(filepath.Join(e.Path, "lua/provider.lua"), []byte("local changes"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, run, "extension", "upgrade", "fixture", "--apply"); err == nil {
		t.Fatal("dirty extension upgraded")
	}
	data, _ = os.ReadFile(filepath.Join(e.Path, "lua/provider.lua"))
	if string(data) != "local changes" {
		t.Fatal("upgrade overwrote local changes")
	}
}
