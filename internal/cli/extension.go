package cli

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var extensionName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

type extension struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Subdir string `json:"subdir,omitempty"`
	Pin    string `json:"pin,omitempty"`
	Local  bool   `json:"local"`
}

func (e extension) fields() map[string]any {
	return map[string]any{"name": e.Name, "source": e.Source, "path": e.Path, "kind": e.Kind, "subdir": e.Subdir, "pin": e.Pin, "local": e.Local}
}

func extensionsDir() (string, error) {
	if dir := os.Getenv("OSCM_EXTENSION_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Abs(filepath.Join(base, "oci-scm", "extensions"))
}

func extensionDir(name string) (string, error) {
	if !extensionName.MatchString(name) {
		return "", fmt.Errorf("invalid extension name %q", name)
	}
	base, err := extensionsDir()
	return filepath.Join(base, name), err
}

func loadExtension(name string) (extension, string, error) {
	var e extension
	dir, err := extensionDir(name)
	if err == nil {
		err = readJSON(filepath.Join(dir, "extension.json"), &e)
	}
	if err == nil && e.Name != name {
		err = fmt.Errorf("extension metadata name mismatch")
	}
	return e, dir, err
}

// Require the selected directory to stay within the repository, including symlinks.
func extensionPath(root, subdir string) (string, error) {
	if filepath.IsAbs(subdir) || strings.Contains(subdir, "\\") {
		return "", fmt.Errorf("extension subdir must be a relative directory")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, subdir))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("extension directory escapes its repository")
	}
	return path, nil
}

