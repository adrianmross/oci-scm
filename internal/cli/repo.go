package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func (a *app) repoCommands() *cobra.Command {
	r := &cobra.Command{Use: "repo", Short: "Discover, configure, and clone OCI repositories"}
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List repositories in a project/compartment", RunE: func(c *cobra.Command, _ []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		args := []string{"devops", "repository", "list", "--all"}
		if cfg.Project != "" {
			args = append(args, "--project-id", cfg.Project)
		} else if cfg.Compartment != "" {
			args = append(args, "--compartment-id", cfg.Compartment)
		} else {
			return fmt.Errorf("--project-id or --compartment-id required")
		}
		v, e := a.oci(c, cfg, args...)
		if e != nil {
			return e
		}
		return a.print(c, items(v))
	}}
	r.AddCommand(list)
	view := &cobra.Command{Use: "view [OCID]", Args: cobra.MaximumNArgs(1), Short: "View repository metadata", RunE: func(c *cobra.Command, args []string) error {
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
		v, e := a.oci(c, cfg, "devops", "repository", "get", "--repository-id", cfg.Repository)
		if e != nil {
			return e
		}
		return a.print(c, v)
	}}
	r.AddCommand(view)
	set := &cobra.Command{Use: "set-default [OCID]", Args: cobra.MaximumNArgs(1), Short: "Save credential-free .oci-scm.json in this worktree", RunE: func(c *cobra.Command, args []string) error {
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
		cfg.Schema = "oci-scm.repo.v1"
		root, e := a.exec(c, "git", "rev-parse", "--show-toplevel")
		if e != nil {
			return e
		}
		path := filepath.Join(strings.TrimSpace(string(root)), ".oci-scm.json")
		if !a.apply {
			return a.print(c, map[string]any{"apply": false, "path": path, "config": cfg})
		}
		if e = writeJSON(path, cfg); e != nil {
			return e
		}
		return a.print(c, map[string]any{"path": path, "config": cfg})
	}}
	r.AddCommand(set)
	var protocol string
	clone := &cobra.Command{Use: "clone OCID [directory]", Args: cobra.RangeArgs(1, 2), Short: "Clone using the repository's SSH or HTTPS URL"}
	clone.Flags().StringVar(&protocol, "protocol", "ssh", "ssh|https")
	clone.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		v, e := a.oci(c, cfg, "devops", "repository", "get", "--repository-id", args[0])
		if e != nil {
			return e
		}
		key := "ssh-url"
		if protocol == "https" {
			key = "http-url"
		} else if protocol != "ssh" {
			return fmt.Errorf("protocol must be ssh or https")
		}
		remote := str(object(v), key)
		if remote == "" {
			return fmt.Errorf("repository has no %s", key)
		}
		cmd := []string{"clone", "--", remote}
		if len(args) > 1 {
			cmd = append(cmd, args[1])
		}
		if !a.apply {
			return a.print(c, map[string]any{"apply": false, "git": cmd})
		}
		b, e := a.exec(c, "git", cmd...)
		if e != nil {
			return e
		}
		fmt.Fprint(c.OutOrStdout(), string(b))
		return nil
	}
	r.AddCommand(clone)
	var name, description, base, parent string
	create := &cobra.Command{Use: "create NAME", Args: cobra.ExactArgs(1), Short: "Create a hosted repository or fork (plan unless --apply)"}
	create.Flags().StringVar(&description, "description", "", "Repository description")
	create.Flags().StringVar(&base, "default-branch", "main", "Default branch")
	create.Flags().StringVar(&parent, "fork-of", "", "Parent repository OCID")
	create.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		if e = require(cfg.Project, "--project-id"); e != nil {
			return e
		}
		name = args[0]
		input := map[string]any{"name": name, "projectId": cfg.Project, "repositoryType": "HOSTED", "defaultBranch": base, "description": description}
		if parent != "" {
			input["repositoryType"] = "FORKED"
			input["parentRepositoryId"] = parent
		}
		v, e := a.mutate(c, cfg, []string{"devops", "repository", "create"}, input)
		if e != nil {
			return e
		}
		return a.print(c, v)
	}
	r.AddCommand(create)
	r.AddCommand(a.extraRepoCommands()...)
	return r
}

