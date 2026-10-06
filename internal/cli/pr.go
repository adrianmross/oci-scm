package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func bodyText(c *cobra.Command, body, file string) (string, error) {
	if body != "" && file != "" {
		return "", fmt.Errorf("use either --body or --body-file")
	}
	if file == "-" {
		b, e := io.ReadAll(io.LimitReader(c.InOrStdin(), 1<<20))
		return string(b), e
	}
	if file != "" {
		b, e := os.ReadFile(file)
		return string(b), e
	}
	return body, nil
}

func (a *app) listPRs(c *cobra.Command, cfg Config, filters ...string) (map[string]any, error) {
	if err := require(cfg.Repository, "--repo or configured repository"); err != nil {
		return nil, err
	}
	args := []string{"devops", "pull-request", "list-pull-requests", "--repository-id", cfg.Repository, "--all"}
	return a.oci(c, cfg, append(args, filters...)...)
}

func (a *app) currentBranch(c *cobra.Command, cfg Config) (string, error) {
	if b, err := a.exec(c, "git", "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(string(b)), cfg.Remote+"/"), nil
	}
	b, err := a.exec(c, "git", "symbolic-ref", "--short", "HEAD")
	return strings.TrimSpace(string(b)), err
}

func (a *app) getPR(c *cobra.Command, cfg Config, selector string) (map[string]any, error) {
	if strings.Contains(selector, "://") {
		u, e := url.Parse(selector)
		if e != nil || u.Scheme != "https" {
			return nil, fmt.Errorf("invalid HTTPS pull request URL")
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		selector = parts[len(parts)-1]
		if !strings.HasPrefix(selector, "ocid1.devopspullrequest.") {
			return nil, fmt.Errorf("URL must end with a pull request OCID")
		}
	}
	if !strings.HasPrefix(selector, "ocid1.devopspullrequest.") {
		var err error
		if selector == "" {
			selector, err = a.currentBranch(c, cfg)
			if err != nil {
				return nil, err
			}
		}
		list, err := a.listPRs(c, cfg, "--source-branch", selector, "--lifecycle-details", "OPEN")
		if err != nil {
			return nil, err
		}
		prs := items(list)
		if len(prs) != 1 {
			return nil, fmt.Errorf("expected one open PR for branch %q, found %d; use its OCID", selector, len(prs))
		}
		selector = str(prs[0], "id")
	}
	v, err := a.oci(c, cfg, "devops", "pull-request", "get", "--pull-request-id", selector)
	if err == nil && cfg.Repository != "" && str(object(v), "repository-id") != cfg.Repository {
		return nil, fmt.Errorf("pull request belongs to a different repository")
	}
	return v, err
}

func (a *app) comments(c *cobra.Command, cfg Config, id string) (map[string]any, error) {
	return a.oci(c, cfg, "devops", "pull-request-comment", "list-pull-request-comments", "--pull-request-id", id, "--all")
}

func findReply(list map[string]any, parent, body string) map[string]any {
	return findComment(list, parent, body, nil)
}

func findComment(list map[string]any, parent, body string, location map[string]any) map[string]any {
	for _, item := range items(list) {
		if str(item, "parent-id") == parent && str(item, "data") == body && str(item, "lifecycle-state") != "DELETED" {
			matches := true
			for key, value := range location {
				field := map[string]string{"filePath": "file-path", "commitId": "commit-id", "fileType": "file-type", "lineNumber": "line-number"}[key]
				if fmt.Sprint(item[field]) != fmt.Sprint(value) {
					matches = false
				}
			}
			if matches && (parent != "" || location != nil || str(item, "file-path") == "") {
				return item
			}
		}
	}
	return nil
}

func (a *app) reply(c *cobra.Command, cfg Config, id, parent, body string) (map[string]any, error) {
	return a.postComment(c, cfg, id, parent, body, nil)
}

func (a *app) postComment(c *cobra.Command, cfg Config, id, parent, body string, location map[string]any) (map[string]any, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("comment body is required")
	}
	before, err := a.comments(c, cfg, id)
	if err != nil {
		return nil, err
	}
	if parent != "" {
		found := false
		for _, item := range items(before) {
			if str(item, "id") == parent && str(item, "lifecycle-state") != "DELETED" {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("parent comment %s not found", parent)
		}
	}
	if existing := findComment(before, parent, body, location); existing != nil {
		return map[string]any{"alreadyPosted": true, "data": existing}, nil
	}
	input := map[string]any{"pullRequestId": id, "data": body}
	for key, value := range location {
		input[key] = value
	}
	if parent != "" {
		input["parentId"] = parent
	}
	v, err := a.mutate(c, cfg, []string{"devops", "pull-request-comment", "create-pull-request-comment"}, input)
	if err != nil || !a.apply {
		return v, err
	}
	after, err := a.comments(c, cfg, id)
	if err != nil {
		return v, fmt.Errorf("comment submitted; readback failed: %w (inspect before retrying)", err)
	}
	if found := findComment(after, parent, body, location); found != nil {
		return map[string]any{"verified": true, "data": found}, nil
	}
	return v, fmt.Errorf("comment submitted; readback did not confirm it (inspect before retrying)")
}

func prState(state string) (string, error) {
	switch strings.ToLower(state) {
	case "open":
		return "OPEN", nil
	case "closed":
		return "CLOSED", nil
	case "merged":
		return "MERGED", nil
	case "all":
		return "", nil
	}
	return "", fmt.Errorf("state must be open, closed, merged, or all")
}

func (a *app) prCommands() *cobra.Command {
	root := &cobra.Command{Use: "pr", Short: "Manage OCI pull requests"}
	var state, head, base, author string
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List pull requests"}
	list.Flags().StringVarP(&state, "state", "s", "open", "open|closed|merged|all")
	list.Flags().StringVarP(&head, "head", "H", "", "Source branch")
	list.Flags().StringVarP(&base, "base", "B", "", "Destination branch")
	list.Flags().StringVar(&author, "author", "", "Author principal OCID")
	list.RunE = func(c *cobra.Command, _ []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		s, e := prState(state)
		if e != nil {
			return e
		}
		filters := []string{}
		for _, pair := range [][2]string{{"--lifecycle-details", s}, {"--source-branch", head}, {"--destination-branch", base}, {"--created-by", author}} {
			if pair[1] != "" {
				filters = append(filters, pair[:]...)
			}
		}
		v, e := a.listPRs(c, cfg, filters...)
		if e != nil {
			return e
		}
		return a.print(c, items(v))
	}
	root.AddCommand(list, a.prReadyCommand())
	for _, name := range []string{"view", "status", "comments", "checks", "diff"} {
		cmd := &cobra.Command{Use: name + " [OCID|URL|branch]", Args: cobra.MaximumNArgs(1), Short: map[string]string{"view": "View a pull request", "status": "View current branch PR status", "comments": "Read all comment threads", "checks": "Read OCI build-run snapshots", "diff": "Read source/base file differences"}[name]}
		cmd.RunE = func(c *cobra.Command, args []string) error {
			cfg, e := a.resolve(c)
			if e != nil {
				return e
			}
			selector := ""
			if len(args) > 0 {
				selector = args[0]
			}
			v, e := a.getPR(c, cfg, selector)
			if e != nil {
				return e
			}
			pr := object(v)
			switch name {
			case "view", "status":
				v, e = a.rawPR(c, cfg, str(pr, "id"))
			case "comments":
				v, e = a.comments(c, cfg, str(pr, "id"))
			case "checks":
				v, e = a.oci(c, cfg, "devops", "pull-request", "list-build-run-snapshots", "--pull-request-id", str(pr, "id"), "--all")
			case "diff":
				v, e = a.oci(c, cfg, "devops", "repository", "get-repo-file-diff", "--repository-id", str(pr, "repository-id"), "--base-version", str(pr, "destination-branch"), "--target-version", str(pr, "source-branch"), "--is-comparison-from-merge-base", "true")
			}
			if e != nil {
				return e
			}
			if name == "comments" || name == "checks" {
				return a.print(c, items(v))
			}
			return a.print(c, v)
		}
		if name == "comments" {
			cmd.AddCommand(a.commentOperations()...)
		}
		root.AddCommand(cmd)
	}
	for _, name := range []string{"create", "edit"} {
		var title, body, file, base, head string
		var reviewers []string
		var draft bool
		cmd := &cobra.Command{Use: name + " [OCID|URL|branch]", Args: cobra.MaximumNArgs(1), Short: map[string]string{"create": "Create a PR (plan unless --apply)", "edit": "Edit title/body/reviewers (plan unless --apply)"}[name]}
		cmd.Flags().StringVarP(&title, "title", "t", "", "Pull request title")
		cmd.Flags().StringVarP(&body, "body", "b", "", "Description")
		cmd.Flags().StringVarP(&file, "body-file", "F", "", "Description file, or - for stdin")
		cmd.Flags().StringVarP(&base, "base", "B", "", "Destination branch")
		cmd.Flags().StringSliceVarP(&reviewers, "reviewer", "r", nil, "Reviewer principal OCIDs")
		if name == "create" {
			cmd.Flags().BoolVarP(&draft, "draft", "d", false, "Create as a draft using the raw OCI API")
			cmd.Flags().StringVarP(&head, "head", "H", "", "Source branch (default current branch)")
		}
		cmd.RunE = func(c *cobra.Command, args []string) error {
			cfg, e := a.resolve(c)
			if e != nil {
				return e
			}
			text, e := bodyText(c, body, file)
			if e != nil {
				return e
			}
			input := map[string]any{}
			if title != "" {
				input["displayName"] = title
			}
			if c.Flags().Changed("body") || file != "" {
				input["description"] = text
			}
			if base != "" {
				input["destinationBranch"] = base
			}
			if len(reviewers) > 0 {
				entries := []map[string]string{}
				for _, id := range reviewers {
					entries = append(entries, map[string]string{"principalId": id})
				}
				input["reviewers"] = entries
			}
			var before map[string]any
			path := []string{"devops", "pull-request", name}
			if name == "create" {
				if len(args) > 0 {
					return fmt.Errorf("create takes flags, not a PR selector")
				}
				if e = require(title, "--title"); e != nil {
					return e
				}
				if e = require(cfg.Repository, "--repo"); e != nil {
					return e
				}
				if head == "" {
					head, e = a.currentBranch(c, cfg)
					if e != nil {
						return e
					}
				}
				input["sourceBranch"] = head
				input["repositoryId"] = cfg.Repository
				found, e := a.listPRs(c, cfg, "--source-branch", head, "--lifecycle-details", "OPEN")
				if e != nil {
					return e
				}
				for _, pr := range items(found) {
					if base == "" || str(pr, "destination-branch") == base {
						if draft {
							existing, err := a.rawPR(c, cfg, str(pr, "id"))
							if err != nil {
								return err
							}
							pr = object(existing)
							if str(pr, "review-status") != "DRAFT" {
								return fmt.Errorf("matching PR already exists and is not a draft; use pr ready --undo explicitly")
							}
						}
						return a.print(c, map[string]any{"alreadyExists": true, "data": pr})
					}
				}
			} else {
				if len(input) == 0 {
					return fmt.Errorf("provide title, body, base, or reviewers to edit")
				}
				selector := ""
				if len(args) > 0 {
					selector = args[0]
				}
				before, e = a.getPR(c, cfg, selector)
				if e != nil {
					return e
				}
				input["pullRequestId"] = str(object(before), "id")
				if tag := str(before, "etag"); tag != "" {
					input["ifMatch"] = tag
				}
				path = []string{"devops", "pull-request", "update", "--force"}
			}
			var v map[string]any
			if name == "create" && draft {
				input["reviewStatus"] = "DRAFT"
				v, e = a.rawPRRequest(c, cfg, "POST", "/20210630/pullRequests", input, "")
			} else {
				v, e = a.mutate(c, cfg, path, input)
			}
			if e != nil {
				return e
			}
			if a.apply && name == "create" {
				id := str(object(v), "id")
				if id == "" {
					found, e := a.listPRs(c, cfg, "--source-branch", head)
					if e != nil {
						return fmt.Errorf("create submitted; readback failed: %w", e)
					}
					matches := []map[string]any{}
					for _, candidate := range items(found) {
						if str(candidate, "display-name") == title && (base == "" || str(candidate, "destination-branch") == base) {
							matches = append(matches, candidate)
						}
					}
					if len(matches) != 1 {
						return fmt.Errorf("create submitted; readback not confirmed (inspect before retrying)")
					}
					id = str(matches[0], "id")
				}
				v, e = a.getPR(c, cfg, id)
				if e == nil && draft {
					v, e = a.rawPR(c, cfg, id)
				}
				if e != nil {
					return fmt.Errorf("create submitted; readback failed: %w", e)
				}
				if draft && str(object(v), "review-status") != "DRAFT" {
					return fmt.Errorf("create submitted; draft status not confirmed (inspect before retrying)")
				}
				if pr := object(v); str(pr, "source-branch") != head || str(pr, "display-name") != title || (base != "" && str(pr, "destination-branch") != base) {
					return fmt.Errorf("create submitted; readback differs")
				}
			}
			if a.apply && name == "edit" {
				v, e = a.getPR(c, cfg, input["pullRequestId"].(string))
				if e != nil {
					return fmt.Errorf("edit submitted, readback failed: %w", e)
				}
				for key, field := range map[string]string{"displayName": "display-name", "description": "description", "destinationBranch": "destination-branch"} {
					if expected, ok := input[key]; ok && object(v)[field] != expected {
						return fmt.Errorf("edit submitted, %s readback differs", field)
					}
				}
			}
			return a.print(c, v)
		}
		root.AddCommand(cmd)
	}
	var commentBody, commentFile, parent string
	var filePath, commitID, fileType string
	var lineNumber int
	comment := &cobra.Command{Use: "comment [OCID|URL|branch]", Aliases: []string{"reply"}, Args: cobra.MaximumNArgs(1), Short: "Post an idempotent comment/reply and verify readback"}
	comment.Flags().StringVarP(&commentBody, "body", "b", "", "Comment text")
	comment.Flags().StringVarP(&commentFile, "body-file", "F", "", "Text file, or - for stdin")
	comment.Flags().StringVar(&parent, "parent", "", "Original comment ID for a threaded reply")
	comment.Flags().StringVar(&filePath, "path", "", "Repository-relative file for an inline comment")
	comment.Flags().StringVar(&commitID, "commit", "", "Commit SHA for an inline comment")
	comment.Flags().StringVar(&fileType, "side", "SOURCE", "SOURCE or DESTINATION")
	comment.Flags().IntVar(&lineNumber, "line", 0, "Positive line number for an inline comment")
	comment.RunE = func(c *cobra.Command, args []string) error {
		var location map[string]any
		if filePath != "" || commitID != "" || lineNumber != 0 || c.Flags().Changed("side") {
			if parent != "" || filePath == "" || commitID == "" || lineNumber < 1 ||
				strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "\\") ||
				strings.Contains("/"+filePath+"/", "/../") || strings.Contains("/"+filePath+"/", "/./") ||
				(fileType != "SOURCE" && fileType != "DESTINATION") {
				return fmt.Errorf("inline comments require --path (repository-relative), --commit, --line > 0 and --side SOURCE|DESTINATION; replies use only --parent")
			}
			location = map[string]any{"filePath": filePath, "commitId": commitID, "fileType": fileType, "lineNumber": lineNumber}
		}
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		selector := ""
		if len(args) > 0 {
			selector = args[0]
		}
		pr, e := a.getPR(c, cfg, selector)
		if e != nil {
			return e
		}
		body, e := bodyText(c, commentBody, commentFile)
		if e != nil {
			return e
		}
		v, e := a.postComment(c, cfg, str(object(pr), "id"), parent, body, location)
		if e != nil {
			return e
		}
		return a.print(c, v)
	}
	root.AddCommand(comment)
	for _, name := range []string{"close", "reopen", "review", "merge"} {
		var approve, unapprove, merge, squash, rebase, deleteBranch bool
		var subject string
		cmd := &cobra.Command{Use: name + " [OCID|URL|branch]", Args: cobra.MaximumNArgs(1), Short: "Plan " + name + "; execute only with --apply"}
		if name == "review" {
			cmd.Flags().BoolVar(&approve, "approve", false, "Approve")
			cmd.Flags().BoolVar(&unapprove, "unapprove", false, "Withdraw approval")
		}
		if name == "merge" {
			cmd.Flags().BoolVar(&merge, "merge", false, "Merge commit")
			cmd.Flags().BoolVar(&squash, "squash", false, "Squash")
			cmd.Flags().BoolVar(&rebase, "rebase", false, "Rebase and merge")
			cmd.Flags().BoolVarP(&deleteBranch, "delete-branch", "d", false, "Delete source branch after merge")
			cmd.Flags().StringVarP(&subject, "subject", "t", "", "Merge commit message")
		}
		cmd.RunE = func(c *cobra.Command, args []string) error {
			cfg, e := a.resolve(c)
			if e != nil {
				return e
			}
			selector := ""
			if len(args) > 0 {
				selector = args[0]
			}
			before, e := a.getPR(c, cfg, selector)
			if e != nil {
				return e
			}
			pr := object(before)
			input := map[string]any{"pullRequestId": str(pr, "id")}
			if tag := str(before, "etag"); tag != "" {
				input["ifMatch"] = tag
			}
			action := name
			switch name {
			case "close":
				action = "decline"
			case "review":
				if approve == unapprove {
					return fmt.Errorf("choose --approve or --unapprove")
				}
				input["action"] = "UNAPPROVE"
				if approve {
					input["action"] = "APPROVE"
				}
			case "merge":
				count := 0
				for _, b := range []bool{merge, squash, rebase} {
					if b {
						count++
					}
				}
				if count != 1 {
					return fmt.Errorf("choose --merge, --squash, or --rebase")
				}
				action = "execute-merge-pull-request"
				input["commitMessage"] = first(subject, str(pr, "display-name"))
				input["mergeStrategy"] = "MERGE_COMMIT"
				if squash {
					input["mergeStrategy"] = "SQUASH"
				}
				if rebase {
					input["mergeStrategy"] = "REBASE_AND_MERGE"
				}
				input["postMergeAction"] = "KEEP_SOURCE_BRANCH"
				if deleteBranch {
					input["postMergeAction"] = "DELETE_SOURCE_BRANCH"
				}
			}
			v, e := a.mutate(c, cfg, []string{"devops", "pull-request", action}, input)
			if e != nil {
				return e
			}
			if a.apply {
				after, e := a.getPR(c, cfg, str(pr, "id"))
				if e != nil {
					return fmt.Errorf("%s submitted; readback failed: %w", name, e)
				}
				v = map[string]any{"submitted": true, "operation": v, "readback": after}
			}
			return a.print(c, v)
		}
		root.AddCommand(cmd)
	}
	checkout := &cobra.Command{Use: "checkout [OCID|URL|branch]", Args: cobra.MaximumNArgs(1), Short: "Check out a PR in an isolated WorkTrunk"}
	checkout.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		selector := ""
		if len(args) > 0 {
			selector = args[0]
		}
		v, e := a.getPR(c, cfg, selector)
		if e != nil {
			return e
		}
		pr := object(v)
		cfg.Repository = str(pr, "repository-id")
		localPrefix := "codex/"
		if source := str(pr, "source-repository-id"); source != "" && source != str(pr, "repository-id") {
			if a.opts.Remote == "" {
				return fmt.Errorf("fork checkout requires an explicitly configured fork remote via --remote")
			}
			cfg.Repository = source
			localPrefix += cfg.Remote + "/"
		}
		branch := str(pr, "source-branch")
		if e = validBranch(c, a, branch); e != nil {
			return e
		}
		if !a.apply {
			return a.print(c, map[string]any{"apply": false, "fetch": []string{cfg.Remote, branch}, "worktrunkBranch": localPrefix + branch, "sourceRepository": cfg.Repository})
		}
		if e = a.validateRemote(c, cfg); e != nil {
			return e
		}
		ref := "refs/remotes/" + cfg.Remote + "/" + branch
		if _, e = a.exec(c, "git", "fetch", cfg.Remote, "refs/heads/"+branch+":"+ref); e != nil {
			return e
		}
		local := localPrefix + branch
		wtArgs := []string{"switch", local, "--no-cd", "--yes"}
		if _, e = a.exec(c, "git", "show-ref", "--verify", "refs/heads/"+local); e != nil {
			wtArgs = append(wtArgs, "--create", "--base", ref)
		} else {
			head, e := a.exec(c, "git", "rev-parse", "refs/heads/"+local)
			if e != nil {
				return e
			}
			tip, e := a.exec(c, "git", "rev-parse", ref)
			if e != nil {
				return e
			}
			if string(head) != string(tip) {
				return fmt.Errorf("existing local branch differs; preserve it and reconcile explicitly")
			}
		}
		b, e := a.exec(c, "wt", wtArgs...)
		if e != nil {
			return e
		}
		fmt.Fprint(c.OutOrStdout(), string(b))
		return nil
	}
	root.AddCommand(checkout)
	return root
}

func validBranch(c *cobra.Command, a *app, branch string) error {
	if branch == "" || strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid branch")
	}
	_, e := a.exec(c, "git", "check-ref-format", "refs/heads/"+branch)
	return e
}
