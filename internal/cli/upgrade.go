package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"confighub.local/internal/buildinfo"
	"github.com/spf13/cobra"
)

const upgradeReleaseRoot = "https://github.com/art-shier/config-hub/releases"
const maxUpgradeArchiveBytes = 64 << 20
const maxUpgradeBinaryBytes = 128 << 20

var upgradeVersionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type upgradeFailure struct{ cause error }

func (failure *upgradeFailure) Error() string { return failure.cause.Error() }
func (failure *upgradeFailure) Unwrap() error { return failure.cause }

type cliUpgrader struct {
	client          *http.Client
	releaseRoot     string
	currentVersion  string
	operatingSystem string
	architecture    string
	executable      func() (string, error)
	verify          func(context.Context, string, string) error
}

func defaultCLIUpgrader() *cliUpgrader {
	return &cliUpgrader{
		client:          &http.Client{Timeout: 2 * time.Minute, CheckRedirect: upgradeRedirect},
		releaseRoot:     upgradeReleaseRoot,
		currentVersion:  buildinfo.Version,
		operatingSystem: runtime.GOOS,
		architecture:    runtime.GOARCH,
		executable:      os.Executable,
		verify:          verifyUpgradeExecutable,
	}
}

func newUpgradeCommand(stdout io.Writer, updater *cliUpgrader) *cobra.Command {
	var version string
	var check bool
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Update this CLI from the official GitHub Release",
		Long:  "Update the current CLI installation from GitHub. No ConfigHub server or token is required. Use --check to inspect the target version without changing files.",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if command.Flags().Changed("version") && !validUpgradeVersion(version) {
				return errLocalInput
			}
			message, err := updater.upgrade(command.Context(), version, check)
			if err != nil {
				return markMutationRuntime("upgrade", &upgradeFailure{cause: err})
			}
			return writeCLICommandOutput(stdout, message)
		},
	}
	command.Flags().BoolVar(&check, "check", false, "check the target version without installing")
	command.Flags().StringVar(&version, "version", "", "install a specific release (vMAJOR.MINOR.PATCH); default: latest stable")
	return command
}

func validUpgradeVersion(version string) bool {
	return len(version) <= 64 && upgradeVersionPattern.MatchString(version)
}

func upgradeRedirect(request *http.Request, previous []*http.Request) error {
	if len(previous) >= 10 {
		return errors.New("too many download redirects")
	}
	if request.URL.Scheme != "https" || request.URL.User != nil || (request.URL.Port() != "" && request.URL.Port() != "443") {
		return errors.New("unsafe download redirect")
	}
	switch request.URL.Hostname() {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return nil
	default:
		return errors.New("download redirected outside GitHub release hosts")
	}
}