func (a *app) authCommands() *cobra.Command {
	r := &cobra.Command{Use: "auth", Short: "Use OCI CLI authentication; never store or print tokens"}
	r.AddCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, Short: "Validate the resolved session or repository access", RunE: func(c *cobra.Command, _ []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		if cfg.Auth == "security_token" {
			args := []string{"session", "validate", "--profile", cfg.Profile}
			if cfg.ConfigFile != "" {
				args = append([]string{"--config-file", cfg.ConfigFile}, args...)
			}
			b, e := a.exec(c, a.ociBin, args...)
			if e != nil {
				return e
			}
			return a.print(c, map[string]any{"target": cfg, "sessionValidated": true, "message": strings.TrimSpace(string(b))})
		}
		if cfg.Repository == "" {
			return fmt.Errorf("set --repo to verify access for %s authentication", cfg.Auth)
		}
		return a.doctor(c, nil)
	}})
	var tenancy, idp string
	login := &cobra.Command{Use: "login", Args: cobra.NoArgs, Short: "Authenticate a named OCI CLI browser session"}
	login.Flags().StringVar(&tenancy, "tenancy-name", "", "Tenancy name")
	login.Flags().StringVar(&idp, "identity-provider", "", "Identity provider name")
	login.RunE = func(c *cobra.Command, _ []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		if cfg.Profile == "" || cfg.Region == "" {
			return fmt.Errorf("explicit profile and region required")
		}
		args := []string{"session", "authenticate", "--profile-name", cfg.Profile, "--region", cfg.Region}
		if cfg.ConfigFile != "" {
			args = append(args, "--config-location", cfg.ConfigFile)
		}
		if tenancy != "" {
			args = append(args, "--tenancy-name", tenancy)
		}
		if idp != "" {
			args = append(args, "--identity-provider-name", idp)
		}
		if !a.apply {
			return a.print(c, map[string]any{"apply": false, "command": args})
		}
		_, e = a.exec(c, a.ociBin, args...)
		if e != nil {
			return fmt.Errorf("OCI browser login failed; run oci session authenticate directly for interactive diagnostics")
		}
		return a.print(c, map[string]any{"profile": cfg.Profile, "region": cfg.Region, "authenticated": true})
	}
	r.AddCommand(login)
	r.AddCommand(&cobra.Command{Use: "refresh", Args: cobra.NoArgs, Short: "Refresh a named OCI CLI session", RunE: func(c *cobra.Command, _ []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		if e = require(cfg.Profile, "profile"); e != nil {
			return e
		}
		args := []string{"session", "refresh", "--profile", cfg.Profile}
		if cfg.ConfigFile != "" {
			args = append([]string{"--config-file", cfg.ConfigFile}, args...)
		}
		if !a.apply {
			return a.print(c, map[string]any{"apply": false, "command": args})
		}
		_, e = a.exec(c, a.ociBin, args...)
		if e != nil {
			return e
		}
		return a.print(c, map[string]any{"profile": cfg.Profile, "refreshed": true})
	}})
	return r
}

func (a *app) runCommands() *cobra.Command {
	r := &cobra.Command{Use: "run", Short: "Inspect OCI DevOps build runs"}
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List build runs", RunE: func(c *cobra.Command, _ []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		args := []string{"devops", "build-run", "list", "--all"}
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
	}}
	r.AddCommand(list, &cobra.Command{Use: "view OCID", Args: cobra.ExactArgs(1), Short: "Read a build run", RunE: func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		v, e := a.oci(c, cfg, "devops", "build-run", "get", "--build-run-id", args[0])
		if e != nil {
			return e
		}
		return a.print(c, v)
	}})
	r.AddCommand(a.extraRunCommands()...)
	return r
}
