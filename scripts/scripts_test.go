package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStartScriptExecsServerWithConfiguredAndDefaultPaths(t *testing.T) {
	for name, configured := range map[string]string{
		"configured path": filepath.Join(t.TempDir(), "private config.yaml"),
		"default path":    "",
	} {
		t.Run(name, func(t *testing.T) {
			repo := scriptFixture(t, "start.sh")
			capture := filepath.Join(repo, "capture")
			writeExecutable(t, filepath.Join(repo, "dist", "confighub-server"), "#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" > \"$CAPTURE\"\n")
			command := exec.Command(filepath.Join(repo, "scripts", "start.sh"))
			command.Env = append(os.Environ(), "CAPTURE="+capture)
			if configured != "" {
				command.Env = append(command.Env, "CONFIGHUB_CONFIG="+configured)
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("start failed: %v output=%s", err, output)
			}
			got := readLines(t, capture)
			wantPath := configured
			if wantPath == "" {
				wantPath = filepath.Join(repo, "config", "config.yaml")
			}
			want := []string{"serve", "--config", wantPath}
			if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("args=%q want %q", got, want)
			}
		})
	}
}

func TestBackupScriptExecsTimestampedOneShotBackup(t *testing.T) {
	repo := scriptFixture(t, "backup.sh")
	bin := filepath.Join(repo, "test-bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(repo, "capture")
	writeExecutable(t, filepath.Join(repo, "dist", "confighub-server"), "#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" > \"$CAPTURE\"\n")
	writeExecutable(t, filepath.Join(bin, "date"), "#!/usr/bin/env bash\nprintf '20260829-123456\\n'\n")
	configured := filepath.Join(repo, "private config.yaml")
	command := exec.Command(filepath.Join(repo, "scripts", "backup.sh"))
	command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "CAPTURE="+capture, "CONFIGHUB_CONFIG="+configured)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("backup failed: %v output=%s", err, output)
	}
	want := []string{
		"backup", "--config", configured, "--output",
		filepath.Join(repo, "backups", "confighub-20260829-123456.db"),
	}
	if got := readLines(t, capture); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args=%q want %q", got, want)
	}
}

func TestBuildAndCheckScriptsDeclareCompleteNativeGates(t *testing.T) {
	repo := repositoryRoot(t)
	build := readFile(t, filepath.Join(repo, "scripts", "build.sh"))
	check := readFile(t, filepath.Join(repo, "scripts", "check.sh"))
	for name, contents := range map[string]string{"build.sh": build, "check.sh": check} {
		if strings.Count(contents, `"$script_dir/verify-toolchain.sh"`) != 1 {
			t.Errorf("%s must call the shared toolchain validator exactly once", name)
		}
		if strings.Contains(contents, "node_major=") || strings.Contains(contents, "go_version_output=") {
			t.Errorf("%s duplicates shared toolchain validation", name)
		}
	}
	if !strings.Contains(build, `npm ci --include=dev --prefix "$repo_root/web"`) {
		t.Error("build.sh must install locked frontend development dependencies even when NODE_ENV=production")
	}
	for _, required := range []string{
		"npm ci", "npm run typecheck", "npm test", "npm run build", "go test ./...",
		`go build -trimpath -ldflags "-X confighub.local/internal/buildinfo.Version=$build_version" -o "$repo_root/dist/confighub-server" ./cmd/server`,
		`go build -trimpath -ldflags "-X confighub.local/internal/buildinfo.Version=$build_version" -o "$repo_root/dist/confighub" ./cmd/cli`,
	} {
		if !strings.Contains(build, required) {
			t.Errorf("build.sh missing %q", required)
		}
	}
	if versionAt, buildAt := strings.Index(build, `git -C "$repo_root" describe --always --dirty`), strings.Index(build, "npm run build"); versionAt < 0 || buildAt < 0 || versionAt > buildAt {
		t.Error("build.sh must capture the checkout version before generated frontend output can dirty the tree")
	}
	ordered := []string{
		"gofmt -l", "go vet ./...", "go test -race -count=1 ./...",
		"npm run typecheck", "npm test", "npm run build",
		"go build", "npm run e2e", "go test ./internal/acceptance",
	}
	position := -1
	for _, required := range ordered {
		next := strings.Index(check, required)
		if next < 0 {
			t.Errorf("check.sh missing %q", required)
			continue
		}
		if next <= position {
			t.Errorf("check.sh command %q is out of order", required)
		}
		position = next
	}
}

