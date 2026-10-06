package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

// Raw requests retain reviewStatus, which older OCI SDK models discard.
func (a *app) rawPRRequest(c *cobra.Command, cfg Config, method, path string, body map[string]any, etag string) (map[string]any, error) {
	if !regexp.MustCompile(`^[a-z]+-[a-z0-9]+-[0-9]+$`).MatchString(cfg.Region) {
		return nil, fmt.Errorf("a valid OCI region is required")
	}
	uri := "https://devops." + cfg.Region + ".oci.oraclecloud.com" + path
	if method != "GET" && !a.apply {
		return map[string]any{"apply": false, "target": cfg, "method": method, "uri": uri, "input": body, "ifMatch": etag}, nil
	}
	args := []string{"raw-request", "--http-method", method, "--target-uri", uri}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		args = append(args, "--request-body", string(b))
	}
	if etag != "" {
		h, _ := json.Marshal(map[string]string{"if-match": etag})
		args = append(args, "--request-headers", string(h))
	}
	v, err := a.oci(c, cfg, args...)
	if err != nil {
		return nil, err
	}
	if status := str(v, "status"); !strings.HasPrefix(status, "2") {
		return nil, fmt.Errorf("DevOps API returned %q: %v", status, v["data"])
	}
	if headers, ok := v["headers"].(map[string]any); ok {
		for k, value := range headers {
			if strings.EqualFold(k, "etag") {
				v["etag"] = value
			}
		}
	}
	v["data"] = normalizePRJSON(v["data"])
	return v, nil
}

func normalizePRJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			var name strings.Builder
			for _, r := range key {
				if unicode.IsUpper(r) {
					name.WriteByte('-')
					r = unicode.ToLower(r)
				}
				name.WriteRune(r)
			}
			switch key {
			case "freeformTags", "definedTags", "systemTags":
				out[name.String()] = item
			default:
				out[name.String()] = normalizePRJSON(item)
			}
		}
		if status, ok := out["review-status"].(string); ok {
			if status == "DRAFT" || status == "READY" {
				out["isDraft"] = status == "DRAFT"
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizePRJSON(item)
		}
		return out
	}
	return value
}

func (a *app) rawPR(c *cobra.Command, cfg Config, id string) (map[string]any, error) {
	v, err := a.rawPRRequest(c, cfg, "GET", "/20210630/pullRequests/"+url.PathEscape(id), nil, "")
	if err == nil && cfg.Repository != "" && str(object(v), "repository-id") != cfg.Repository {
		return nil, fmt.Errorf("pull request belongs to a different repository")
	}
	return v, err
}

func (a *app) prReadyCommand() *cobra.Command {
	var undo bool
	c := &cobra.Command{Use: "ready [OCID|URL|branch]", Args: cobra.MaximumNArgs(1), Short: "Mark ready for review (plan unless --apply)"}
	c.Flags().BoolVar(&undo, "undo", false, "Convert back to draft")
	c.RunE = func(c *cobra.Command, args []string) error {
		cfg, err := a.resolve(c)
		if err != nil {
			return err
		}
		selector := ""
		if len(args) > 0 {
			selector = args[0]
		}
		before, err := a.getPR(c, cfg, selector)
		if err != nil {
			return err
		}
		id := str(object(before), "id")
		before, err = a.rawPR(c, cfg, id)
		if err != nil {
			return err
		}
		pr := object(before)
		if str(pr, "lifecycle-details") != "OPEN" {
			return fmt.Errorf("only open pull requests can change review readiness")
		}
		status := str(pr, "review-status")
		if status != "DRAFT" && status != "READY" {
			return fmt.Errorf("unsupported or missing reviewStatus %q", status)
		}
		wanted := "READY"
		if undo {
			wanted = "DRAFT"
		}
		if status == wanted {
			return a.print(c, map[string]any{"alreadySet": true, "data": pr})
		}
		etag := str(before, "etag")
		if etag == "" {
			return fmt.Errorf("missing ETag; refusing an unguarded review-status update")
		}
		v, err := a.rawPRRequest(c, cfg, "PUT", "/20210630/pullRequests/"+url.PathEscape(id), map[string]any{"reviewStatus": wanted}, etag)
		if err != nil {
			return err
		}
		if !a.apply {
			return a.print(c, v)
		}
		after, err := a.rawPR(c, cfg, id)
		if err != nil {
			return fmt.Errorf("review-status update submitted; readback failed: %w (inspect before retrying)", err)
		}
		if str(object(after), "review-status") != wanted {
			return fmt.Errorf("review-status update submitted; readback differs (inspect before retrying)")
		}
		return a.print(c, map[string]any{"verified": true, "data": object(after)})
	}
	return c
}
