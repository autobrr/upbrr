package ci

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLoggingReportTrustBoundary protects the actual workflow entry points:
// PR-controlled policy code remains read-only, and only trusted code can post.
func TestLoggingReportTrustBoundary(t *testing.T) {
	type step struct {
		Uses string
		Run  string
		With map[string]string
		Env  map[string]string
	}
	type job struct {
		If          string
		Needs       string
		Permissions map[string]string
		Concurrency struct {
			Group            string
			Queue            string
			CancelInProgress bool `yaml:"cancel-in-progress"`
		}
		Steps []step
	}
	type workflow struct {
		On          map[string]any
		Permissions map[string]string
		Jobs        map[string]job
	}
	read := func(name string) workflow {
		t.Helper()
		data, err := os.ReadFile("../../.github/workflows/" + name + ".yml")
		if err != nil {
			t.Fatal(err)
		}
		var result workflow
		if err := yaml.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	policy := read("logpolicy")
	if len(policy.Permissions) != 1 || policy.Permissions["contents"] != "read" || len(policy.Jobs["logpolicy"].Permissions) != 0 {
		t.Fatal("PR policy execution must remain read-only")
	}
	var checker, publisherTests bool
	for _, step := range policy.Jobs["logpolicy"].Steps {
		checker = checker || step.Run == "go run ./cmd/logpolicy"
		publisherTests = publisherTests || strings.Contains(step.Run, "node --test scripts/ci/logpolicy-report.test.cjs")
	}
	if !checker || !publisherTests {
		t.Fatal("policy checker and publisher controls must run in PR validation")
	}
	reporter := read("logpolicy-report")
	trigger, ok := reporter.On["workflow_run"].(map[string]any)
	if !ok || len(reporter.On) != 1 || len(reporter.Permissions) != 0 {
		t.Fatal("reporter must use workflow_run with no default token permissions")
	}
	if got, ok := trigger["workflows"].([]any); !ok || len(got) != 1 || got[0] != "logpolicy" {
		t.Fatal("reporter must follow the policy workflow only")
	}
	if reporter.Jobs["analyze"].Permissions["issues"] != "" || reporter.Jobs["publish"].Permissions["pull-requests"] != "write" ||
		reporter.Jobs["publish"].Needs != "analyze" {
		t.Fatal("only the separate publisher may write comments")
	}
	writer := reporter.Jobs["publish"]
	if writer.Concurrency.Group != "logpolicy-comment-${{ github.repository }}" || writer.Concurrency.CancelInProgress || writer.Concurrency.Queue != "max" {
		t.Fatal("comment writes must serialize without interrupting an in-flight writer")
	}
	for name, job := range reporter.Jobs {
		for permission, value := range job.Permissions {
			if value != "read" && (name != "publish" || permission != "pull-requests" || value != "write") {
				t.Fatal("unexpected reporter write permission")
			}
		}
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") &&
				(step.With["ref"] != "${{ github.workflow_sha }}" || step.With["persist-credentials"] != "false") {
				t.Fatal("reporter must only check out trusted workflow code without saved credentials")
			}
		}
	}
	for _, step := range writer.Steps {
		if step.Run != "" || (step.Uses != "" && !strings.HasPrefix(step.Uses, "actions/checkout@") && !strings.HasPrefix(step.Uses, "actions/github-script@")) {
			t.Fatal("writer may only load trusted publisher code, never build or execute PR source")
		}
		if strings.Contains(step.With["script"], "${{") {
			t.Fatal("event and report values must arrive as data, never interpolated JavaScript")
		}
	}
}