func TestRuntimeIgnoresProtectSecretsDatabaseAndNestedBackupTemps(t *testing.T) {
	repo := repositoryRoot(t)
	for _, path := range []string{
		"config/config.yaml",
		"config/users.yaml",
		"config/session.key",
		"data/confighub.db",
		"data/confighub.db-wal",
		"data/confighub.db-shm",
		"data/confighub.db-journal",
		"archive/daily/.confighub-backup-operation.tmp",
		"archive/daily/.confighub-backup-operation.tmp-wal",
	} {
		command := exec.Command("git", "check-ignore", "--no-index", "--quiet", "--", path)
		command.Dir = repo
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("runtime artifact %q is not ignored: err=%v output=%s", path, err, output)
		}
	}
	for _, path := range []string{"config/config.example.yaml", "config/users.example.yaml"} {
		command := exec.Command("git", "check-ignore", "--no-index", "--quiet", "--", path)
		command.Dir = repo
		if err := command.Run(); err == nil {
			t.Errorf("tracked example %q is hidden by ignore rules", path)
		}
	}
}

func TestBuildScriptAcceptsSupportedToolchainBoundariesBeforeNPMInstall(t *testing.T) {
	for _, test := range []struct {
		name        string
		goVersion   string
		nodeVersion string
	}{
		{name: "Go 1.25 and Node 22 lower bound", goVersion: "go version go1.25.0 linux/amd64", nodeVersion: "v22.22.2"},
		{name: "Go patch and Node 24 lower bound", goVersion: "go version go1.25.12 linux/amd64", nodeVersion: "v24.15.0"},
		{name: "Node 26 lower bound", goVersion: "go version go1.25.1 linux/amd64", nodeVersion: "v26.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, capture, err := runGateWithFakeToolchain(t, "build.sh", test.goVersion, test.nodeVersion)
			if err == nil {
				t.Fatal("build unexpectedly continued past the intentionally failing fake npm")
			}
			if got := readFile(t, capture); !strings.HasPrefix(got, "npm ci --include=dev") {
				t.Fatalf("npm install not reached after supported versions: capture=%q output=%s", got, output)
			}
		})
	}
}

func TestBuildScriptRejectsUnsupportedToolchainsBeforeNPMInstall(t *testing.T) {
	for _, test := range []struct {
		name        string
		goVersion   string
		nodeVersion string
		wantMessage string
	}{
		{name: "older Go", goVersion: "go version go1.24.9 linux/amd64", nodeVersion: "v24.15.0", wantMessage: "Go 1.25.x"},
		{name: "newer Go line", goVersion: "go version go1.26.0 linux/amd64", nodeVersion: "v24.15.0", wantMessage: "Go 1.25.x"},
		{name: "malformed Go", goVersion: "go version devel unknown linux/amd64", nodeVersion: "v24.15.0", wantMessage: "Go 1.25.x"},
		{name: "Node 22 below patch floor", goVersion: "go version go1.25.0 linux/amd64", nodeVersion: "v22.22.1", wantMessage: "^22.22.2 || ^24.15.0 || >=26.0.0"},
		{name: "Node 23", goVersion: "go version go1.25.0 linux/amd64", nodeVersion: "v23.9.0", wantMessage: "^22.22.2 || ^24.15.0 || >=26.0.0"},
		{name: "Node 24 below minor floor", goVersion: "go version go1.25.0 linux/amd64", nodeVersion: "v24.14.9", wantMessage: "^22.22.2 || ^24.15.0 || >=26.0.0"},
		{name: "Node 25", goVersion: "go version go1.25.0 linux/amd64", nodeVersion: "v25.9.0", wantMessage: "^22.22.2 || ^24.15.0 || >=26.0.0"},
		{name: "malformed Node", goVersion: "go version go1.25.0 linux/amd64", nodeVersion: "v24.latest.0", wantMessage: "^22.22.2 || ^24.15.0 || >=26.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, capture, err := runGateWithFakeToolchain(t, "build.sh", test.goVersion, test.nodeVersion)
			if err == nil {
				t.Fatal("unsupported toolchain succeeded")
			}
			if !strings.Contains(output, test.wantMessage) {
				t.Fatalf("error does not state supported range: output=%s", output)
			}
			if contents, readErr := os.ReadFile(capture); readErr == nil && strings.Contains(string(contents), "npm ") {
				t.Fatalf("npm ran before toolchain rejection: %s", contents)
			} else if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
		})
	}
}

