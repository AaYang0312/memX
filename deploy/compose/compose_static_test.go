// Package compose guards deploy/compose/docker-compose.yml with static checks.
//
// The personal Task 1 stack is never pulled or started (implementation plan
// §7.7): these tests scan the file textually, using the standard library only,
// so the read-only CI job (no Docker daemon) still enforces the invariants
// recorded in ADR 0006 and plan §7.3: opt-in profiles, digest-pinned images,
// healthchecks with persistent volumes, loopback-only ports and no default
// credentials. The authoritative full parse remains
// `docker compose config --quiet` in environments that have Docker.
package compose

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const composeFile = "docker-compose.yml"

// expectedImages mirrors ADR 0006: tag plus multi-platform manifest-index
// digest, verified 2026-09-23. Compose and the ADR must change together.
var expectedImages = map[string]string{
	"postgres":      "postgres:18.6-bookworm@sha256:3725f4e2499eef5134592b3b4ab79a543ed7f8e533b05b5b637af926630f6650",
	"kafka":         "apache/kafka:4.3.1@sha256:77e3df9054047a88b520d0cc46e16696d3b22022e1d580aeccd2632df6532837",
	"redis":         "redis:8.10.1@sha256:8a1efc5f479551822b47424ccae982026b633f28818eab0387348120a61e10e2",
	"elasticsearch": "docker.elastic.co/elasticsearch/elasticsearch:8.19.21@sha256:cbf5cd6cfe5532a9c02d510c66d238bf329cd51fe3d57170a2a880aca7d47419",
	"qdrant":        "qdrant/qdrant:v1.19.0@sha256:057ee3a8da769fe7310dd3537b4dc7583bf87a95ce8ac43c0af5a46bc580d1fc",
}

var expectedServices = []string{"elasticsearch", "kafka", "postgres", "qdrant", "redis"}

var expectedVolumes = []string{"esdata", "kafkadata", "pgdata", "qdrantdata", "redisdata"}

type composeLine struct {
	indent int
	text   string
}

type serviceBlock struct {
	name  string
	lines []composeLine
}

var (
	imageRe       = regexp.MustCompile(`^image:\s*([^@\s]+)@sha256:([0-9a-f]{64})$`)
	namedVolumeRe = regexp.MustCompile(`^([a-z][a-z0-9_-]*):`)
	loopbackRe    = regexp.MustCompile(`^(127\.0\.0\.1|\[::1\]):\d+:\d+$`)
)

type composeFileModel struct {
	services map[string]serviceBlock
	volumes  []string
	lines    []composeLine
}

