package cli

import (
	"strings"
	"testing"
)

func TestInlineCommentsVerifyLocationAndDeduplicate(t *testing.T) {
	cleanEnv(t)
	f := &fakeOCI{}
	args := append([]string{"pr", "comment", "ocid1.devopspullrequest.test", "--body", "Check this", "--path", "src/a.go", "--commit", strings.Repeat("a", 40), "--line", "2", "--apply"}, targetArgs()...)
	for i := 0; i < 2; i++ {
		if _, e := invoke(t, f.run, args...); e != nil {
			t.Fatal(e)
		}
	}
	if f.mutations != 1 {
		t.Fatal("inline retry was not deduplicated")
	}
	for i, arg := range args {
		if arg == "src/a.go" {
			args[i] = "src/b.go"
		}
	}
	if _, e := invoke(t, f.run, args...); e != nil {
		t.Fatal(e)
	}
	if f.mutations != 2 {
		t.Fatal("same text on another file was incorrectly deduplicated")
	}
	if f.payload["fileType"] != "SOURCE" || f.payload["lineNumber"] != float64(2) {
		t.Fatal("inline location lost")
	}
	for _, extra := range [][]string{{"--path", "../escape", "--commit", "sha", "--line", "2"}, {"--path", "a.go"}, {"--line", "-1"}, {"--parent", "root", "--path", "a.go", "--commit", "sha", "--line", "2"}} {
		if _, e := invoke(t, f.run, append(append([]string{"pr", "comment", "ocid1.devopspullrequest.test", "--body", "text"}, extra...), targetArgs()...)...); e == nil {
			t.Fatalf("invalid location accepted: %v", extra)
		}
	}
	if f.mutations != 2 {
		t.Fatal("invalid input caused mutation")
	}
}
