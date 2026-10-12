package distribution

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGoReleaserConfigMatchesDistributionSpec(t *testing.T) {
	data, err := os.ReadFile("../../.goreleaser.yml") // #nosec G304 -- test reads repository release config.
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		ProjectName string `yaml:"project_name"`
		Builds      []struct {
			Binary string   `yaml:"binary"`
			Goos   []string `yaml:"goos"`
			Goarch []string `yaml:"goarch"`
			Env    []string `yaml:"env"`
		} `yaml:"builds"`
		Archives []struct {
			NameTemplate string   `yaml:"name_template"`
			Formats      []string `yaml:"formats"`
		} `yaml:"archives"`
		Signs []struct {
			Cmd string `yaml:"cmd"`
		} `yaml:"signs"`
		Dockers []struct {
			ImageTemplates []string `yaml:"image_templates"`
			Goos           string   `yaml:"goos"`
			Goarch         string   `yaml:"goarch"`
		} `yaml:"dockers"`
		DockerManifests []struct {
			NameTemplate string `yaml:"name_template"`
		} `yaml:"docker_manifests"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectName != "astrotui" || len(cfg.Builds) != 1 || cfg.Builds[0].Binary != "astrotui" {
		t.Fatalf("unexpected project/build config %#v", cfg)
	}
	assertContainsAll(t, cfg.Builds[0].Goos, []string{"linux", "darwin"})
	assertContainsAll(t, cfg.Builds[0].Goarch, []string{"amd64", "arm64"})
	assertContainsAll(t, cfg.Builds[0].Env, []string{"CGO_ENABLED=0"})
	if len(cfg.Archives) != 1 || cfg.Archives[0].NameTemplate != "astrotui_{{ .Os }}_{{ .Arch }}" {
		t.Fatalf("install.sh archive contract is not explicit: %#v", cfg.Archives)
	}
	if len(cfg.Signs) == 0 || cfg.Signs[0].Cmd != "cosign" {
		t.Fatalf("expected cosign signing config, got %#v", cfg.Signs)
	}
	if len(cfg.Dockers) != 2 || len(cfg.DockerManifests) != 1 || !strings.Contains(cfg.DockerManifests[0].NameTemplate, "ghcr.io/alphabravo-oss/astrotui") {
		t.Fatalf("unexpected docker config dockers=%#v manifests=%#v", cfg.Dockers, cfg.DockerManifests)
	}
}

func TestDockerfileRunsAsNonRoot(t *testing.T) {
	data, err := os.ReadFile("../../Dockerfile") // #nosec G304 -- test reads repository Dockerfile.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"FROM gcr.io/distroless/static-debian12:nonroot",
		`LABEL org.opencontainers.image.source="https://github.com/alphabravo-oss/astronomer-tui"`,
		`LABEL org.opencontainers.image.licenses="Apache-2.0"`,
		"USER nonroot:nonroot",
		`ENTRYPOINT ["/usr/local/bin/astrotui"]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, text)
		}
	}
}

func TestDockerignoreKeepsImageContextSmall(t *testing.T) {
	data, err := os.ReadFile("../../.dockerignore") // #nosec G304 -- test reads repository Docker build context policy.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"*", "!Dockerfile", "!astrotui"} {
		if !strings.Contains(text, want) {
			t.Fatalf(".dockerignore missing %q:\n%s", want, text)
		}
	}
}

func TestInstallScriptMatchesReleaseArtifacts(t *testing.T) {
	data, err := os.ReadFile("../../install.sh") // #nosec G304 -- test reads repository install script.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"alphabravo-oss/astronomer-tui",
		"astrotui_${OS}_${ARCH}.tar.gz",
		"install -m 0755",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.sh missing %q:\n%s", want, text)
		}
	}
}

func TestREADMEHasInstallQuickstartAndScreenshot(t *testing.T) {
	data, err := os.ReadFile("../../README.md") // #nosec G304 -- test reads repository README.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"curl -fsSL https://raw.githubusercontent.com/alphabravo-oss/astronomer-tui/main/install.sh | sh",
		"./bin/astrotui --context kind-kind",
		"ghcr.io/alphabravo-oss/astrotui:<version>",
		"docs/screenshots/astrotui-main.svg",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("README missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat("../../docs/screenshots/astrotui-main.svg"); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubReleaseWorkflowRunsGoReleaserWithSigningPrereqs(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml") // #nosec G304 -- test reads repository workflow config.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"tags:",
		"goreleaser/goreleaser-action",
		"sigstore/cosign-installer",
		"docker/login-action",
		"packages: write",
		"id-token: write",
		"release --clean",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("release workflow missing %q:\n%s", want, text)
		}
	}
}

