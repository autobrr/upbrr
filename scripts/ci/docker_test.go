package ci

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDockerPRPublication keeps image export and publication behind the same
// trust boundary, with no registry credentials or write permissions during builds.
func TestDockerPRPublication(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/docker.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On          map[string]any
		Permissions map[string]string
		Jobs        map[string]struct {
			If          string
			Needs       string
			Permissions map[string]string
			Steps       []struct {
				If   string
				Uses string
				Run  string
				With map[string]string
				Env  map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	if _, ok := workflow.On["pull_request"]; !ok || len(workflow.On) != 1 {
		t.Fatal("Docker CI must use only the unprivileged pull_request trigger")
	}
	if len(workflow.Permissions) != 1 || workflow.Permissions["contents"] != "read" {
		t.Fatal("default permissions must remain contents: read")
	}
	build := workflow.Jobs["build"]
	if len(build.Permissions) != 0 {
		t.Fatal("the build must inherit read-only permissions")
	}
	const trustedPR = "github.event.pull_request.head.repo.full_name == github.repository && " +
		"github.event.pull_request.user.login != 'dependabot[bot]' && github.actor != 'dependabot[bot]'"
	const imageTag = "ghcr.io/${{ github.repository }}:pr${{ github.event.pull_request.number }}"
	var exported, uploaded bool
	for _, step := range build.Steps {
		if strings.HasPrefix(step.Uses, "docker/login-action@") {
			t.Fatal("registry login must not run in the build job")
		}
		if strings.HasPrefix(step.Uses, "docker/build-push-action@") {
			exported = step.With["push"] == "false" && step.With["platforms"] == "linux/amd64" &&
				step.With["tags"] == imageTag && step.With["outputs"] == "${{ "+trustedPR+
				" && format('type=docker,dest={0}/image.tar', runner.temp) || '' }}"
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			uploaded = step.If == trustedPR && step.With["name"] == "docker-pr-image" &&
				step.With["path"] == "${{ runner.temp }}/image.tar" && step.With["if-no-files-found"] == "error"
		}
	}
	if !exported || !uploaded {
		t.Fatal("trusted PR builds must export and upload the tagged image without pushing")
	}
	publish := workflow.Jobs["publish"]
	if publish.If != trustedPR || publish.Needs != "build" ||
		len(publish.Permissions) != 1 || publish.Permissions["packages"] != "write" {
		t.Fatal("publication requires a successful trusted build and only packages: write")
	}
	var downloaded, loggedIn, pushed bool
	for _, step := range publish.Steps {
		switch {
		case strings.HasPrefix(step.Uses, "actions/download-artifact@"):
			downloaded = step.With["name"] == "docker-pr-image" && step.With["path"] == "${{ runner.temp }}/docker-pr-image"
		case strings.HasPrefix(step.Uses, "docker/login-action@"):
			loggedIn = step.With["registry"] == "ghcr.io" && step.With["username"] == "${{ github.actor }}" &&
				step.With["password"] == "${{ secrets.GITHUB_TOKEN }}"
		case step.Uses != "":
			t.Fatal("the publisher may only download the image and log in to the registry")
		}
		if step.Run != "" {
			pushed = step.Run == "docker load --input \"$IMAGE_ARCHIVE\"\ndocker push \"$IMAGE_TAG\"\n" &&
				step.Env["IMAGE_ARCHIVE"] == "${{ runner.temp }}/docker-pr-image/image.tar" && step.Env["IMAGE_TAG"] == imageTag
			if !pushed {
				t.Fatal("the publisher may only load and push the exported image, never run it")
			}
		}
	}
	if !downloaded || !loggedIn || !pushed {
		t.Fatal("publication must download the exported PR image, log in, and push it")
	}
}
