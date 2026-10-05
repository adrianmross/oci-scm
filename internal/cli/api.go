package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

func (a *app) apiCommand() *cobra.Command {
	var method, input string
	c := &cobra.Command{Use: "api PATH", Args: cobra.ExactArgs(1), Short: "Send an OCI-signed regional DevOps API request"}
	c.Flags().StringVarP(&method, "method", "X", "GET", "GET|POST|PUT|PATCH|DELETE")
	c.Flags().StringVar(&input, "input", "", "JSON request body file")
	c.RunE = func(c *cobra.Command, args []string) error {
		cfg, e := a.resolve(c)
		if e != nil {
			return e
		}
		if !regexp.MustCompile(`^[a-z]+-[a-z0-9]+-[0-9]+$`).MatchString(cfg.Region) {
			return fmt.Errorf("a valid OCI region is required")
		}
		path, e := url.Parse(args[0])
		if e != nil || path.IsAbs() || path.Host != "" || !strings.HasPrefix(path.Path, "/") {
			return fmt.Errorf("API path must be relative to the regional OCI DevOps endpoint")
		}
		method = strings.ToUpper(method)
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return fmt.Errorf("unsupported HTTP method")
		}
		uri := "https://devops." + cfg.Region + ".oci.oraclecloud.com" + path.String()
		cmd := []string{"raw-request", "--http-method", method, "--target-uri", uri}
		var body json.RawMessage
		if input != "" {
			input, e = filepath.Abs(input)
			if e != nil {
				return e
			}
			b, e := os.ReadFile(input)
			if e != nil {
				return e
			}
			if !json.Valid(b) {
				return fmt.Errorf("input must be JSON")
			}
			body = b
			cmd = append(cmd, "--request-body", "file://"+input)
		}
		if method != "GET" && !a.apply {
			return a.print(c, map[string]any{"apply": false, "target": cfg, "method": method, "uri": uri, "body": body})
		}
		v, e := a.oci(c, cfg, cmd...)
		if e != nil {
			return e
		}
		if status := str(v, "status"); status != "" && !strings.HasPrefix(status, "2") {
			if e = a.print(c, v); e != nil {
				return e
			}
			return fmt.Errorf("DevOps API returned %s", status)
		}
		return a.print(c, v)
	}
	return c
}

func canonicalRemote(value string) string {
	if !strings.Contains(value, "://") {
		if i := strings.Index(value, ":"); i > 0 {
			value = "ssh://" + value[:i] + "/" + value[i+1:]
		}
	}
	u, e := url.Parse(value)
	if e != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(u.Hostname()) + strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
}

func (a *app) validateRemote(c *cobra.Command, cfg Config, push ...bool) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`).MatchString(cfg.Remote) {
		return fmt.Errorf("invalid Git remote name")
	}
	args := []string{"remote", "get-url", "--all"}
	if len(push) > 0 && push[0] {
		args = append(args, "--push")
	}
	args = append(args, cfg.Remote)
	b, e := a.exec(c, "git", args...)
	if e != nil {
		return e
	}
	v, e := a.oci(c, cfg, "devops", "repository", "get", "--repository-id", cfg.Repository)
	if e != nil {
		return e
	}
	repo := object(v)
	urls := strings.Fields(string(b))
	if len(urls) == 0 {
		return fmt.Errorf("Git remote has no URL")
	}
	for _, value := range urls {
		if u, e := url.Parse(value); e == nil && u.Scheme != "" {
			if u.RawQuery != "" {
				return fmt.Errorf("credential/query-bearing Git URLs are unsupported; use SSH or a credential manager")
			}
			if u.User != nil {
				if _, ok := u.User.Password(); ok {
					return fmt.Errorf("credential-bearing Git URLs are unsupported; use SSH or a credential manager")
				}
			}
		}
		remote := canonicalRemote(value)
		if remote == "" || (remote != canonicalRemote(str(repo, "ssh-url")) && remote != canonicalRemote(str(repo, "http-url"))) {
			return fmt.Errorf("Git remote does not match the selected OCI repository; preserve it and configure the intended remote explicitly")
		}
	}
	return nil
}

func (a *app) verifyPush(c *cobra.Command, cfg Config, branch, head string) error {
	b, e := a.exec(c, "git", "remote", "get-url", "--all", "--push", cfg.Remote)
	if e != nil {
		return e
	}
	for _, remote := range strings.Fields(string(b)) {
		output, e := a.exec(c, "git", "ls-remote", "--heads", remote, "refs/heads/"+branch)
		if e != nil {
			return e
		}
		fields := strings.Fields(string(output))
		if len(fields) < 1 || fields[0] != head {
			return fmt.Errorf("push submitted; remote head verification failed")
		}
	}
	return nil
}
