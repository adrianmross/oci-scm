package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type Reply struct {
	Parent    string `json:"parent"`
	Body      string `json:"body"`
	FixCommit string `json:"fixCommit,omitempty"`
}
type Check struct {
	Name     string   `json:"name"`
	Argv     []string `json:"argv"`
	Evidence string   `json:"evidence"`
}
type Dependency struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type Manifest struct {
	Schema        string       `json:"schema"`
	Target        Config       `json:"target"`
	PullRequest   string       `json:"pullRequest,omitempty"`
	SourceBranch  string       `json:"sourceBranch"`
	BaseBranch    string       `json:"baseBranch"`
	ExpectedStart string       `json:"expectedStart"`
	Tip           string       `json:"tip"`
	Commits       []string     `json:"commits"`
	Bundle        string       `json:"bundle"`
	SHA256        string       `json:"sha256"`
	Description   string       `json:"description,omitempty"`
	Replies       []Reply      `json:"replies"`
	Checks        []Check      `json:"checks"`
	Dependencies  []Dependency `json:"dependencies"`
	PushHost      string       `json:"pushHost,omitempty"`
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

func confined(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("handoff file must be relative")
	}
	root, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	p, e := filepath.EvalSymlinks(filepath.Join(root, name))
	if e != nil {
		return "", e
	}
	rel, e := filepath.Rel(root, p)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("handoff path escapes directory")
	}
	return p, nil
}

