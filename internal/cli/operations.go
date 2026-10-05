package cli

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"net/url"
	"runtime"
	"strings"
	"time"
)

func (a *app) extraRepoCommands() []*cobra.Command {
	commands := []*cobra.Command{}
	for _, name := range []string{"edit", "delete", "sync"} {
		var description, branch, newName, source string
		var discard bool
		cmd := &cobra.Command{Use: name + " [OCID]", Args: cobra.MaximumNArgs(1), Short: "Plan repository " + name + "; execute only with --apply"}
		if name == "edit" {
			cmd.Flags().StringVar(&description, "description", "", "Description")
			cmd.Flags().StringVar(&branch, "default-branch", "", "Default branch")
			cmd.Flags().StringVar(&newName, "name", "", "New repository name")
		}
		if name == "sync" {
			cmd.Flags().StringVar(&source, "source", "main", "Upstream branch")
			cmd.Flags().StringVarP(&branch, "branch", "b", "main", "Destination branch")
			cmd.Flags().BoolVar(&discard, "discard", false, "Discard fork changes instead of merging upstream")
		}
		cmd.RunE = func(c *cobra.Command, args []string) error {
			cfg, e := a.resolve(c)
			if e != nil {
				return e
			}
			if len(args) > 0 {
				cfg.Repository = args[0]
			}
			if e = require(cfg.Repository, "repository OCID"); e != nil {
				return e
			}
			before, e := a.oci(c, cfg, "devops", "repository", "get", "--repository-id", cfg.Repository)
			if e != nil {
				return e
			}
			input := map[string]any{"repositoryId": cfg.Repository}
			if tag := str(before, "etag"); tag != "" {
				input["ifMatch"] = tag
			}
			path := []string{"devops", "repository", name}
			switch name {
			case "edit":
				path = []string{"devops", "repository", "update", "--force"}
				count := 0
				for _, pair := range [][2]string{{"description", description}, {"defaultBranch", branch}, {"name", newName}} {
					if pair[1] != "" {
						input[pair[0]] = pair[1]
						count++
					}
				}
				if count == 0 {
					return fmt.Errorf("provide description, default branch, or name")
				}
			case "delete":
				path = append(path, "--force")
			case "sync":
				if str(object(before), "repository-type") != "FORKED" {
					return fmt.Errorf("sync applies to forked repositories")
				}
				input["sourceBranch"] = source
				input["destinationBranch"] = branch
				input["syncMergeStrategy"] = "FETCH_AND_MERGE"
				if discard {
					input["syncMergeStrategy"] = "DISCARD"
				}
			}
			v, e := a.mutate(c, cfg, path, input)
			if e != nil {
				return e
			}
			if a.apply && name != "delete" {
				after, e := a.oci(c, cfg, "devops", "repository", "get", "--repository-id", cfg.Repository)
				if e != nil {
					return fmt.Errorf("%s submitted; readback failed: %w", name, e)
				}
				v = map[string]any{"submitted": true, "operation": v, "readback": after}
			}
			return a.print(c, v)
		}
		commands = append(commands, cmd)
	}
	var forkName string
	fork := &cobra.Command{Use: "fork OCID", Args: cobra.ExactArgs(1), Short: "Create a fork in the selected project"}
	fork.Flags().StringVar(&forkName, "name", "", "Fork name (required)")
	fork.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		if e = require(cfg.Project, "--project-id"); e != nil {
			return e
		}
		if e = require(forkName, "--name"); e != nil {
			return e
		}
		v, e := a.mutate(c, cfg, []string{"devops", "repository", "create"}, map[string]any{"name": forkName, "projectId": cfg.Project, "repositoryType": "FORKED", "parentRepositoryId": args[0]})
		if e != nil {
			return e
		}
		return a.print(c, v)
	}
	return append(commands, fork)
}

