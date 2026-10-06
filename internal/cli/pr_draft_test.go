package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPRReviewReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, initial, wanted, status string
		undo, apply, ignore, noETag   bool
		wantError                     bool
	}{
		{name: "plan", initial: "DRAFT", wanted: "READY"},
		{name: "ready", initial: "DRAFT", wanted: "READY", apply: true},
		{name: "undo", initial: "READY", wanted: "DRAFT", undo: true, apply: true},
		{name: "idempotent", initial: "READY", wanted: "READY", apply: true},
		{name: "ignored", initial: "DRAFT", wanted: "READY", apply: true, ignore: true, wantError: true},
		{name: "rejected", initial: "DRAFT", wanted: "READY", apply: true, status: "412 Precondition Failed", wantError: true},
		{name: "missing etag", initial: "DRAFT", wanted: "READY", apply: true, noETag: true, wantError: true},
		{name: "unknown", initial: "UNKNOWN", wanted: "READY", apply: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			state, writes := tc.initial, 0
			f := &fakeOCI{}
			run := func(ctx context.Context, dir, bin string, args ...string) ([]byte, error) {
				if !strings.Contains(strings.Join(args, " "), "raw-request") {
					return f.run(ctx, dir, bin, args...)
				}
				get := func(flag string) string {
					for i, s := range args {
						if s == flag && i+1 < len(args) {
							return args[i+1]
						}
					}
					return ""
				}
				if get("--http-method") == "PUT" {
					writes++
					var body, headers map[string]string
					if err := json.Unmarshal([]byte(get("--request-body")), &body); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal([]byte(get("--request-headers")), &headers); err != nil {
						t.Fatal(err)
					}
					if body["reviewStatus"] != tc.wanted || len(body) != 1 || headers["if-match"] != "test-etag" {
						t.Fatalf("unguarded/wrong update: %v %v", body, headers)
					}
					if tc.status != "" {
						return jsonBytes(map[string]any{"status": tc.status}), nil
					}
					if !tc.ignore {
						state = body["reviewStatus"]
					}
				}
				headers := map[string]any{}
				if !tc.noETag {
					headers["ETag"] = "test-etag"
				}
				return jsonBytes(map[string]any{"status": "200 OK", "headers": headers, "data": map[string]any{"id": "ocid1.devopspullrequest.test", "repositoryId": "ocid1.devopsrepository.test", "reviewStatus": state, "lifecycleDetails": "OPEN"}}), nil
			}
			args := []string{"pr", "ready", "ocid1.devopspullrequest.test"}
			if tc.undo {
				args = append(args, "--undo")
			}
			if tc.apply {
				args = append(args, "--apply")
			}
			out, err := invoke(t, run, append(args, targetArgs()...)...)
			if (err != nil) != tc.wantError {
				t.Fatalf("output %s error %v", out, err)
			}
			wantWrites := 0
			if tc.apply && tc.initial != tc.wanted && !tc.noETag && tc.initial != "UNKNOWN" {
				wantWrites = 1
			}
			if writes != wantWrites {
				t.Fatalf("writes: %d expected %d", writes, wantWrites)
			}
		})
	}
}

func TestDraftCreationUsesRawStatus(t *testing.T) {
	cleanEnv(t)
	f := &fakeOCI{}
	posts := 0
	run := func(ctx context.Context, dir, bin string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "raw-request") && strings.Contains(joined, "--http-method POST") {
			posts++
			for i, arg := range args {
				if arg == "--request-body" {
					var body map[string]any
					if err := json.Unmarshal([]byte(args[i+1]), &body); err != nil {
						return nil, err
					}
					if body["reviewStatus"] != "DRAFT" || body["repositoryId"] != "ocid1.devopsrepository.test" {
						return nil, fmt.Errorf("wrong body %v", body)
					}
				}
			}
			return jsonBytes(map[string]any{"status": "200 OK", "data": map[string]any{"id": "ocid1.devopspullrequest.test"}}), nil
		}
		if strings.Contains(joined, "raw-request") {
			return jsonBytes(map[string]any{"status": "200 OK", "data": map[string]any{"id": "ocid1.devopspullrequest.test", "repositoryId": "ocid1.devopsrepository.test", "sourceBranch": "feature", "destinationBranch": "main", "displayName": "Example", "reviewStatus": "DRAFT"}}), nil
		}
		return f.run(ctx, dir, bin, args...)
	}
	args := append([]string{"pr", "create", "--draft", "--title", "Example", "--head", "feature", "--base", "main"}, targetArgs()...)
	out, err := invoke(t, run, args...)
	if err != nil || posts != 0 || !strings.Contains(out, `"reviewStatus": "DRAFT"`) {
		t.Fatalf("plan: %s %v", out, err)
	}
	out, err = invoke(t, run, append(args, "--apply")...)
	if err != nil || posts != 1 || !strings.Contains(out, `"isDraft": true`) {
		t.Fatalf("apply: %s %v", out, err)
	}
}