func digest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifySums(dir string, required ...string) error {
	b, e := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 {
			return fmt.Errorf("invalid SHA256SUMS entry")
		}
		name := parts[1]
		if seen[name] {
			return fmt.Errorf("duplicate checksum entry")
		}
		seen[name] = true
		path, e := confined(dir, name)
		if e != nil {
			return e
		}
		hash, e := digest(path)
		if e != nil {
			return e
		}
		if hash != parts[0] {
			return fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	for _, name := range required {
		if name != "" && !seen[name] {
			return fmt.Errorf("missing checksum for %s", name)
		}
	}
	return nil
}

func (a *app) verifyHandoff(c *cobra.Command, dir string) (Manifest, string, error) {
	var m Manifest
	if e := readJSON(filepath.Join(dir, "manifest.json"), &m); e != nil {
		return m, "", e
	}
	if m.Schema != "oci-scm.handoff.v1" {
		return m, "", fmt.Errorf("unsupported handoff schema")
	}
	if e := verifySums(dir, "manifest.json", m.Bundle, m.Description); e != nil {
		return m, "", e
	}
	if e := require(m.Target.Repository, "manifest repository"); e != nil {
		return m, "", e
	}
	for _, hash := range append([]string{m.ExpectedStart, m.Tip}, m.Commits...) {
		if !commitPattern.MatchString(hash) {
			return m, "", fmt.Errorf("manifest contains invalid commit ID")
		}
	}
	if e := validBranch(c, a, m.SourceBranch); e != nil {
		return m, "", e
	}
	if e := validBranch(c, a, m.BaseBranch); e != nil {
		return m, "", e
	}
	for _, check := range m.Checks {
		if check.Name == "" || len(check.Argv) == 0 {
			return m, "", fmt.Errorf("check requires name and argv")
		}
		switch check.Evidence {
		case "ordinary", "oci-simulation":
		default:
			return m, "", fmt.Errorf("check evidence must be ordinary or oci-simulation")
		}
	}
	for _, reply := range m.Replies {
		if reply.Parent == "" || strings.TrimSpace(reply.Body) == "" {
			return m, "", fmt.Errorf("threaded replies require parent and body")
		}
		if reply.FixCommit != "" && !commitPattern.MatchString(reply.FixCommit) {
			return m, "", fmt.Errorf("invalid reply fix commit")
		}
	}
	bundle, e := confined(dir, m.Bundle)
	if e != nil {
		return m, "", e
	}
	hash, e := digest(bundle)
	if e != nil {
		return m, "", e
	}
	if hash != m.SHA256 {
		return m, "", fmt.Errorf("bundle checksum mismatch")
	}
	if m.Description != "" {
		if _, e = confined(dir, m.Description); e != nil {
			return m, "", e
		}
	}
	if _, e = a.exec(c, "git", "bundle", "verify", bundle); e != nil {
		return m, "", e
	}
	return m, bundle, nil
}

type HandoffPlan struct {
	Schema       string       `json:"schema"`
	LocalHead    string       `json:"localHead"`
	Tip          string       `json:"tip"`
	Missing      []string     `json:"missing"`
	Equivalent   []string     `json:"equivalent"`
	FastForward  bool         `json:"fastForward"`
	Checks       []Check      `json:"checks"`
	Replies      []Reply      `json:"replies"`
	Dependencies []Dependency `json:"dependencies"`
}

// Compare in a disposable object database: planning never updates caller refs.
func (a *app) planHandoff(c *cobra.Command, m Manifest, bundle string) (HandoffPlan, error) {
	p := HandoffPlan{Schema: "oci-scm.handoff-plan.v1", Tip: m.Tip, Missing: []string{}, Equivalent: []string{}, Checks: m.Checks, Replies: m.Replies, Dependencies: m.Dependencies}
	head, e := a.exec(c, "git", "rev-parse", "HEAD")
	if e != nil {
		return p, e
	}
	p.LocalHead = strings.TrimSpace(string(head))
	if _, e = a.exec(c, "git", "merge-base", "--is-ancestor", m.ExpectedStart, p.LocalHead); e != nil {
		return p, fmt.Errorf("expected starting commit is not an ancestor of local HEAD")
	}
	tmp, e := os.MkdirTemp("", "oscm-plan-*")
	if e != nil {
		return p, e
	}
	defer os.RemoveAll(tmp)
	scratch := *a
	scratch.dir = tmp
	if _, e = scratch.exec(c, "git", "init", "--bare"); e != nil {
		return p, e
	}
	local, e := filepath.Abs(a.dir)
	if e != nil {
		return p, e
	}
	if _, e = scratch.exec(c, "git", "fetch", "--no-tags", local, "HEAD:refs/oscm/current"); e != nil {
		return p, e
	}
	ref := "refs/oscm/incoming"
	if _, e = scratch.exec(c, "git", "fetch", "--no-tags", bundle, "refs/heads/"+m.SourceBranch+":"+ref); e != nil {
		return p, e
	}
	tip, e := scratch.exec(c, "git", "rev-parse", ref)
	if e != nil {
		return p, e
	}
	if strings.TrimSpace(string(tip)) != m.Tip {
		return p, fmt.Errorf("bundle tip differs from manifest")
	}
	commits, e := scratch.exec(c, "git", "rev-list", "--reverse", m.ExpectedStart+".."+ref)
	if e != nil {
		return p, e
	}
	merges, e := scratch.exec(c, "git", "rev-list", "--merges", m.ExpectedStart+".."+ref)
	if e != nil {
		return p, e
	}
	if len(strings.TrimSpace(string(merges))) > 0 {
		return p, fmt.Errorf("handoffs require a linear review-fix history; reconcile merge commits explicitly")
	}
	actual := strings.Fields(string(commits))
	if strings.Join(actual, ",") != strings.Join(m.Commits, ",") {
		return p, fmt.Errorf("bundle commit range differs from manifest")
	}
	for _, reply := range m.Replies {
		if reply.FixCommit != "" {
			found := false
			for _, commit := range m.Commits {
				if reply.FixCommit == commit {
					found = true
				}
			}
			if !found {
				return p, fmt.Errorf("reply fixCommit is outside the incoming range")
			}
		}
	}
	cherry, e := scratch.exec(c, "git", "cherry", "refs/oscm/current", ref, m.ExpectedStart)
	if e != nil {
		return p, e
	}
	missing := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(cherry)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			if fields[0] == "+" {
				missing[fields[1]] = true
			} else {
				p.Equivalent = append(p.Equivalent, fields[1])
			}
		}
	}
	for _, hash := range m.Commits {
		if missing[hash] {
			p.Missing = append(p.Missing, hash)
		}
	}
	_, e = scratch.exec(c, "git", "merge-base", "--is-ancestor", "refs/oscm/current", ref)
	p.FastForward = e == nil
	return p, nil
}