func (a *app) commentOperations() []*cobra.Command {
	commands := []*cobra.Command{}
	for _, name := range []string{"edit", "delete"} {
		var body, file string
		cmd := &cobra.Command{Use: name + " PR COMMENT_ID", Args: cobra.ExactArgs(2), Short: "Plan comment " + name + "; execute only with --apply"}
		if name == "edit" {
			cmd.Flags().StringVarP(&body, "body", "b", "", "Replacement comment")
			cmd.Flags().StringVarP(&file, "body-file", "F", "", "Replacement file or - for stdin")
		}
		cmd.RunE = func(c *cobra.Command, args []string) error {
			cfg, e := a.resolve(c)
			if e != nil {
				return e
			}
			pr, e := a.getPR(c, cfg, args[0])
			if e != nil {
				return e
			}
			id := str(object(pr), "id")
			before, e := a.oci(c, cfg, "devops", "pull-request-comment", "get-pull-request-comment", "--pull-request-id", id, "--comment-id", args[1])
			if e != nil {
				return e
			}
			input := map[string]any{"pullRequestId": id, "commentId": args[1]}
			if tag := str(before, "etag"); tag != "" {
				input["ifMatch"] = tag
			}
			path := []string{"devops", "pull-request-comment", "delete-pull-request-comment", "--force"}
			if name == "edit" {
				text, e := bodyText(c, body, file)
				if e != nil {
					return e
				}
				if strings.TrimSpace(text) == "" {
					return fmt.Errorf("replacement body required")
				}
				input["data"] = text
				path = []string{"devops", "pull-request-comment", "update-pull-request-comment"}
			}
			v, e := a.mutate(c, cfg, path, input)
			if e != nil {
				return e
			}
			if a.apply {
				after, e := a.comments(c, cfg, id)
				if e != nil {
					return fmt.Errorf("comment %s submitted; readback failed: %w", name, e)
				}
				found := false
				for _, item := range items(after) {
					if str(item, "id") == args[1] && str(item, "lifecycle-state") != "DELETED" {
						found = true
						if name == "edit" && str(item, "data") != input["data"] {
							return fmt.Errorf("comment edit readback differs")
						}
					}
				}
				if (name == "edit" && !found) || (name == "delete" && found) {
					return fmt.Errorf("comment %s submitted; readback not confirmed", name)
				}
				v = map[string]any{"verified": true, "operation": v}
			}
			return a.print(c, v)
		}
		commands = append(commands, cmd)
	}
	return commands
}

func (a *app) workflowCommands() *cobra.Command {
	r := &cobra.Command{Use: "workflow", Short: "Inspect and explicitly run OCI DevOps build pipelines"}
	r.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List build pipelines", RunE: func(c *cobra.Command, _ []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		args := []string{"devops", "build-pipeline", "list", "--all"}
		if cfg.Project != "" {
			args = append(args, "--project-id", cfg.Project)
		} else if cfg.Compartment != "" {
			args = append(args, "--compartment-id", cfg.Compartment)
		} else {
			return fmt.Errorf("project or compartment required")
		}
		v, e := a.oci(c, cfg, args...)
		if e != nil {
			return e
		}
		return a.print(c, items(v))
	}})
	r.AddCommand(&cobra.Command{Use: "view OCID", Args: cobra.ExactArgs(1), Short: "Read a build pipeline", RunE: func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		v, e := a.oci(c, cfg, "devops", "build-pipeline", "get", "--build-pipeline-id", args[0])
		if e != nil {
			return e
		}
		return a.print(c, v)
	}})
	var inputs []string
	run := &cobra.Command{Use: "run OCID", Args: cobra.ExactArgs(1), Short: "Plan a pipeline invocation; --apply may execute publication/deployment steps"}
	run.Flags().StringSliceVarP(&inputs, "field", "f", nil, "Build arguments as name=value")
	run.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		input := map[string]any{"buildPipelineId": args[0]}
		entries := []map[string]string{}
		for _, field := range inputs {
			key, value, ok := strings.Cut(field, "=")
			if !ok || key == "" {
				return fmt.Errorf("field must be name=value")
			}
			entries = append(entries, map[string]string{"name": key, "value": value})
		}
		if len(entries) > 0 {
			input["buildRunArguments"] = map[string]any{"items": entries}
		}
		v, e := a.mutate(c, cfg, []string{"devops", "build-run", "create"}, input)
		if e != nil {
			return e
		}
		return a.print(c, v)
	}
	r.AddCommand(run)
	return r
}