func (updater *cliUpgrader) upgrade(ctx context.Context, requestedVersion string, check bool) (string, error) {
	extension := ".tar.gz"
	switch updater.operatingSystem + "/" + updater.architecture {
	case "linux/amd64", "linux/arm64", "darwin/arm64":
	case "windows/amd64":
		extension = ".zip"
	default:
		return "", errors.New("no official CLI release for this platform")
	}
	version := requestedVersion
	if version == "" {
		response, err := updater.request(ctx, updater.releaseRoot+"/latest")
		if err != nil {
			return "", err
		}
		response.Body.Close()
		prefix := updater.releaseRoot + "/tag/"
		finalURL := response.Request.URL.String()
		if !strings.HasPrefix(finalURL, prefix) {
			return "", errors.New("invalid latest release URL")
		}
		version = strings.TrimPrefix(finalURL, prefix)
	}
	if !validUpgradeVersion(version) {
		return "", errors.New("invalid release version")
	}
	if updater.currentVersion == version || (requestedVersion == "" && !upgradeIsNewer(version, updater.currentVersion)) {
		return fmt.Sprintf("already up to date: %s (target %s)\n", updater.currentVersion, version), nil
	}
	if check {
		if requestedVersion != "" {
			response, err := updater.request(ctx, updater.releaseRoot+"/tag/"+version)
			if err != nil {
				return "", err
			}
			response.Body.Close()
		}
		return fmt.Sprintf("update available: %s -> %s\n", updater.currentVersion, version), nil
	}
	executable, err := updater.executable()
	if err != nil {
		return "", errors.New("could not locate the current executable")
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("could not resolve the current executable: %w", err)
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("current executable must be a regular file")
	}
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return "", errors.New("cannot upgrade a setuid or setgid executable")
	}
	staged, err := os.CreateTemp(filepath.Dir(executable), ".confighub-upgrade-*.exe")
	if err != nil {
		return "", fmt.Errorf("installation directory must be writable (use an account with install permission): %w", err)
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	base := "config-hub-cli_" + strings.TrimPrefix(version, "v") + "_" + updater.operatingSystem + "_" + updater.architecture
	archiveName := base + extension
	releaseURL := updater.releaseRoot + "/download/" + version + "/"
	manifest, err := updater.download(ctx, releaseURL+"checksums.txt", 64<<10)
	if err != nil {
		return "", err
	}
	archive, err := updater.download(ctx, releaseURL+archiveName, maxUpgradeArchiveBytes)
	if err != nil {
		return "", err
	}
	if err := verifyUpgradeChecksum(manifest, archive, archiveName); err != nil {
		return "", err
	}
	binary, err := extractUpgradeArchive(archive, base, updater.operatingSystem)
	if err != nil {
		return "", err
	}
	if _, err := staged.Write(binary); err != nil {
		return "", fmt.Errorf("could not stage the executable: %w", err)
	}
	if err := staged.Chmod(info.Mode().Perm()); err != nil {
		return "", err
	}
	if err := staged.Sync(); err != nil {
		return "", err
	}
	if err := staged.Close(); err != nil {
		return "", err
	}
	if err := updater.verify(ctx, staged.Name(), version); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	backup, err := replaceUpgradeExecutable(staged.Name(), executable)
	if err != nil {
		return "", fmt.Errorf("could not replace the executable: %w", err)
	}
	message := fmt.Sprintf("upgraded %s -> %s at %s\n", updater.currentVersion, version, executable)
	if backup != "" {
		message += "previous executable retained at " + backup + "; remove it after this command exits\n"
	}
	return message, nil
}

func upgradeIsNewer(target, current string) bool {
	if !validUpgradeVersion(current) {
		return true
	}
	targetParts := strings.Split(target[1:], ".")
	currentParts := strings.Split(current[1:], ".")
	for index := range targetParts {
		if len(targetParts[index]) != len(currentParts[index]) {
			return len(targetParts[index]) > len(currentParts[index])
		}
		if targetParts[index] != currentParts[index] {
			return targetParts[index] > currentParts[index]
		}
	}
	return false
}

func (updater *cliUpgrader) request(ctx context.Context, address string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("could not create upgrade request")
	}
	response, err := updater.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not download from GitHub; check network access and retry")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("GitHub download returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func (updater *cliUpgrader) download(ctx context.Context, address string, limit int64) ([]byte, error) {
	response, err := updater.request(ctx, address)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, errors.New("could not read release download")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("release download exceeds size limit")
	}
	return data, nil
}

func verifyUpgradeChecksum(manifest, archive []byte, name string) error {
	var expected string
	count := 0
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			expected = fields[0]
			count++
		}
	}
	digest, err := hex.DecodeString(expected)
	actual := sha256.Sum256(archive)
	if count != 1 || err != nil || len(digest) != sha256.Size || !bytes.Equal(digest, actual[:]) {
		return errors.New("release archive checksum verification failed")
	}
	return nil
}

func verifyUpgradeExecutable(ctx context.Context, executable, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "version")
	var output upgradeVersionOutput
	command.Stdout = &output
	if err := command.Run(); err != nil || strings.TrimSpace(output.String()) != version {
		return errors.New("downloaded CLI did not report the expected version")
	}
	return nil
}

type upgradeVersionOutput struct{ buffer bytes.Buffer }

func (output *upgradeVersionOutput) String() string { return output.buffer.String() }

func (output *upgradeVersionOutput) Write(data []byte) (int, error) {
	if output.buffer.Len()+len(data) > 4096 {
		return 0, errors.New("version output exceeds limit")
	}
	return output.buffer.Write(data)
}
