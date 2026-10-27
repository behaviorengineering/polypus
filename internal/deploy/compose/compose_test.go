package compose

import (
	"slices"
	"testing"
)

func TestArgsProject(t *testing.T) {
	c := Client{Project: "polypus-work", ComposeFile: "/repo/docker-compose.deploy.yml", EnvFiles: []string{"/repo/.env.images", "/repo/.env.deploy"}}
	got := c.Args("exec", "-T", "gateway", "cat", "/x")
	want := []string{
		"compose", "-f", "/repo/docker-compose.deploy.yml", "-p", "polypus-work",
		"--env-file", "/repo/.env.images", "--env-file", "/repo/.env.deploy",
		"exec", "-T", "gateway", "cat", "/x",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args %v\nwant %v", got, want)
	}
}

func TestArgsWithoutProject(t *testing.T) {
	got := Client{ComposeFile: "f.yml"}.Args("up", "-d")
	if want := []string{"compose", "-f", "f.yml", "up", "-d"}; !slices.Equal(got, want) {
		t.Fatalf("args %v want %v", got, want)
	}
}
