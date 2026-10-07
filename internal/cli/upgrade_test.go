package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUpgradeHelpDoesNotNeedConnectionConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), []string{"upgrade", "--help"}, nil, &stdout, &stderr, func() (configSnapshot, error) {
		t.Fatal("upgrade must not read connection configuration")
		return configSnapshot{}, nil
	})
	if code != 0 || !strings.Contains(stderr.String(), "--check") || !strings.Contains(stderr.String(), "--version") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestUpgradeRejectsInvalidVersions(t *testing.T) {
	for _, version := range []string{"", "latest", "v1.2.3/../other", "v1.2.3?token=secret", "v01.2.3", "v1.2.3-rc1"} {
		var stdout, stderr bytes.Buffer
		code := Execute(context.Background(), []string{"upgrade", "--version", version}, nil, &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 {
			t.Fatalf("version=%q code=%d", version, code)
		}
	}
}

func TestUpgradeVersionChecksDoNotChangeFiles(t *testing.T) {
	for _, test := range []struct{ current, target, requested, message string }{
		{"v0.2.0", "v0.3.0", "", "update available"},
		{"v0.3.0", "v0.3.0", "", "already up to date"},
		{"v0.10.0", "v0.9.0", "", "already up to date"},
		{"v0.10.0", "v0.9.0", "v0.9.0", "update available"},
		{"dev", "v0.3.0", "", "update available"},
	} {
		t.Run(test.current+test.requested, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/releases/latest":
					http.Redirect(writer, request, "/releases/tag/"+test.target, http.StatusFound)
				case "/releases/tag/" + test.target:
					writer.WriteHeader(http.StatusOK)
				default:
					t.Error("version check attempted an asset download")
					writer.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			updater := defaultCLIUpgrader()
			updater.client, updater.releaseRoot = server.Client(), server.URL+"/releases"
			updater.currentVersion = test.current
			updater.operatingSystem, updater.architecture = "linux", "amd64"
			updater.executable = func() (string, error) { t.Fatal("version check accessed installation files"); return "", nil }
			message, err := updater.upgrade(context.Background(), test.requested, true)
			if err != nil || !strings.Contains(message, test.message) {
				t.Fatalf("message=%q err=%v", message, err)
			}
			if test.message == "already up to date" {
				if _, err := updater.upgrade(context.Background(), test.requested, false); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestUpgradePreservesOldExecutableUntilValidation(t *testing.T) {
	for _, scenario := range []string{"success", "checksum", "archive", "version", "download", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "confighub.exe")
			if err := os.WriteFile(target, []byte("old executable"), 0755); err != nil {
				t.Fatal(err)
			}
			base := "config-hub-cli_0.3.0_windows_amd64"
			entries := []upgradeTestEntry{{base + "/", "", true, false}, {base + "/confighub.exe", "new executable", false, false}}
			if scenario == "archive" {
				entries = append(entries, upgradeTestEntry{"../outside", "unexpected", false, false})
			}
			archive := upgradeTestArchive(t, true, entries)
			digest := sha256.Sum256(archive)
			manifest := fmt.Sprintf("%x  %s.zip\n", digest, base)
			if scenario == "checksum" {
				manifest = strings.Repeat("0", 64) + "  " + base + ".zip\n"
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Header.Get("Authorization") != "" {
					t.Error("upgrade sent credentials")
				}
				if scenario == "download" {
					writer.WriteHeader(http.StatusNotFound)
					return
				}
				switch request.URL.Path {
				case "/releases/download/v0.3.0/checksums.txt":
					fmt.Fprint(writer, manifest)
				case "/releases/download/v0.3.0/" + base + ".zip":
					writer.Write(archive)
				default:
					t.Error("unexpected release request")
					writer.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			updater := defaultCLIUpgrader()
			updater.client, updater.releaseRoot = server.Client(), server.URL+"/releases"
			updater.currentVersion = "v0.2.0"
			updater.operatingSystem, updater.architecture = "windows", "amd64"
			updater.executable = func() (string, error) { return target, nil }
			updater.verify = func(_ context.Context, staged, version string) error {
				data, _ := os.ReadFile(target)
				if string(data) != "old executable" {
					t.Error("old executable changed before verification")
				}
				data, _ = os.ReadFile(staged)
				if string(data) != "new executable" || version != "v0.3.0" {
					t.Error("incorrect staged executable")
				}
				if scenario == "version" {
					return errors.New("version mismatch")
				}
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			message, err := updater.upgrade(ctx, "v0.3.0", false)
			data, _ := os.ReadFile(target)
			if scenario == "success" {
				if err != nil || string(data) != "new executable" || !strings.Contains(message, "v0.2.0 -> v0.3.0") {
					t.Fatalf("message=%s err=%v data=%s", message, err, data)
				}
			} else if err == nil || string(data) != "old executable" {
				t.Fatalf("failed upgrade changed target: %v %s", err, data)
			}
			files, _ := os.ReadDir(directory)
			if len(files) != 1 {
				t.Fatalf("staging files remain: %v", files)
			}
		})
	}
}

type upgradeTestEntry struct {
	name, body      string
	directory, link bool
}

func upgradeTestArchive(t *testing.T, windows bool, entries []upgradeTestEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if windows {
		writer := zip.NewWriter(&buffer)
		for _, entry := range entries {
			header := &zip.FileHeader{Name: entry.name}
			mode := os.FileMode(0755)
			if entry.directory {
				mode |= os.ModeDir
			}
			if entry.link {
				mode |= os.ModeSymlink
			}
			header.SetMode(mode)
			file, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		compressed := gzip.NewWriter(&buffer)
		writer := tar.NewWriter(compressed)
		for _, entry := range entries {
			kind := byte(tar.TypeReg)
			if entry.directory {
				kind = tar.TypeDir
			}
			if entry.link {
				kind = tar.TypeSymlink
			}
			if err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0755, Typeflag: kind, Size: int64(len(entry.body))}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buffer.Bytes()
}

func TestUpgradeArchivesRejectUnexpectedEntries(t *testing.T) {
	for _, operatingSystem := range []string{"linux", "windows"} {
		base := "config-hub-cli_1.2.3_" + operatingSystem + "_amd64"
		name := base + "/confighub"
		if operatingSystem == "windows" {
			name += ".exe"
		}
		for _, scenario := range []string{"valid", "extra", "duplicate", "traversal", "symlink", "missing-directory"} {
			t.Run(operatingSystem+"/"+scenario, func(t *testing.T) {
				entries := []upgradeTestEntry{{base + "/", "", true, false}, {name, "binary", false, false}}
				switch scenario {
				case "extra":
					entries = append(entries, upgradeTestEntry{base + "/extra", "extra", false, false})
				case "duplicate":
					entries = append(entries, entries[1])
				case "traversal":
					entries[1].name = base + "/../confighub"
				case "symlink":
					entries[1].link, entries[1].body = true, ""
				case "missing-directory":
					entries = entries[1:]
				}
				binary, err := extractUpgradeArchive(upgradeTestArchive(t, operatingSystem == "windows", entries), base, operatingSystem)
				if scenario == "valid" {
					if err != nil || string(binary) != "binary" {
						t.Fatalf("data=%s err=%v", binary, err)
					}
				} else if err == nil {
					t.Fatal("accepted invalid archive")
				}
			})
		}
	}
}

func TestUpgradeVersionVerification(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "version.exe")
	command := exec.Command("go", "build", "-o", executable, "./testdata/upgradeversion")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, output)
	}
	if err := verifyUpgradeExecutable(context.Background(), executable, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := verifyUpgradeExecutable(context.Background(), executable, "v9.9.9"); err == nil {
		t.Fatal("accepted mismatching version")
	}
}

func TestUpgradeReplacesRunningExecutable(t *testing.T) {
	if os.Getenv("CONFIGHUB_UPGRADE_TEST_HELPER") == "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := replaceUpgradeExecutable(executable+".next", executable); err != nil {
			t.Fatal(err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "confighub-test.exe")
	if err := os.WriteFile(target, binary, 0755); err != nil {
		t.Fatal(err)
	}
	updated := append(binary, []byte("replacement marker")...)
	if err := os.WriteFile(target+".next", updated, 0755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(target, "-test.run=^TestUpgradeReplacesRunningExecutable$", "-test.count=1")
	command.Env = append(os.Environ(), "CONFIGHUB_UPGRADE_TEST_HELPER=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("running replacement on %s: %v %s", runtime.GOOS, err, output)
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, updated) {
		t.Fatalf("replacement failed: %v", err)
	}
}

func TestUpgradeDownloadAndRedirectLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { fmt.Fprint(writer, "too large") }))
	defer server.Close()
	updater := defaultCLIUpgrader()
	updater.client = server.Client()
	if _, err := updater.download(context.Background(), server.URL, 3); err == nil {
		t.Fatal("accepted oversized response")
	}
	for _, address := range []string{"http://github.com/file", "https://example.com/file", "https://github.com:444/file", "https://user:secret@github.com/file"} {
		request, _ := http.NewRequest(http.MethodGet, address, nil)
		if err := upgradeRedirect(request, nil); err == nil {
			t.Fatalf("accepted redirect %s", address)
		}
	}
	request, _ := http.NewRequest(http.MethodGet, "https://release-assets.githubusercontent.com/file", nil)
	if err := upgradeRedirect(request, nil); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeRejectsAmbiguousChecksumManifest(t *testing.T) {
	archive := []byte("archive")
	digest := sha256.Sum256(archive)
	line := fmt.Sprintf("%x  cli.zip\n", digest)
	for _, manifest := range []string{"", line + line, "invalid  cli.zip\n", strings.ReplaceAll(line, "cli.zip", "other.zip")} {
		if err := verifyUpgradeChecksum([]byte(manifest), archive, "cli.zip"); err == nil {
			t.Fatal("accepted ambiguous or missing checksum")
		}
	}
	if err := verifyUpgradeChecksum([]byte(line), archive, "cli.zip"); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeReplacementFailureRestoresExecutable(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "confighub.exe")
	if err := os.WriteFile(target, []byte("old executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := replaceUpgradeExecutable(filepath.Join(directory, "missing"), target); err == nil {
		t.Fatal("replacement should fail")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "old executable" {
		t.Fatalf("original executable not restored: %v", err)
	}
	files, _ := os.ReadDir(directory)
	if len(files) != 1 {
		t.Fatalf("backup files remain: %v", files)
	}
}
