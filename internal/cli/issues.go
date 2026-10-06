package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type IssueConfig struct {
	Provider string         `json:"provider"`
	Options  map[string]any `json:"options,omitempty"`
}

type issueManifest struct {
	Schema     string `json:"schema"`
	Kind       string `json:"kind"`
	Executable string `json:"executable,omitempty"`
}

func issueExecutable(e extension) (string, error) {
	var manifest issueManifest
	if err := readJSON(filepath.Join(e.Path, "oscm-extension.json"), &manifest); err != nil {
		return "", err
	}
	if manifest.Kind != "issue-provider" || manifest.Schema != "oci-scm.extension.v1" || manifest.Executable == "" {
		return "", fmt.Errorf("not an issue-provider extension")
	}
	path, err := extensionPath(e.Path, manifest.Executable)
	if err != nil {
		return "", err
	}
	return exec.LookPath(path)
}

func validateIssueResponse(v map[string]any, operation string) error {
	if str(v, "schema") != "issue-provider.response.v1" {
		return fmt.Errorf("unsupported issue provider response schema")
	}
	validate := func(value any) bool {
		m, ok := value.(map[string]any)
		return ok && str(m, "key") != "" && str(m, "title") != ""
	}
	if operation == "get" {
		if !validate(v["issue"]) {
			return fmt.Errorf("issue provider returned an invalid issue")
		}
	} else {
		list, ok := v["items"].([]any)
		if !ok {
			return fmt.Errorf("issue provider returned an invalid search result")
		}
		for _, item := range list {
			if !validate(item) {
				return fmt.Errorf("issue provider returned an invalid issue")
			}
		}
	}
	return nil
}

func (a *app) issueCommands() *cobra.Command {
	root := &cobra.Command{Use: "issue", Short: "Read issues through optional installed providers (no OCI authentication required)"}
	var provider string
	root.PersistentFlags().StringVar(&provider, "provider", "", "Installed issue-provider extension (overrides repo settings)")
	for _, operation := range []string{"view", "search"} {
		var offline, refresh bool
		c := &cobra.Command{Use: operation + " KEY_OR_QUERY", Args: cobra.ExactArgs(1), Short: "Read issue provider " + operation}
		c.Flags().BoolVar(&offline, "offline", false, "Read cached data only")
		c.Flags().BoolVar(&refresh, "refresh", false, "Require a fresh remote read")
		c.MarkFlagsMutuallyExclusive("offline", "refresh")
		c.RunE = func(c *cobra.Command, args []string) error {
			selected := IssueConfig{}
			if b, err := a.exec(c, "git", "rev-parse", "--show-toplevel"); err == nil {
				path := filepath.Join(strings.TrimSpace(string(b)), ".oci-scm.json")
				if _, err = os.Stat(path); err == nil {
					var cfg Config
					if err = readJSON(path, &cfg); err != nil {
						return err
					}
					if cfg.Schema != "oci-scm.repo.v1" {
						return fmt.Errorf("unsupported repository config schema")
					}
					if cfg.Issues != nil {
						selected = *cfg.Issues
					}
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			if provider != "" && provider != selected.Provider {
				selected.Options = nil
			}
			selected.Provider = first(provider, selected.Provider)
			if selected.Provider == "" {
				return fmt.Errorf("select an installed issue provider with --provider or repo issues.provider")
			}
			e, _, err := loadExtension(selected.Provider)
			if err != nil {
				return err
			}
			if e.Kind != "issue-provider" {
				return fmt.Errorf("extension %s is not an issue provider", e.Name)
			}
			bin, err := issueExecutable(e)
			if err != nil {
				return err
			}
			op := "get"
			if operation == "search" {
				op = "search"
			}
			request := map[string]any{"schema": "issue-provider.request.v1", "operation": op, "options": selected.Options, "offline": offline, "refresh": refresh}
			if op == "get" {
				request["key"] = args[0]
			} else {
				request["query"] = args[0]
			}
			b, err := json.Marshal(request)
			if err != nil {
				return err
			}
			out, err := a.exec(c, bin, "--request", string(b))
			if err != nil {
				return err
			}
			var response map[string]any
			if err = json.Unmarshal(out, &response); err != nil {
				return fmt.Errorf("issue provider response is not JSON: %w", err)
			}
			if err = validateIssueResponse(response, op); err != nil {
				return err
			}
			if a.fields != "" && a.fields != "all" && op == "get" {
				issue := response["issue"].(map[string]any)
				projected := map[string]any{}
				for _, field := range strings.Split(a.fields, ",") {
					field = strings.TrimSpace(field)
					value, ok := issue[field]
					if !ok {
						return fmt.Errorf("unknown issue JSON field %q", field)
					}
					projected[field] = value
				}
				return json.NewEncoder(c.OutOrStdout()).Encode(projected)
			}
			return a.print(c, response)
		}
		root.AddCommand(c)
	}
	return root
}