func (a *app) handoffCommands() *cobra.Command {
	r := &cobra.Command{Use: "handoff", Short: "Package and apply checksum-verified cross-machine PR updates"}
	var since, source, base, pr, description, pushHost, repliesFile, checksFile, dependenciesFile string
	create := &cobra.Command{Use: "create DIRECTORY", Args: cobra.ExactArgs(1), Short: "Export a branch bundle and structured handoff"}
	create.Flags().StringVar(&since, "since", "", "Existing reviewed/start commit (required)")
	create.Flags().StringVar(&source, "source", "", "Remote source branch name (required)")
	create.Flags().StringVar(&base, "base", "main", "PR destination branch")
	create.Flags().StringVar(&pr, "pr", "", "Existing PR OCID")
	create.Flags().StringVar(&description, "body-file", "", "Proposed PR description")
	create.Flags().StringVar(&pushHost, "push-host", "", "Only this hostname may push during apply")
	create.Flags().StringVar(&repliesFile, "replies-file", "", "JSON array of reviewer replies")
	create.Flags().StringVar(&checksFile, "checks-file", "", "JSON array of checks with argv and evidence kind")
	create.Flags().StringVar(&dependenciesFile, "dependencies-file", "", "JSON array of dependency statuses and remaining blockers")
	create.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		if e = require(cfg.Repository, "--repo"); e != nil {
			return e
		}
		if e = require(since, "--since"); e != nil {
			return e
		}
		if e = validBranch(c, a, source); e != nil {
			return e
		}
		if e = validBranch(c, a, base); e != nil {
			return e
		}
		start, e := a.exec(c, "git", "rev-parse", "--verify", "--end-of-options", since+"^{commit}")
		if e != nil {
			return e
		}
		since = strings.TrimSpace(string(start))
		if _, e = a.exec(c, "git", "merge-base", "--is-ancestor", since, "HEAD"); e != nil {
			return fmt.Errorf("--since must be an ancestor of HEAD")
		}
		merges, e := a.exec(c, "git", "rev-list", "--merges", since+"..HEAD")
		if e != nil {
			return e
		}
		if len(strings.TrimSpace(string(merges))) > 0 {
			return fmt.Errorf("handoffs require a linear review-fix history")
		}
		dirty, e := a.exec(c, "git", "status", "--porcelain")
		if e != nil {
			return e
		}
		if len(dirty) > 0 {
			return fmt.Errorf("commit changes before exporting; handoffs contain committed content only")
		}
		head, e := a.exec(c, "git", "rev-parse", "HEAD")
		if e != nil {
			return e
		}
		commits, e := a.exec(c, "git", "rev-list", "--reverse", since+"..HEAD")
		if e != nil {
			return e
		}
		m := Manifest{Schema: "oci-scm.handoff.v1", Target: cfg, PullRequest: pr, SourceBranch: source, BaseBranch: base, ExpectedStart: since, Tip: strings.TrimSpace(string(head)), Commits: strings.Fields(string(commits)), Bundle: "changes.bundle", Replies: []Reply{}, Checks: []Check{}, Dependencies: []Dependency{}, PushHost: pushHost}
		m.Target.ConfigFile = "" // machine-specific credential locations are not portable
		if repliesFile != "" {
			if e = readJSON(repliesFile, &m.Replies); e != nil {
				return e
			}
		}
		if checksFile != "" {
			if e = readJSON(checksFile, &m.Checks); e != nil {
				return e
			}
		}
		if dependenciesFile != "" {
			if e = readJSON(dependenciesFile, &m.Dependencies); e != nil {
				return e
			}
		}
		if e = os.Mkdir(args[0], 0700); e != nil {
			return e
		}
		dir, e := filepath.Abs(args[0])
		if e != nil {
			return e
		}
		if description != "" {
			b, e := os.ReadFile(description)
			if e != nil {
				return e
			}
			if e = os.WriteFile(filepath.Join(dir, "pr-description.md"), b, 0600); e != nil {
				return e
			}
			m.Description = "pr-description.md"
		}
		// A temporary ref gives the bundle the remote branch's name without renaming the worktree branch.
		tmp, e := os.MkdirTemp("", "oscm-export-*")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		scratch := *a
		scratch.dir = tmp
		if _, e = scratch.exec(c, "git", "init", "--bare"); e != nil {
			return e
		}
		local, e := filepath.Abs(a.dir)
		if e != nil {
			return e
		}
		if _, e = scratch.exec(c, "git", "fetch", "--no-tags", local, "HEAD:refs/heads/"+source); e != nil {
			return e
		}
		bundle := filepath.Join(dir, m.Bundle)
		bundleArgs := []string{"bundle", "create", bundle, "refs/heads/" + source}
		if len(m.Commits) > 0 {
			bundleArgs = append(bundleArgs, "^"+since)
		}
		if _, e = scratch.exec(c, "git", bundleArgs...); e != nil {
			return e
		}
		m.SHA256, e = digest(bundle)
		if e != nil {
			return e
		}
		if e = writeJSON(filepath.Join(dir, "manifest.json"), m); e != nil {
			return e
		}
		sums := []string{m.SHA256 + "  " + m.Bundle}
		for _, name := range []string{"manifest.json", m.Description} {
			if name != "" {
				hash, e := digest(filepath.Join(dir, name))
				if e != nil {
					return e
				}
				sums = append(sums, hash+"  "+name)
			}
		}
		if e = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(sums, "\n")+"\n"), 0600); e != nil {
			return e
		}
		return a.print(c, m)
	}
	r.AddCommand(create)
	for _, name := range []string{"verify", "plan"} {
		cmd := &cobra.Command{Use: name + " DIRECTORY", Args: cobra.ExactArgs(1), Short: "Verify handoff"}
		cmd.RunE = func(c *cobra.Command, args []string) error {
			m, bundle, e := a.verifyHandoff(c, args[0])
			if e != nil {
				return e
			}
			if name == "verify" {
				return a.print(c, map[string]any{"verified": true, "manifest": m})
			}
			p, e := a.planHandoff(c, m, bundle)
			if e != nil {
				return e
			}
			return a.print(c, p)
		}
		r.AddCommand(cmd)
	}
	var runChecks, push, updatePR bool
	apply := &cobra.Command{Use: "apply DIRECTORY", Args: cobra.ExactArgs(1), Short: "Apply missing commits; opt in separately to checks, push, and PR updates"}
	apply.Flags().BoolVar(&runChecks, "run-checks", false, "Execute reviewed manifest argv checks")
	apply.Flags().BoolVar(&push, "push", false, "Push only to manifest source branch")
	apply.Flags().BoolVar(&updatePR, "update-pr", false, "Update existing description and reviewer replies")
	apply.RunE = func(c *cobra.Command, args []string) error {
		return a.applyHandoff(c, args[0], runChecks, push, updatePR)
	}
	r.AddCommand(apply)
	return r
}