func TestCheckScriptAcceptsSupportedToolchainBoundariesBeforeGoWork(t *testing.T) {
	for _, test := range []struct {
		name        string
		goVersion   string
		nodeVersion string
	}{
		{name: "Node 22 lower bound", goVersion: "go version go1.25.0 linux/amd64", nodeVersion: "v22.22.2"},
		{name: "Node 24 lower bound", goVersion: "go version go1.25.12 linux/amd64", nodeVersion: "v24.15.0"},
		{name: "Node 26 lower bound", goVersion: "go version go1.25.1 linux/amd64", nodeVersion: "v26.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, capture, err := runGateWithFakeToolchain(t, "check.sh", test.goVersion, test.nodeVersion)
			if err == nil {
				t.Fatal("check unexpectedly continued past the intentionally failing fake go")
			}
			if got := readFile(t, capture); !strings.HasPrefix(got, "go vet ./...") {
				t.Fatalf("Go work not reached after supported versions: capture=%q output=%s", got, output)
			}
		})
	}
}

func TestCheckScriptRejectsUnsupportedToolchainsBeforeGoOrNPMWork(t *testing.T) {
	for _, test := range []struct {
		name        string
		goVersion   string
		nodeVersion string
		wantMessage string
	}{
		{name: "wrong Go", goVersion: "go version go1.26.0 linux/amd64", nodeVersion: "v24.15.0", wantMessage: "Go 1.25.x"},
		{name: "Node 25", goVersion: "go version go1.25.0 linux/amd64", nodeVersion: "v25.9.0", wantMessage: "^22.22.2 || ^24.15.0 || >=26.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, capture, err := runGateWithFakeToolchain(t, "check.sh", test.goVersion, test.nodeVersion)
			if err == nil {
				t.Fatal("unsupported toolchain succeeded")
			}
			if !strings.Contains(output, test.wantMessage) {
				t.Fatalf("error does not state supported range: output=%s", output)
			}
			if contents, readErr := os.ReadFile(capture); readErr == nil {
				t.Fatalf("Go/npm work ran before toolchain rejection: %s", contents)
			} else if !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
		})
	}
}

func scriptFixture(t *testing.T, name string) string {
	t.Helper()
	repo := t.TempDir()
	for _, dir := range []string{"scripts", "dist", "config", "backups"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	contents := readFile(t, filepath.Join(repositoryRoot(t), "scripts", name))
	writeExecutable(t, filepath.Join(repo, "scripts", name), contents)
	if name == "build.sh" || name == "check.sh" {
		validator := readFile(t, filepath.Join(repositoryRoot(t), "scripts", "verify-toolchain.sh"))
		writeExecutable(t, filepath.Join(repo, "scripts", "verify-toolchain.sh"), validator)
	}
	return repo
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate scripts test")
	}
	return filepath.Dir(filepath.Dir(file))
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(readFile(t, path), "\n"), "\n")
}

func runGateWithFakeToolchain(t *testing.T, scriptName, goVersion, nodeVersion string) (string, string, error) {
	t.Helper()
	repo := scriptFixture(t, scriptName)
	bin := filepath.Join(repo, "test-bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(repo, "capture")
	writeExecutable(t, filepath.Join(bin, "go"), `#!/usr/bin/env bash
if [[ "${1-}" == "version" ]]; then
  printf '%s\n' "$FAKE_GO_VERSION"
  exit 0
fi
printf 'go %s\n' "$*" >> "$CAPTURE"
exit 72
`)
	writeExecutable(t, filepath.Join(bin, "node"), `#!/usr/bin/env bash
printf '%s\n' "$FAKE_NODE_VERSION"
`)
	writeExecutable(t, filepath.Join(bin, "npm"), `#!/usr/bin/env bash
printf 'npm %s\n' "$*" >> "$CAPTURE"
exit 73
`)
	writeExecutable(t, filepath.Join(bin, "git"), `#!/usr/bin/env bash
printf '%s\n' 'test-describe'
`)
	command := exec.Command(filepath.Join(repo, "scripts", scriptName))
	command.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"CAPTURE="+capture,
		"FAKE_GO_VERSION="+goVersion,
		"FAKE_NODE_VERSION="+nodeVersion,
	)
	output, err := command.CombinedOutput()
	return string(output), capture, err
}