func parseComposeFile(t *testing.T) composeFileModel {
	t.Helper()
	raw, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("read %s: %v", composeFile, err)
	}
	model := composeFileModel{services: map[string]serviceBlock{}}
	var blocks []*serviceBlock
	section := ""
	for _, rawLine := range strings.Split(string(raw), "\n") {
		text := strings.TrimSpace(strings.TrimRight(rawLine, "\r"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		line := composeLine{
			indent: len(rawLine) - len(strings.TrimLeft(rawLine, " \t")),
			text:   text,
		}
		model.lines = append(model.lines, line)
		if line.indent == 0 && strings.HasSuffix(text, ":") {
			section = strings.TrimSuffix(text, ":")
			continue
		}
		switch section {
		case "services":
			if line.indent == 2 {
				if !strings.HasSuffix(text, ":") || strings.HasPrefix(text, "- ") {
					t.Fatalf("unexpected line under services: %q", text)
				}
				name := strings.TrimSuffix(text, ":")
				if _, dup := model.services[name]; dup {
					t.Fatalf("duplicate service %q", name)
				}
				blocks = append(blocks, &serviceBlock{name: name})
				continue
			}
			if len(blocks) == 0 {
				t.Fatalf("service content before any service key: %q", text)
			}
			last := blocks[len(blocks)-1]
			last.lines = append(last.lines, line)
		case "volumes":
			if line.indent == 2 && strings.HasSuffix(text, ":") && !strings.HasPrefix(text, "- ") {
				model.volumes = append(model.volumes, strings.TrimSuffix(text, ":"))
			}
		}
	}
	for _, b := range blocks {
		model.services[b.name] = *b
	}
	return model
}

func (s serviceBlock) hasKey(key string) bool {
	for _, l := range s.lines {
		if l.indent == 4 && l.text == key+":" {
			return true
		}
	}
	return false
}

func (s serviceBlock) hasKeyEquals(key, value string) bool {
	for _, l := range s.lines {
		if l.indent == 4 && l.text == key+": "+value {
			return true
		}
	}
	return false
}

func (s serviceBlock) hasSubKey(key, sub string) bool {
	for i, l := range s.lines {
		if l.indent != 4 || l.text != key+":" {
			continue
		}
		for _, next := range s.lines[i+1:] {
			if next.indent <= 4 {
				break
			}
			if next.indent == 6 && strings.HasPrefix(next.text, sub+":") {
				return true
			}
		}
	}
	return false
}

// listItems collects the `- item` entries of a block-style key (ports,
// volumes). Missing keys yield nil.
func (s serviceBlock) listItems(key string) []string {
	for i, l := range s.lines {
		if l.indent != 4 || l.text != key+":" {
			continue
		}
		var items []string
		for _, next := range s.lines[i+1:] {
			if next.indent <= 4 {
				break
			}
			if strings.HasPrefix(next.text, "- ") {
				items = append(items, strings.TrimSpace(strings.TrimPrefix(next.text, "- ")))
			}
		}
		return items
	}
	return nil
}

// profiles accepts both `profiles: [a, b]` and block style; it must never be
// empty, otherwise the service starts on a bare `docker compose up`.
func (s serviceBlock) profiles() []string {
	for i, l := range s.lines {
		if l.indent != 4 || !strings.HasPrefix(l.text, "profiles:") {
			continue
		}
		inline := strings.TrimSpace(strings.TrimPrefix(l.text, "profiles:"))
		if inline != "" {
			var out []string
			for _, p := range strings.Split(strings.Trim(inline, "[]"), ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			return out
		}
		var out []string
		for _, next := range s.lines[i+1:] {
			if next.indent <= 4 {
				break
			}
			if strings.HasPrefix(next.text, "- ") {
				out = append(out, strings.TrimSpace(strings.TrimPrefix(next.text, "- ")))
			}
		}
		return out
	}
	return nil
}

func lookupService(t *testing.T, model composeFileModel, name string) serviceBlock {
	t.Helper()
	svc, ok := model.services[name]
	if !ok {
		t.Fatalf("service %q is missing from %s", name, composeFile)
	}
	return svc
}

func TestServicesAreExactlyThePersonalTask1Stack(t *testing.T) {
	model := parseComposeFile(t)
	want := map[string]bool{}
	for _, name := range expectedServices {
		want[name] = true
		if _, ok := model.services[name]; !ok {
			t.Errorf("missing service %q", name)
		}
	}
	for name := range model.services {
		if !want[name] {
			t.Errorf("unexpected service %q; the personal Task 1 stack is exactly %v", name, expectedServices)
		}
	}
}

func TestEveryServiceIsOptInViaExplicitProfiles(t *testing.T) {
	model := parseComposeFile(t)
	for _, name := range expectedServices {
		profiles := lookupService(t, model, name).profiles()
		if len(profiles) == 0 {
			t.Errorf("service %q has no profiles; a bare `docker compose up` would start it (ADR 0006 run discipline 1)", name)
			continue
		}
		for _, p := range profiles {
			if p == "default" {
				t.Errorf("service %q uses the default profile, which is active without --profile", name)
			}
		}
	}
}

func TestEveryImageIsPinnedToTheADRDigest(t *testing.T) {
	model := parseComposeFile(t)
	for _, name := range expectedServices {
		want, ok := expectedImages[name]
		if !ok {
			t.Fatalf("no ADR 0006 pin recorded for service %q", name)
		}
		svc := lookupService(t, model, name)
		got := ""
		for _, l := range svc.lines {
			if m := imageRe.FindStringSubmatch(l.text); m != nil {
				if got != "" {
					t.Errorf("service %q declares multiple images", name)
				}
				got = m[1] + "@sha256:" + m[2]
			}
		}
		if got == "" {
			t.Errorf("service %q has no image pinned as tag@sha256:<64-hex digest>", name)
			continue
		}
		if got != want {
			t.Errorf("service %q image %s does not match the ADR 0006 pin %s; update Compose and the ADR together, never drop the digest", name, got, want)
		}
		if !svc.hasKeyEquals("platform", "linux/amd64") {
			t.Errorf("service %q does not pin platform linux/amd64 (ADR 0006 observes linux/amd64 in every manifest index)", name)
		}
	}
}

func TestEveryServiceHasHealthcheckAndNamedVolume(t *testing.T) {
	model := parseComposeFile(t)
	declared := map[string]bool{}
	for _, v := range model.volumes {
		declared[v] = true
	}
	if len(declared) != len(expectedVolumes) {
		t.Errorf("top-level volumes are %v, want exactly %v", model.volumes, expectedVolumes)
	}
	for _, want := range expectedVolumes {
		if !declared[want] {
			t.Errorf("named volume %q is not declared at the top level", want)
		}
	}
	for _, name := range expectedServices {
		svc := lookupService(t, model, name)
		if !svc.hasKey("healthcheck") || !svc.hasSubKey("healthcheck", "test") {
			t.Errorf("service %q lacks a healthcheck with a test command (plan §7.3)", name)
		}
		mounts := svc.listItems("volumes")
		if len(mounts) == 0 {
			t.Errorf("service %q has no persistent volume mount (plan §7.3)", name)
			continue
		}
		for _, m := range mounts {
			match := namedVolumeRe.FindStringSubmatch(m)
			if match == nil {
				t.Errorf("service %q has an unexpected volume mount %q; named volumes only", name, m)
				continue
			}
			if !declared[match[1]] {
				t.Errorf("service %q mounts volume %q which is not declared at the top level", name, match[1])
			}
		}
	}
}

func TestPublishedPortsAreLoopbackOnly(t *testing.T) {
	model := parseComposeFile(t)
	for _, l := range model.lines {
		if strings.Contains(l.text, "0.0.0.0") {
			t.Errorf("%s must not listen on 0.0.0.0: %q", composeFile, l.text)
		}
	}
	for _, name := range expectedServices {
		for _, publish := range lookupService(t, model, name).listItems("ports") {
			publish = strings.Trim(publish, `"`)
			if !loopbackRe.MatchString(publish) {
				t.Errorf("service %q publishes %q; only 127.0.0.1/[::1] host bindings are allowed (ADR 0006: no public ports)", name, publish)
			}
		}
	}
}

func TestNoDefaultCredentials(t *testing.T) {
	model := parseComposeFile(t)
	found := false
	for _, l := range model.lines {
		if strings.Contains(l.text, "${MEMX_LOCAL_PG_PASSWORD:-") || strings.Contains(l.text, "${MEMX_LOCAL_PG_PASSWORD-") {
			t.Errorf("the PG password must have no fallback default: %q", l.text)
		}
		if strings.Contains(l.text, "POSTGRES_PASSWORD") {
			found = true
			if !strings.HasPrefix(l.text, "POSTGRES_PASSWORD: ${MEMX_LOCAL_PG_PASSWORD:?") {
				t.Errorf("POSTGRES_PASSWORD must come from the required MEMX_LOCAL_PG_PASSWORD variable, got %q", l.text)
			}
		}
	}
	if !found {
		t.Fatalf("no POSTGRES_PASSWORD configuration found in %s", composeFile)
	}
}