func (a *app) applyHandoff(c *cobra.Command, dir string, runChecks, push, updatePR bool) (err error) {
	m, bundle, err := a.verifyHandoff(c, dir)
	if err != nil {
		return err
	}
	plan, err := a.planHandoff(c, m, bundle)
	if err != nil {
		return err
	}
	if !a.apply {
		return a.print(c, map[string]any{"apply": false, "plan": plan, "runChecks": runChecks, "push": push, "updatePR": updatePR})
	}
	if (push || updatePR) && (!runChecks || len(m.Checks) == 0) {
		return fmt.Errorf("push/PR updates require explicit --run-checks and at least one recorded check")
	}
	if (push || updatePR) && len(plan.Equivalent) > 0 {
		return fmt.Errorf("equivalent cherry-picked commits require refreshed reviewer commit references before publication")
	}
	if push && m.PushHost != "" {
		host, e := os.Hostname()
		if e != nil {
			return e
		}
		if host != m.PushHost {
			return fmt.Errorf("push is restricted to host %s", m.PushHost)
		}
	}
	cfg, err := a.resolve(c)
	if err != nil {
		return err
	}
	if cfg.Repository != m.Target.Repository || cfg.Profile != m.Target.Profile || cfg.Region != m.Target.Region {
		return fmt.Errorf("effective repository/profile/region differs from handoff; select its target explicitly")
	}
	if push || updatePR {
		if err = a.validateRemote(c, cfg, push); err != nil {
			return err
		}
	}
	dirty, err := a.exec(c, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("worktree has local changes; preserve them and use an isolated WorkTrunk")
	}
	if !plan.FastForward && len(plan.Missing) > 0 && len(plan.Equivalent) == 0 {
		return fmt.Errorf("branches diverged; review a rebase/cherry-pick plan explicitly")
	}
	var before map[string]any
	if updatePR {
		if m.PullRequest == "" {
			return fmt.Errorf("handoff does not name an existing PR")
		}
		before, err = a.getPR(c, cfg, m.PullRequest)
		if err != nil {
			return err
		}
		pr := object(before)
		if str(pr, "source-branch") != m.SourceBranch || str(pr, "destination-branch") != m.BaseBranch || str(pr, "lifecycle-details") != "OPEN" {
			return fmt.Errorf("existing PR source/base/state differs from manifest")
		}
	}
	runDir := filepath.Join(dir, "runs", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err = os.MkdirAll(runDir, 0700); err != nil {
		return err
	}
	results := map[string]any{"schema": "oci-scm.handoff-results.v1", "version": version, "plan": plan, "target": cfg, "dependencies": m.Dependencies, "checks": []map[string]any{}, "startedAt": time.Now().UTC().Format(time.RFC3339)}
	commands := []map[string]any{}
	a.trace = &commands
	results["commands"] = &commands
	for key, bin := range map[string]string{"gitVersion": "git", "ociVersion": a.ociBin} {
		if b, e := a.exec(c, bin, "--version"); e == nil {
			results[key] = strings.TrimSpace(string(b))
		}
	}
	defer func() {
		a.trace = nil
		results["success"] = err == nil
		if err != nil {
			results["error"] = err.Error()
		}
		results["finishedAt"] = time.Now().UTC().Format(time.RFC3339)
		if e := writeJSON(filepath.Join(runDir, "results.json"), results); e != nil && err == nil {
			err = e
		}
		var report strings.Builder
		fmt.Fprintf(&report, "# OSCM handoff results\n\nSuccess: %t\n\nTool: oscm %s; Git: %v; OCI CLI: %v\n\nRepository: %s\nProfile: %s\nRegion: %s\nRemote: %s\nPR: %s\n\nStarting head: %s\nIncoming tip: %s\nTested/local head: %v\nVerified pushed head: %v\n", err == nil, version, results["gitVersion"], results["ociVersion"], cfg.Repository, cfg.Profile, cfg.Region, cfg.Remote, m.PullRequest, plan.LocalHead, m.Tip, results["localHead"], results["pushedHead"])
		fmt.Fprintln(&report, "\n## Checks\n\nOrdinary checks and OCI simulation are recorded separately. Skipped checks establish no success.")
		if entries, ok := results["checks"].([]map[string]any); ok && len(entries) > 0 {
			for _, entry := range entries {
				argv, _ := json.Marshal(entry["argv"])
				fmt.Fprintf(&report, "\n- %v (%v): passed=%v; argv=%s\n", entry["name"], entry["evidence"], entry["passed"], argv)
			}
		} else {
			fmt.Fprintln(&report, "\nNo checks executed.")
		}
		fmt.Fprintln(&report, "\n## Dependencies and remaining blockers")
		for _, dependency := range m.Dependencies {
			fmt.Fprintf(&report, "\n- %s: %s — %s\n", dependency.Name, dependency.Status, dependency.Detail)
		}
		if err != nil {
			fmt.Fprintf(&report, "\nStopped: %s\n", err)
		}
		commandsJSON, _ := json.MarshalIndent(commands, "", "  ")
		fmt.Fprintf(&report, "\n## Commands\n\n```json\n%s\n```\n\nPR readbacks, reply results and full machine-readable evidence: results.json and adjacent logs. No review approval or thread resolution is inferred.\n", commandsJSON)
		if e := os.WriteFile(filepath.Join(runDir, "RESULTS.md"), []byte(report.String()), 0600); e != nil && err == nil {
			err = e
		}
		fmt.Fprintf(c.ErrOrStderr(), "Evidence: %s\n", runDir)
	}()
	if before != nil {
		if err = writeJSON(filepath.Join(runDir, "pr-before.json"), before); err != nil {
			return err
		}
	}
	if len(plan.Missing) > 0 {
		ref := "refs/oscm/handoff/" + m.Tip
		if _, err = a.exec(c, "git", "fetch", "--no-tags", bundle, "refs/heads/"+m.SourceBranch+":"+ref); err != nil {
			return err
		}
		imported, e := a.exec(c, "git", "rev-parse", ref)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(imported)) != m.Tip {
			return fmt.Errorf("imported bundle tip differs from verified plan")
		}
		if plan.FastForward {
			_, err = a.exec(c, "git", "merge", "--ff-only", ref)
		} else {
			_, err = a.exec(c, "git", append([]string{"cherry-pick"}, plan.Missing...)...)
		}
		if err != nil {
			return fmt.Errorf("apply stopped; local conflict state preserved: %w", err)
		}
	}
	head, err := a.exec(c, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	results["localHead"] = strings.TrimSpace(string(head))
	checks := []map[string]any{}
	if runChecks {
		for i, check := range m.Checks {
			b, e := a.exec(c, check.Argv[0], check.Argv[1:]...)
			entry := map[string]any{"name": check.Name, "argv": check.Argv, "evidence": check.Evidence, "passed": e == nil}
			checks = append(checks, entry)
			results["checks"] = checks
			log := b
			if e != nil {
				log = append(log, []byte("\n"+e.Error())...)
			}
			if err = os.WriteFile(filepath.Join(runDir, fmt.Sprintf("check-%02d.log", i+1)), log, 0600); err != nil {
				return err
			}
			if e != nil {
				return fmt.Errorf("check %s failed: %w", check.Name, e)
			}
		}
	}
	if push {
		current, e := a.exec(c, "git", "rev-parse", "HEAD")
		if e != nil {
			return e
		}
		dirty, e := a.exec(c, "git", "status", "--porcelain")
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(current)) != results["localHead"] || len(dirty) > 0 {
			return fmt.Errorf("checks changed HEAD or left worktree changes; review before pushing")
		}
		if _, err = a.exec(c, "git", "push", cfg.Remote, "HEAD:refs/heads/"+m.SourceBranch); err != nil {
			return err
		}
		if err = a.verifyPush(c, cfg, m.SourceBranch, results["localHead"].(string)); err != nil {
			return err
		}
		results["pushedHead"] = results["localHead"]
	}
	if updatePR {
		if !push {
			remote, e := a.exec(c, "git", "ls-remote", "--heads", cfg.Remote, "refs/heads/"+m.SourceBranch)
			if e != nil {
				return e
			}
			fields := strings.Fields(string(remote))
			if len(fields) < 1 || fields[0] != results["localHead"] {
				return fmt.Errorf("remote head does not match tested local head; push explicitly first")
			}
		}
		before, err = a.getPR(c, cfg, m.PullRequest)
		if err != nil {
			return err
		}
		if pr := object(before); str(pr, "source-branch") != m.SourceBranch || str(pr, "destination-branch") != m.BaseBranch || str(pr, "lifecycle-details") != "OPEN" {
			return fmt.Errorf("PR changed during checks/push; review before updating")
		}
		if m.Description != "" {
			path, e := confined(dir, m.Description)
			if e != nil {
				return e
			}
			body, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			input := map[string]any{"pullRequestId": m.PullRequest, "description": string(body)}
			if tag := str(before, "etag"); tag != "" {
				input["ifMatch"] = tag
			}
			if _, err = a.mutate(c, cfg, []string{"devops", "pull-request", "update", "--force"}, input); err != nil {
				return err
			}
		}
		replies := []map[string]any{}
		for _, reply := range m.Replies {
			v, e := a.reply(c, cfg, m.PullRequest, reply.Parent, reply.Body)
			if e != nil {
				return e
			}
			replies = append(replies, v)
		}
		results["replies"] = replies
		after, e := a.getPR(c, cfg, m.PullRequest)
		if e != nil {
			return e
		}
		if m.Description != "" {
			path, e := confined(dir, m.Description)
			if e != nil {
				return e
			}
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if str(object(after), "description") != string(b) {
				return fmt.Errorf("PR description readback differs")
			}
		}
		if err = writeJSON(filepath.Join(runDir, "pr-after.json"), after); err != nil {
			return err
		}
	}
	return a.print(c, results)
}
