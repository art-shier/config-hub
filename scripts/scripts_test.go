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
	check := readFile(t, filepath.Join(repo, "scripts", "check.sh"))
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
