package stack

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// composeService is the slice of a compose service the volume-owner checks read.
type composeService struct {
	Image      string                       `yaml:"image"`
	User       string                       `yaml:"user"`
	Restart    string                       `yaml:"restart"`
	Profiles   []string                     `yaml:"profiles"`
	Entrypoint []string                     `yaml:"entrypoint"`
	Volumes    []string                     `yaml:"volumes"`
	DependsOn  map[string]map[string]string `yaml:"depends_on"`
}

// namedMounts returns the mount targets of svc's named volumes (bind mounts skipped).
func namedMounts(t *testing.T, svc composeService, named map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, v := range svc.Volumes {
		src, rest, ok := strings.Cut(v, ":")
		if !ok {
			t.Fatalf("volume %q has no target", v)
		}
		if _, isNamed := named[src]; !isNamed {
			continue
		}
		target, _, _ := strings.Cut(rest, ":")
		out[src] = target
	}
	return out
}

// TestVolumeOwnerCoversNonRootServices fails when gateway or Switchyard mounts a named
// volume that volume-owner does not chown, or starts before volume-owner has finished,
// which leaves a volume an older root image created unwritable for uid 10001.
func TestVolumeOwnerCoversNonRootServices(t *testing.T) {
	var doc struct {
		Services map[string]composeService `yaml:"services"`
		Volumes  map[string]any            `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(composeFile, &doc); err != nil {
		t.Fatal(err)
	}
	owner, ok := doc.Services["volume-owner"]
	if !ok {
		t.Fatal("compose file has no volume-owner service")
	}
	if owner.User != "0:0" || owner.Restart != "no" || len(owner.Profiles) != 0 {
		t.Fatalf("volume-owner user=%q restart=%q profiles=%v, want 0:0, no and none", owner.User, owner.Restart, owner.Profiles)
	}
	if owner.Image != doc.Services[ServiceGateway].Image {
		t.Fatalf("volume-owner image %q, want the gateway image %q", owner.Image, doc.Services[ServiceGateway].Image)
	}
	if len(owner.Entrypoint) < 3 || owner.Entrypoint[0] != "chown" || owner.Entrypoint[2] != "10001:10001" {
		t.Fatalf("volume-owner entrypoint %v, want chown -R 10001:10001 <mounts>", owner.Entrypoint)
	}
	chowned := owner.Entrypoint[3:]
	ownerMounts := namedMounts(t, owner, doc.Volumes)
	for _, name := range []string{ServiceGateway, ServiceSwitchyard} {
		svc, ok := doc.Services[name]
		if !ok {
			t.Fatalf("compose file has no %s service", name)
		}
		for vol, target := range namedMounts(t, svc, doc.Volumes) {
			if ownerMounts[vol] != target {
				t.Errorf("%s mounts %s at %s; volume-owner mounts it at %q", name, vol, target, ownerMounts[vol])
			}
			if !slices.Contains(chowned, target) {
				t.Errorf("%s mounts %s at %s, which volume-owner does not chown", name, vol, target)
			}
		}
		if got := svc.DependsOn["volume-owner"]["condition"]; got != "service_completed_successfully" {
			t.Errorf("%s depends_on volume-owner condition %q, want service_completed_successfully", name, got)
		}
	}
}
