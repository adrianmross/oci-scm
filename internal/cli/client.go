package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var version = "dev"

type environmentKey struct{}

type runner func(context.Context, string, string, ...string) ([]byte, error)

func execute(ctx context.Context, dir, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	if overrides, ok := ctx.Value(environmentKey{}).([]string); ok {
		cmd.Env = append(os.Environ(), overrides...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", bin, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Config contains references and identifiers only. OCI CLI owns credentials.
type Config struct {
	Schema      string `json:"schema,omitempty"`
	Repository  string `json:"repository,omitempty"`
	Remote      string `json:"remote,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Region      string `json:"region,omitempty"`
	Auth        string `json:"auth,omitempty"`
	ConfigFile  string `json:"configFile,omitempty"`
	Compartment string `json:"compartment,omitempty"`
	Project     string `json:"project,omitempty"`
	Context     string `json:"context,omitempty"`
}

type app struct {
	opts                                                   Config
	dir, ociBin, contextBin, contextConfig, idmBin, fields string
	noContext, apply                                       bool
	timeout                                                time.Duration
	run                                                    runner
	root                                                   *cobra.Command
	trace                                                  *[]map[string]any
}

func New() *cobra.Command { return newApp(execute).root }

func newApp(run runner) *app {
	a := &app{run: run}
	r := &cobra.Command{Use: "oscm", Short: "OCI DevOps SCM, with gh-style commands and portable handoffs", SilenceUsage: true, SilenceErrors: true, Version: version}
	a.root = r
	f := r.PersistentFlags()
	f.StringVarP(&a.dir, "directory", "C", ".", "Local Git repository/worktree")
	f.StringVarP(&a.opts.Repository, "repo", "R", "", "OCI repository OCID")
	f.StringVar(&a.opts.Profile, "profile", "", "OCI CLI profile")
	f.StringVar(&a.opts.Region, "region", "", "OCI region")
	f.StringVar(&a.opts.Auth, "auth", "", "OCI CLI authentication method")
	f.StringVar(&a.opts.ConfigFile, "config-file", "", "OCI CLI configuration path")
	f.StringVar(&a.opts.Compartment, "compartment-id", "", "OCI compartment OCID")
	f.StringVar(&a.opts.Project, "project-id", "", "OCI DevOps project OCID")
	f.StringVar(&a.opts.Remote, "remote", "", "Git remote (default origin)")
	f.StringVar(&a.opts.Context, "context", "", "Named octx/oci-context context; never switches global context")
	f.StringVar(&a.contextBin, "context-bin", "", "octx/oci-context executable override")
	f.StringVar(&a.contextConfig, "context-config", "", "octx/oci-context configuration path")
	f.BoolVar(&a.noContext, "no-context", false, "Disable optional octx/oci-context integration")
	f.StringVar(&a.ociBin, "oci-bin", "oci", "OCI CLI executable")
	f.StringVar(&a.idmBin, "idm-bin", "", "oidm/oci-idm executable override")
	f.StringVar(&a.fields, "json", "", "JSON fields (comma-separated), or all")
	f.BoolVar(&a.apply, "apply", false, "Execute mutations; otherwise print a plan")
	f.DurationVar(&a.timeout, "timeout", 2*time.Minute, "Timeout per external command")
	r.AddCommand(a.contextCommand(), a.authCommands(), a.repoCommands(), a.prCommands(), a.runCommands(), a.handoffCommands(), a.apiCommand(), a.workflowCommands(), a.browseCommand(), a.extensionCommands())
	r.AddCommand(&cobra.Command{Use: "version", Args: cobra.NoArgs, Short: "Show version", RunE: func(c *cobra.Command, _ []string) error { return a.print(c, map[string]any{"version": version}) }})
	r.AddCommand(&cobra.Command{Use: "doctor", Args: cobra.NoArgs, Short: "Verify tooling, effective target, and repository access", RunE: a.doctor})
	return a
}

func (a *app) exec(c *cobra.Command, bin string, args ...string) ([]byte, error) {
	return a.execEnv(c, nil, bin, args...)
}

func (a *app) execEnv(c *cobra.Command, env []string, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(c.Context(), a.timeout)
	defer cancel()
	if len(env) > 0 {
		ctx = context.WithValue(ctx, environmentKey{}, env)
	}
	b, err := a.run(ctx, a.dir, bin, args...)
	if a.trace != nil {
		*a.trace = append(*a.trace, map[string]any{"directory": a.dir, "executable": bin, "argv": args, "success": err == nil})
	}
	return b, err
}

func readJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%s: trailing JSON", path)
	}
	return nil
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

func first(values ...string) string {
	for _, s := range values {
		if s != "" {
			return s
		}
	}
	return ""
}

func findBin(explicit string, names ...string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("install %s or supply an executable override", strings.Join(names, " or "))
}

func (a *app) resolve(c *cobra.Command) (Config, error) {
	cfg := Config{}
	if root, err := a.exec(c, "git", "rev-parse", "--show-toplevel"); err == nil {
		path := filepath.Join(strings.TrimSpace(string(root)), ".oci-scm.json")
		if _, err = os.Stat(path); err == nil {
			if err = readJSON(path, &cfg); err != nil {
				return cfg, err
			}
			if cfg.Schema != "oci-scm.repo.v1" {
				return cfg, fmt.Errorf("unsupported repository config schema")
			}
		}
	}
	contextName := first(a.opts.Context, os.Getenv("OSCM_CONTEXT"), cfg.Context)
	if a.noContext && contextName != "" {
		return cfg, fmt.Errorf("--no-context conflicts with a selected context")
	}
	defaults := Config{}
	if !a.noContext {
		bin, err := findBin(a.contextBin, "octx", "oci-context", "ocix")
		if err != nil && contextName != "" {
			return cfg, err
		}
		if err == nil {
			args := []string{"export", "-o", "json"}
			if contextName != "" {
				args = []string{"get", "-o", "json"}
			}
			if a.contextConfig != "" {
				args = append(args, "--config", a.contextConfig)
			}
			b, e := a.exec(c, bin, args...)
			type entry struct {
				Name        string `json:"name"`
				Profile     string `json:"profile"`
				Region      string `json:"region"`
				Auth        string `json:"auth_method"`
				Compartment string `json:"compartment_ocid"`
			}
			var selected entry
			if e == nil && contextName != "" {
				var entries []entry
				e = json.Unmarshal(b, &entries)
				for _, v := range entries {
					if v.Name == contextName {
						selected = v
						break
					}
				}
				if e == nil && selected.Name == "" {
					e = fmt.Errorf("context %q not found", contextName)
				}
			} else if e == nil {
				e = json.Unmarshal(b, &selected)
			}
			if e != nil {
				return cfg, fmt.Errorf("context integration: %w (use --no-context for explicit configuration)", e)
			}
			defaults = Config{Context: selected.Name, Profile: selected.Profile, Region: selected.Region, Auth: selected.Auth, Compartment: selected.Compartment}
			args = []string{"paths", "-o", "json"}
			if a.contextConfig != "" {
				args = append(args, "--config", a.contextConfig)
			}
			b, e = a.exec(c, bin, args...)
			if e != nil {
				return cfg, e
			}
			var paths struct {
				ConfigFile string `json:"oci_config_path"`
			}
			if e = json.Unmarshal(b, &paths); e != nil {
				return cfg, e
			}
			defaults.ConfigFile = paths.ConfigFile
		}
	}
	if a.opts.Context != "" || os.Getenv("OSCM_CONTEXT") != "" {
		cfg.Profile = defaults.Profile
		cfg.Region = defaults.Region
		cfg.Auth = defaults.Auth
		cfg.Compartment = defaults.Compartment
		cfg.ConfigFile = defaults.ConfigFile
	}
	cfg.Profile = first(a.opts.Profile, os.Getenv("OCI_CLI_PROFILE"), cfg.Profile, defaults.Profile)
	cfg.Region = first(a.opts.Region, os.Getenv("OCI_CLI_REGION"), os.Getenv("OCI_REGION"), cfg.Region, defaults.Region)
	cfg.Auth = first(a.opts.Auth, os.Getenv("OCI_CLI_AUTH"), cfg.Auth, defaults.Auth, "api_key")
	cfg.ConfigFile = first(a.opts.ConfigFile, os.Getenv("OCI_CLI_CONFIG_FILE"), cfg.ConfigFile, defaults.ConfigFile)
	cfg.Compartment = first(a.opts.Compartment, os.Getenv("OCI_COMPARTMENT_OCID"), cfg.Compartment, defaults.Compartment)
	cfg.Project = first(a.opts.Project, os.Getenv("OSCM_PROJECT"), cfg.Project)
	cfg.Repository = first(a.opts.Repository, os.Getenv("OSCM_REPOSITORY"), cfg.Repository)
	cfg.Remote = first(a.opts.Remote, cfg.Remote, "origin")
	cfg.Context = first(contextName, defaults.Context)
	return cfg, nil
}

func (a *app) oci(c *cobra.Command, cfg Config, args ...string) (map[string]any, error) {
	if cfg.Profile == "" || cfg.Region == "" {
		return nil, fmt.Errorf("profile and region are required; use explicit flags, repo config, or octx")
	}
	global := []string{"--profile", cfg.Profile, "--region", cfg.Region, "--auth", cfg.Auth, "--output", "json"}
	if cfg.ConfigFile != "" {
		global = append(global, "--config-file", cfg.ConfigFile)
	}
	b, err := a.exec(c, a.ociBin, append(global, args...)...)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err = json.Unmarshal(b, &value); err != nil {
		return nil, fmt.Errorf("OCI response is not JSON: %w", err)
	}
	return value, nil
}

func (a *app) mutate(c *cobra.Command, cfg Config, path []string, body map[string]any) (map[string]any, error) {
	if !a.apply {
		return map[string]any{"apply": false, "target": cfg, "command": path, "input": body}, nil
	}
	f, err := os.CreateTemp("", "oscm-input-*.json")
	if err != nil {
		return nil, err
	}
	p := f.Name()
	defer os.Remove(p)
	if err = json.NewEncoder(f).Encode(body); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	return a.oci(c, cfg, append(path, "--from-json", "file://"+p)...)
}

func object(response map[string]any) map[string]any {
	v, _ := response["data"].(map[string]any)
	return v
}
func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func items(response map[string]any) []map[string]any {
	v := response["data"]
	if m, ok := v.(map[string]any); ok {
		v = m["items"]
	}
	list, _ := v.([]any)
	out := []map[string]any{}
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

var aliases = map[string]string{"reviewStatus": "review-status", "title": "display-name", "body": "description", "state": "lifecycle-details", "headRefName": "source-branch", "baseRefName": "destination-branch", "createdAt": "time-created", "updatedAt": "time-updated"}

func (a *app) print(c *cobra.Command, value any) error {
	if a.fields != "" && a.fields != "all" {
		selectFields := func(m map[string]any) (map[string]any, error) {
			out := map[string]any{}
			for _, key := range strings.Split(a.fields, ",") {
				key = strings.TrimSpace(key)
				source := first(aliases[key], key)
				v, ok := m[source]
				if !ok {
					return nil, fmt.Errorf("unknown JSON field %q", key)
				}
				out[key] = v
			}
			return out, nil
		}
		if m, ok := value.(map[string]any); ok {
			if data := object(m); data != nil {
				m = data
			}
			var err error
			value, err = selectFields(m)
			if err != nil {
				return err
			}
		}
		if list, ok := value.([]map[string]any); ok {
			out := []map[string]any{}
			for _, m := range list {
				v, err := selectFields(m)
				if err != nil {
					return err
				}
				out = append(out, v)
			}
			value = out
		}
	}
	// Full JSON is the stable automation format; human lists remain compact.
	if a.fields == "" {
		if list, ok := value.([]map[string]any); ok {
			for _, m := range list {
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\n", str(m, "id"), first(str(m, "lifecycle-details"), str(m, "lifecycle-state")), first(str(m, "display-name"), str(m, "name"), str(m, "data")))
			}
			return nil
		}
	}
	e := json.NewEncoder(c.OutOrStdout())
	e.SetIndent("", "  ")
	return e.Encode(value)
}

func require(value, label string) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	return nil
}

func (a *app) contextCommand() *cobra.Command {
	c := &cobra.Command{Use: "context", Short: "Show effective OCI target and optional identity defaults", Args: cobra.NoArgs}
	var identity bool
	c.Flags().BoolVar(&identity, "identity", false, "Include optional oidm/oci-idm metadata (never tokens)")
	c.RunE = func(c *cobra.Command, _ []string) error {
		cfg, err := a.resolve(c)
		if err != nil {
			return err
		}
		out := map[string]any{"schema": "oci-scm.context.v1", "target": cfg, "precedence": []string{"individual flags", "environment", "explicit named context", "repo config", "current octx context"}}
		if identity {
			bin, e := findBin(a.idmBin, "oidm", "oci-idm", "ocidm")
			if e != nil {
				return e
			}
			args := []string{"defaults", "--output", "json"}
			if a.contextBin != "" {
				args = append(args, "--oci-context-bin", a.contextBin)
			}
			env := []string{"OCI_CLI_PROFILE=" + cfg.Profile, "OCI_CLI_REGION=" + cfg.Region, "OCI_CLI_AUTH=" + cfg.Auth}
			if cfg.ConfigFile != "" {
				env = append(env, "OCI_CLI_CONFIG_FILE="+cfg.ConfigFile)
			}
			b, e := a.execEnv(c, env, bin, args...)
			if e != nil {
				return e
			}
			var metadata any
			if e = json.Unmarshal(b, &metadata); e != nil {
				return e
			}
			out["identityDefaults"] = metadata
			out["identityNotice"] = "Identity profile/region inherit this target. Service/issuer defaults resolve independently and remain advisory; they never authenticate SCM."
		}
		return a.print(c, out)
	}
	return c
}

func (a *app) doctor(c *cobra.Command, _ []string) error {
	cfg, err := a.resolve(c)
	if err != nil {
		return err
	}
	git, e := a.exec(c, "git", "--version")
	if e != nil {
		return e
	}
	oci, e := a.exec(c, a.ociBin, "--version")
	if e != nil {
		return e
	}
	out := map[string]any{"target": cfg, "git": strings.TrimSpace(string(git)), "oci": strings.TrimSpace(string(oci))}
	if cfg.Repository != "" {
		v, e := a.oci(c, cfg, "devops", "repository", "get", "--repository-id", cfg.Repository)
		if e != nil {
			return e
		}
		out["repository"] = object(v)
	} else {
		out["accessVerified"] = false
	}
	return a.print(c, out)
}