func inspectExtension(e *extension) error {
	var manifest struct {
		Schema string `json:"schema"`
		Kind   string `json:"kind"`
	}
	err := readJSON(filepath.Join(e.Path, "oscm-extension.json"), &manifest)
	if err == nil {
		if manifest.Schema != "oci-scm.extension.v1" || manifest.Kind != "neovim" {
			return fmt.Errorf("unsupported extension manifest")
		}
		e.Kind = "neovim"
		info, err := os.Stat(filepath.Join(e.Path, "lua"))
		if err != nil || !info.IsDir() {
			return fmt.Errorf("Neovim extension requires a lua directory")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	e.Kind = "command"
	_, err = exec.LookPath(filepath.Join(e.Path, "oscm-"+e.Name))
	if err != nil {
		return fmt.Errorf("extension requires executable oscm-%s or a Neovim manifest", e.Name)
	}
	return nil
}

func (a *app) extensionCommands() *cobra.Command {
	r := &cobra.Command{Use: "extension", Aliases: []string{"extensions", "ext"}, Short: "Manage optional command and Neovim extensions (no OCI authentication required)"}
	var name, subdir, pin string
	install := &cobra.Command{Use: "install REPOSITORY", Args: cobra.ExactArgs(1), Short: "Install a Git repository or link a local extension (plan unless --apply)"}
	install.Flags().StringVar(&name, "name", "", "Extension name (default repository name without oscm-)")
	install.Flags().StringVar(&subdir, "subdir", ".", "Extension directory within the repository")
	install.Flags().StringVar(&pin, "pin", "", "Pin a remote extension to a Git tag or commit")
	install.RunE = func(c *cobra.Command, args []string) error {
		if strings.HasPrefix(pin, "-") {
			return fmt.Errorf("--pin must be a Git tag or commit, not an option")
		}
		source := args[0]
		localPath := source
		if !filepath.IsAbs(localPath) {
			localPath = filepath.Join(a.dir, localPath)
		}
		explicitLocal := filepath.IsAbs(source) || source == "." || source == ".." || strings.HasPrefix(source, "."+string(filepath.Separator)) || strings.HasPrefix(source, ".."+string(filepath.Separator))
		info, err := os.Stat(localPath)
		local := explicitLocal && err == nil && info.IsDir()
		if explicitLocal && !local {
			return fmt.Errorf("local extension directory does not exist or is inaccessible")
		}
		if local {
			source, err = filepath.Abs(localPath)
			if pin != "" {
				return fmt.Errorf("local extensions are linked; --pin requires a remote repository")
			}
		} else if !strings.Contains(source, "://") {
			parts := strings.Split(source, "/")
			if len(parts) != 2 || !extensionName.MatchString(parts[0]) || !extensionName.MatchString(strings.TrimSuffix(parts[1], ".git")) {
				return fmt.Errorf("use OWNER/REPO, an HTTPS repository URL, or an existing local directory")
			}
			source = "https://github.com/" + strings.TrimSuffix(source, ".git") + ".git"
		} else {
			u, parseErr := url.Parse(source)
			if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("use an HTTPS repository URL without embedded credentials, query, or fragment")
			}
			source = strings.TrimRight(source, "/")
		}
		if err != nil && local {
			return err
		}
		selected := name
		if selected == "" {
			selected = strings.TrimPrefix(strings.TrimSuffix(filepath.Base(source), ".git"), "oscm-")
		}
		dir, err := extensionDir(selected)
		if err != nil {
			return err
		}
		if filepath.IsAbs(subdir) || filepath.Clean(subdir) == ".." || strings.HasPrefix(filepath.Clean(subdir), ".."+string(filepath.Separator)) || strings.Contains(subdir, "\\") {
			return fmt.Errorf("extension subdir must stay within its repository")
		}
		if _, err = os.Lstat(dir); !os.IsNotExist(err) {
			return fmt.Errorf("extension %s already exists or cannot be inspected", selected)
		}
		e := extension{Name: selected, Source: source, Subdir: subdir, Pin: pin, Local: local}
		if !a.apply {
			return a.print(c, map[string]any{"apply": false, "extension": e, "directory": dir})
		}
		if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
			return err
		}
		if err = os.Mkdir(dir, 0700); err != nil {
			return err
		}
		complete := false
		defer func() {
			if !complete {
				_ = os.RemoveAll(dir)
			}
		}()
		root := source
		if !local {
			root = filepath.Join(dir, "repo")
			if _, err = a.exec(c, "git", "clone", "--", source, root); err != nil {
				return err
			}
			if pin != "" {
				if _, err = a.exec(c, "git", "-C", root, "checkout", "--detach", pin, "--"); err != nil {
					return err
				}
			}
		}
		if e.Path, err = extensionPath(root, subdir); err != nil {
			return err
		}
		if err = inspectExtension(&e); err != nil {
			return err
		}
		if err = writeJSON(filepath.Join(dir, "extension.json"), e); err != nil {
			return err
		}
		complete = true
		return a.print(c, e.fields())
	}
	r.AddCommand(install)
	r.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List installed extensions and their runtime paths", RunE: func(c *cobra.Command, _ []string) error {
		base, err := extensionsDir()
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(base)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		result := []map[string]any{}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			e, _, err := loadExtension(entry.Name())
			if err != nil {
				return err
			}
			if a.fields == "" {
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\n", e.Name, e.Kind, e.Path)
			}
			result = append(result, e.fields())
		}
		if a.fields == "" {
			return nil
		}
		return a.print(c, result)
	}})
	for _, action := range []string{"upgrade", "remove", "path"} {
		r.AddCommand(&cobra.Command{Use: action + " NAME", Args: cobra.ExactArgs(1), Short: map[string]string{"upgrade": "Fast-forward an unpinned remote extension", "remove": "Remove an installed extension; keep local source files", "path": "Print an installed extension's runtime directory"}[action], RunE: func(c *cobra.Command, args []string) error {
			e, dir, err := loadExtension(args[0])
			if err != nil {
				return err
			}
			if action == "path" {
				_, err = fmt.Fprintln(c.OutOrStdout(), e.Path)
				return err
			}
			if !a.apply {
				return a.print(c, map[string]any{"apply": false, "operation": action, "extension": e})
			}
			if action == "remove" {
				if err = os.RemoveAll(dir); err != nil {
					return err
				}
				return a.print(c, map[string]any{"removed": e.Name})
			}
			if e.Local || e.Pin != "" {
				return fmt.Errorf("local and pinned extensions are not upgraded; edit the local source or reinstall with a new pin")
			}
			root := filepath.Join(dir, "repo")
			status, err := a.exec(c, "git", "-C", root, "status", "--porcelain")
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(status)) != "" {
				return fmt.Errorf("extension checkout has local changes; preserve them before upgrading")
			}
			if _, err = a.exec(c, "git", "-C", root, "pull", "--ff-only"); err != nil {
				return err
			}
			if e.Path, err = extensionPath(root, e.Subdir); err != nil {
				return err
			}
			if err = inspectExtension(&e); err != nil {
				return err
			}
			if err = writeJSON(filepath.Join(dir, "extension.json"), e); err != nil {
				return err
			}
			return a.print(c, e.fields())
		}})
	}
	r.AddCommand(&cobra.Command{Use: "exec NAME [ARGS...]", Args: cobra.MinimumNArgs(1), DisableFlagParsing: true, Short: "Run an installed command extension (external code)", RunE: func(c *cobra.Command, args []string) error {
		e, _, err := loadExtension(args[0])
		if err != nil {
			return err
		}
		if e.Kind != "command" {
			return fmt.Errorf("%s is a Neovim plugin; add the extension path to Neovim's runtimepath instead", e.Name)
		}
		if err = inspectExtension(&e); err != nil {
			return err
		}
		// Explicit exec is the authorization to run external code, like gh extension exec.
		argv := args[1:]
		if len(argv) > 0 && argv[0] == "--" {
			argv = argv[1:]
		}
		executable, err := exec.LookPath(filepath.Join(e.Path, "oscm-"+e.Name))
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(c.Context(), executable, argv...)
		cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = a.dir, c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr()
		return cmd.Run()
	}})
	return r
}