func (a *app) extraRunCommands() []*cobra.Command {
	var reason string
	cancel := &cobra.Command{Use: "cancel OCID", Args: cobra.ExactArgs(1), Short: "Plan cancellation; execute only with --apply"}
	cancel.Flags().StringVar(&reason, "reason", "Cancelled through oscm", "Cancellation reason")
	cancel.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		before, e := a.oci(c, cfg, "devops", "build-run", "get", "--build-run-id", args[0])
		if e != nil {
			return e
		}
		input := map[string]any{"buildRunId": args[0], "reason": reason}
		if tag := str(before, "etag"); tag != "" {
			input["ifMatch"] = tag
		}
		v, e := a.mutate(c, cfg, []string{"devops", "build-run", "cancel"}, input)
		if e != nil {
			return e
		}
		return a.print(c, v)
	}
	rerun := &cobra.Command{Use: "rerun OCID", Args: cobra.ExactArgs(1), Short: "Plan rerun using the original pipeline, arguments and commit"}
	rerun.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		before, e := a.oci(c, cfg, "devops", "build-run", "get", "--build-run-id", args[0])
		if e != nil {
			return e
		}
		run := object(before)
		pipeline := str(run, "build-pipeline-id")
		if e = require(pipeline, "original pipeline ID"); e != nil {
			return e
		}
		input := map[string]any{"buildPipelineId": pipeline}
		if arguments, ok := run["build-run-arguments"]; ok && arguments != nil {
			input["buildRunArguments"] = arguments
		}
		if commit, ok := run["commit-info"].(map[string]any); ok {
			input["commitInfo"] = map[string]any{"commitHash": commit["commit-hash"], "repositoryBranch": commit["repository-branch"], "repositoryUrl": commit["repository-url"]}
		}
		v, e := a.mutate(c, cfg, []string{"devops", "build-run", "create"}, input)
		if e != nil {
			return e
		}
		return a.print(c, v)
	}
	var interval, wait time.Duration
	var exitStatus bool
	watch := &cobra.Command{Use: "watch OCID", Args: cobra.ExactArgs(1), Short: "Watch an OCI build run until terminal state"}
	watch.Flags().DurationVar(&interval, "interval", 10*time.Second, "Poll interval")
	watch.Flags().DurationVar(&wait, "watch-timeout", 15*time.Minute, "Overall watch timeout")
	watch.Flags().BoolVar(&exitStatus, "exit-status", false, "Fail for failed/cancelled builds")
	watch.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		if interval <= 0 || wait <= 0 {
			return fmt.Errorf("interval and watch timeout must be positive")
		}
		ctx, stop := context.WithTimeout(c.Context(), wait)
		defer stop()
		c.SetContext(ctx)
		for {
			v, e := a.oci(c, cfg, "devops", "build-run", "get", "--build-run-id", args[0])
			if e != nil {
				return e
			}
			if e = a.print(c, v); e != nil {
				return e
			}
			state := str(object(v), "lifecycle-state")
			switch state {
			case "SUCCEEDED":
				return nil
			case "FAILED", "CANCELED", "DELETING":
				if exitStatus {
					return fmt.Errorf("build finished with %s", state)
				}
				return nil
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return []*cobra.Command{cancel, rerun, watch}
}

func (a *app) browseCommand() *cobra.Command {
	var urlOnly bool
	c := &cobra.Command{Use: "browse HTTPS_URL", Args: cobra.ExactArgs(1), Short: "Open a supplied OCI console/SCM URL"}
	c.Flags().BoolVar(&urlOnly, "url", false, "Print URL instead of opening it")
	c.RunE = func(c *cobra.Command, args []string) error {
		u, e := url.Parse(args[0])
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("HTTPS URL without credentials required")
		}
		if urlOnly {
			fmt.Fprintln(c.OutOrStdout(), u.String())
			return nil
		}
		bin := "xdg-open"
		argv := []string{u.String()}
		switch runtime.GOOS {
		case "darwin":
			bin = "open"
		case "windows":
			bin = "rundll32"
			argv = []string{"url.dll,FileProtocolHandler", u.String()}
		}
		_, e = a.exec(c, bin, argv...)
		return e
	}
	return c
}