func TestCIWorkflowRunsNFRGates(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml") // #nosec G304 -- test reads repository workflow config.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"make lint",
		"make test",
		"make envtest",
		"make build",
		"make perf",
		"make coverage-nfr",
		"goreleaser/goreleaser-action",
		"install-only: true",
		"make release-snapshot",
		"helm/kind-action",
		"make e2e",
		"make nfr-live",
		"ASTROTUI_E2E",
		"ASTROTUI_E2E_CONTEXT",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ci workflow missing %q:\n%s", want, text)
		}
	}
}

func TestMakefileRunsLiveNFRGate(t *testing.T) {
	data, err := os.ReadFile("../../Makefile") // #nosec G304 -- test reads repository Makefile.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"nfr-live:",
		"ASTROTUI_E2E_PROCESS_IDLE=5m",
		"TestHeadlessRenderProcessGoldenPath",
		"-timeout=8m",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Makefile missing %q:\n%s", want, text)
		}
	}
}

func TestMakefileRunsEnvtestWithManagedAssets(t *testing.T) {
	data, err := os.ReadFile("../../Makefile") // #nosec G304 -- test reads repository Makefile.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"envtest:",
		"KUBEBUILDER_ASSETS",
		"setup-envtest@v0.24.0",
		"go test ./test/envtest",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Makefile missing %q:\n%s", want, text)
		}
	}
}

func TestMakefileRunsNonInteractiveSnapshotRelease(t *testing.T) {
	data, err := os.ReadFile("../../Makefile") // #nosec G304 -- test reads repository Makefile.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"release-snapshot:",
		"goreleaser release --snapshot --clean",
		"--skip=sign,publish,docker",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Makefile missing %q:\n%s", want, text)
		}
	}
}

func TestNoTelemetryDependencies(t *testing.T) {
	data, err := os.ReadFile("../../go.mod") // #nosec G304 -- test reads repository module manifest.
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(data))
	for _, forbidden := range []string{
		"opentelemetry",
		"segmentio",
		"posthog",
		"sentry",
		"datadog",
		"newrelic",
		"bugsnag",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("go.mod contains telemetry dependency marker %q", forbidden)
		}
	}
}

func assertContainsAll(t *testing.T, got []string, want []string) {
	t.Helper()
	set := map[string]bool{}
	for _, item := range got {
		set[item] = true
	}
	for _, item := range want {
		if !set[item] {
			t.Fatalf("expected %q in %#v", item, got)
		}
	}
}

func TestReleaseCandidatesStayPrereleasesWithCompleteInstallables(t *testing.T) {
	data, err := os.ReadFile("../../.goreleaser.yml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Release struct {
			Prerelease string `yaml:"prerelease"`
			MakeLatest bool   `yaml:"make_latest"`
			ExtraFiles []struct {
				Glob string `yaml:"glob"`
			} `yaml:"extra_files"`
		} `yaml:"release"`
		Dockers []struct {
			Goarch string   `yaml:"goarch"`
			Flags  []string `yaml:"build_flag_templates"`
		} `yaml:"dockers"`
	}
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Release.Prerelease != "auto" || cfg.Release.MakeLatest {
		t.Fatal("RC tags must remain prereleases without replacing latest")
	}
	installer := false
	for _, f := range cfg.Release.ExtraFiles {
		installer = installer || f.Glob == "install.sh"
	}
	if !installer {
		t.Fatal("release installer asset missing")
	}
	for _, d := range cfg.Dockers {
		assertContainsAll(t, d.Flags, []string{"--platform=linux/" + d.Goarch})
	}
	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Export signed container installables", "container-checksums.txt.sig", "container-checksums.txt.pem", "docker save", "gh release upload"} {
		if !strings.Contains(string(workflow), want) {
			t.Fatalf("offline container installable contract missing %q", want)
		}
	}
}
