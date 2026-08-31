# ConfigHub GitHub Release and Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish independently installable ConfigHub Server+Web and CLI GitHub Release assets for Linux amd64/arm64, with separate verified installers and an idempotent systemd Server deployment path.

**Architecture:** GitHub Actions protects `main` and turns strict semantic-version tags into four immutable tarballs plus one checksum manifest. Self-contained Bash entrypoints resolve a requested or latest GitHub Release, verify and safely extract only their product, then either atomically install the CLI or provision/upgrade the Server under a dedicated systemd account while preserving configuration and SQLite state.

**Tech Stack:** Go 1.25, React/Vite embedded assets, Bash 5, GitHub Actions, GitHub CLI, GNU tar/gzip/coreutils, systemd, SQLite, shellcheck, actionlint.

**Spec:** `docs/superpowers/specs/2026-08-31-github-release-deployment-design.md`

## Global Constraints

- Work directly on `main`; do not create a worktree or feature branch.
- Do not use subagents in this session; execute inline with `superpowers:executing-plans`.
- Release tags are exactly `vMAJOR.MINOR.PATCH`; release filenames remove only the leading `v`.
- Supported release targets are exactly `linux/amd64` and `linux/arm64`, built with `CGO_ENABLED=0`.
- Server and embedded Web are one product; CLI is a separate product. Never create a standalone Web archive.
- A Release contains four `.tar.gz` files and one `checksums.txt` file.
- Installers trust only fixed `https://github.com/art-shier/config-hub` release URLs and verify the selected archive against an exact SHA-256 manifest entry.
- Server deployment owns only `/usr/local/bin/confighub-server`, `/usr/local/lib/confighub`, `/etc/confighub`, `/var/lib/confighub`, `/var/backups/confighub`, and `/etc/systemd/system/confighub.service`.
- Never overwrite existing Server configuration, users, session key, SQLite files, or backups.
- Upgrade backup must succeed before any installed Server binary or unit changes.
- Follow red-green-refactor for Go and Bash behavior. Run each focused test red, implement the minimum, rerun green, then commit the task.
- Preserve the user's unrelated untracked `.coder-studio/` directory.

---

## Locked File Map

### Version surface

- `internal/cli/root.go`: adds the unauthenticated `confighub version` command using `buildinfo.Version`.
- `internal/cli/root_test.go`: tests version output and invalid extra arguments.
- `cmd/server/main.go`: adds stdout-aware command execution and `confighub-server version` without changing existing serve/backup test interfaces.
- `cmd/server/main_test.go`: tests Server version output and usage.

### Release packaging

- `deploy/systemd/confighub.service`: hardened, foreground systemd service template shipped only in Server archives.
- `scripts/package-release.sh`: validates the source tag/worktree, builds Web+Go targets, creates product archives, and writes checksums.
- `scripts/tests/testlib.sh`: minimal assertion and fixture helpers shared only by Bash tests.
- `scripts/tests/package_release_test.sh`: tests tag/asset naming, unit contract, and package helper behavior.

### Independent installers

- `scripts/install-cli.sh`: self-contained latest/pinned CLI downloader, verifier, safe extractor, and atomic installer.
- `scripts/tests/install_cli_test.sh`: tests platform mapping, version resolution boundary, checksum/layout rejection, and successful atomic installation.
- `scripts/deploy-server.sh`: self-contained first-install and upgrade orchestration for Server+Web.
- `scripts/tests/deploy_server_test.sh`: tests validation, generated runtime files, path/permission intent, preservation, backup gating, previous binary retention, and health failures under a temporary root.
- `scripts/tests/run.sh`: runs all Bash contract and behavior tests in a stable order.

### Automation and documentation

- `.github/workflows/ci.yml`: pull request and `main` quality workflow with read-only contents permission.
- `.github/workflows/release.yml`: tag-only quality, packaging, draft Release upload, asset verification, and publication.
- `scripts/tests/workflow_contract_test.sh`: tests workflow triggers, permissions, toolchain pins, validation, and expected asset publication contract.
- `.gitattributes`: keeps Bash and workflow files LF-normalized.
- `scripts/check.sh`: includes the new Bash test suite before browser/runtime acceptance.
- `README.md`: documents product split, release flow, independent install/upgrade, operations, and conservative uninstall.

---

### Task 1: Add explicit version commands

**Files:**
- Create: `internal/cli/root_test.go`
- Modify: `internal/cli/root.go`
- Modify: `cmd/server/main_test.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: mutable `internal/buildinfo.Version`, already populated by `-ldflags -X`.
- Produces: `confighub version` and `confighub-server version`, each writing exactly `<build-version>\n` to stdout with exit code `0`.
- Produces: `runCommandWithIO(ctx context.Context, args []string, stdout, stderr io.Writer) int`; existing `runCommand(ctx, args, stderr)` remains a compatibility wrapper for current tests.

- [ ] **Step 1: Write failing CLI version tests**

Create `internal/cli/root_test.go` with a test that restores global state:

```go
package cli

import (
	"bytes"
	"context"
	"testing"

	"confighub.local/internal/buildinfo"
)

func TestExecuteVersionWritesBuildVersionWithoutCredentials(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "v1.2.3"
	t.Cleanup(func() { buildinfo.Version = original })
	var stdout, stderr bytes.Buffer

	code := Execute(context.Background(), []string{"version"}, nil, &stdout, &stderr)

	if code != 0 || stdout.String() != "v1.2.3\n" || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExecuteVersionRejectsArgumentsWithoutWritingVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"version", "extra"}, nil, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
```

- [ ] **Step 2: Run the CLI tests and confirm the command is missing**

Run: `go test ./internal/cli -run 'TestExecuteVersion' -count=1`

Expected: FAIL because `version` is not registered and the current command returns usage exit code `2`.

- [ ] **Step 3: Implement the minimal CLI command**

Import `confighub.local/internal/buildinfo` in `internal/cli/root.go` and add this command before returning the root:

```go
version := &cobra.Command{
	Use:   "version",
	Short: "Print the ConfigHub CLI version",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		_, err := fmt.Fprintln(stdout, buildinfo.Version)
		return err
	},
}
root.AddCommand(version)
```

Map a version-output write failure to the existing runtime/output diagnostic category instead of silently succeeding; extend the test with a failing writer before considering the task green.

- [ ] **Step 4: Run focused and package CLI tests**

Run:

```bash
go test ./internal/cli -run 'TestExecuteVersion' -count=1
go test ./internal/cli -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing Server version tests**

Add to `cmd/server/main_test.go`:

```go
func TestVersionCommandWritesBuildVersionToStdout(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "v1.2.3"
	t.Cleanup(func() { buildinfo.Version = original })
	var stdout, stderr bytes.Buffer
	code := runCommandWithIO(context.Background(), []string{"version"}, &stdout, &stderr)
	if code != 0 || stdout.String() != "v1.2.3\n" || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestVersionCommandRejectsArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCommandWithIO(context.Background(), []string{"version", "extra"}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "confighub-server version") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
```

Add `confighub.local/internal/buildinfo` to the test imports.

- [ ] **Step 6: Run the Server tests and confirm the IO entrypoint is missing**

Run: `go test ./cmd/server -run 'TestVersionCommand' -count=1`

Expected: FAIL to compile because `runCommandWithIO` does not exist.

- [ ] **Step 7: Implement stdout-aware Server command dispatch**

Make `main` call `runCommandWithIO(..., os.Stdout, os.Stderr)`. Keep the existing helper as:

```go
func runCommand(ctx context.Context, args []string, stderr io.Writer) int {
	return runCommandWithIO(ctx, args, io.Discard, stderr)
}
```

In `runCommandWithIO`, normalize nil writers to `io.Discard`, accept only zero arguments for `version`, write `buildinfo.Version` to stdout, and map write failure to exit `1` with the redacted message `confighub-server: version output failed`. Add `confighub-server version` to usage.

- [ ] **Step 8: Run all Go tests and commit**

Run:

```bash
gofmt -w internal/cli/root.go internal/cli/root_test.go cmd/server/main.go cmd/server/main_test.go
go test ./... -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/cli/root.go internal/cli/root_test.go cmd/server/main.go cmd/server/main_test.go
git commit -m "feat: expose release version commands"
```

---

### Task 2: Define the Server unit and deterministic release packaging

**Files:**
- Create: `deploy/systemd/confighub.service`
- Create: `scripts/package-release.sh`
- Create: `scripts/tests/testlib.sh`
- Create: `scripts/tests/package_release_test.sh`

**Interfaces:**
- Consumes: exact tag argument `scripts/package-release.sh vMAJOR.MINOR.PATCH` and a clean checkout whose `HEAD` has that tag.
- Produces: `dist/release/{config-hub-server_VERSION_linux_ARCH.tar.gz,config-hub-cli_VERSION_linux_ARCH.tar.gz,checksums.txt}`.
- Produces shell functions `validate_release_tag`, `release_version`, `release_archive_base`, and `verify_release_source`, which remain source-testable because `main` is guarded.

- [ ] **Step 1: Create the Bash test harness and failing package contract tests**

`scripts/tests/testlib.sh` must provide `fail`, `assert_eq`, `assert_file`, `assert_not_file`, `assert_contains`, and `assert_fails` without external test frameworks. `scripts/tests/package_release_test.sh` sources the missing production script and checks:

```bash
assert_eq "1.2.3" "$(release_version v1.2.3)"
assert_eq "config-hub-server_1.2.3_linux_arm64" \
  "$(release_archive_base server v1.2.3 arm64)"
assert_eq "config-hub-cli_1.2.3_linux_amd64" \
  "$(release_archive_base cli v1.2.3 amd64)"
assert_fails validate_release_tag v1.2
assert_fails validate_release_tag 1.2.3
assert_fails validate_release_tag v1.2.3-rc.1
```

The same test asserts the unit contains the exact stable directives:

```text
User=confighub
Group=confighub
UMask=0077
ExecStart=/usr/local/bin/confighub-server serve --config /etc/confighub/config.yaml
ExecReload=/bin/kill -HUP $MAINPID
ReadWritePaths=/var/lib/confighub /var/backups/confighub
ReadOnlyPaths=/etc/confighub
```

- [ ] **Step 2: Run the package tests and confirm missing files fail**

Run: `bash scripts/tests/package_release_test.sh`

Expected: FAIL because `scripts/package-release.sh` and the unit do not exist.

- [ ] **Step 3: Implement the systemd unit**

Use `[Unit]`, `[Service]`, and `[Install]` sections. The service is foreground `Type=simple`, starts after `network.target`, restarts only on failure, uses a five-second restart delay, writes to journal, and includes these compatible hardening directives:

```ini
NoNewPrivileges=true
PrivateDevices=true
PrivateTmp=true
ProtectControlGroups=true
ProtectHome=true
ProtectKernelModules=true
ProtectKernelTunables=true
ProtectSystem=strict
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictSUIDSGID=true
```

Set `WantedBy=multi-user.target`. Do not add TLS, socket activation, dynamic users, or writable paths outside the two runtime directories.

- [ ] **Step 4: Implement release validation and packaging helpers**

`scripts/package-release.sh` starts with `set -euo pipefail`, `umask 022`, computes an absolute `repo_root`, and uses:

```bash
release_tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
architectures=(amd64 arm64)
products=(server cli)
```

`verify_release_source` requires Linux, the strict tag, `git tag --points-at HEAD` containing exactly the requested tag, and empty `git status --porcelain`. `release_archive_base` emits only the two locked product names and rejects unknown products/architectures.

- [ ] **Step 5: Implement deterministic cross-build and archive assembly**

The implementation must:

```bash
"$script_dir/verify-toolchain.sh"
npm ci --include=dev --prefix "$repo_root/web"
npm run typecheck --prefix "$repo_root/web"
npm test --prefix "$repo_root/web"
npm run build --prefix "$repo_root/web"
go test ./...
```

For each architecture, compile with:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -buildvcs=false \
  -ldflags "-X confighub.local/internal/buildinfo.Version=$tag" \
  -o "$staging/server/confighub-server" ./cmd/server
```

and the corresponding CLI command. Copy only the locked Server templates/unit, set binary mode `0755` and ordinary file mode `0644`, then create archives with stable path order, source commit timestamp, numeric owner/group zero, and `gzip -n`. Generate `checksums.txt` with `sha256sum` sorted by filename. Assemble everything under `mktemp -d`; move into a newly prepared, validated `dist/release` only after all four archives exist.

- [ ] **Step 6: Verify green package helper tests and shell syntax**

Run:

```bash
bash -n scripts/package-release.sh scripts/tests/testlib.sh scripts/tests/package_release_test.sh
bash scripts/tests/package_release_test.sh
```

Expected: PASS.

- [ ] **Step 7: Commit release packaging**

```bash
git add deploy/systemd/confighub.service scripts/package-release.sh scripts/tests/testlib.sh scripts/tests/package_release_test.sh
git commit -m "feat: package independent release products"
```

---

### Task 3: Build the independent CLI installer

**Files:**
- Create: `scripts/install-cli.sh`
- Create: `scripts/tests/install_cli_test.sh`

**Interfaces:**
- Consumes: `install-cli.sh [--version vMAJOR.MINOR.PATCH] [--install-dir ABSOLUTE_DIRECTORY]`.
- Produces: one atomic executable at `INSTALL_DIR/confighub`; no other durable state.
- Source-testable functions: `validate_release_tag`, `detect_linux_arch`, `resolve_latest_tag`, `verify_download_checksum`, `validate_cli_archive`, and `install_cli_release`.

- [ ] **Step 1: Write failing platform and validation tests**

Source `scripts/install-cli.sh` from the test and assert:

```bash
assert_eq "amd64" "$(detect_linux_arch Linux x86_64)"
assert_eq "amd64" "$(detect_linux_arch Linux amd64)"
assert_eq "arm64" "$(detect_linux_arch Linux aarch64)"
assert_eq "arm64" "$(detect_linux_arch Linux arm64)"
assert_fails detect_linux_arch Darwin arm64
assert_fails detect_linux_arch Linux riscv64
assert_fails validate_release_tag v1.2.3-rc1
```

Also test argument parsing rejects relative install directories and unknown flags before calling download functions.

- [ ] **Step 2: Write failing end-to-end fixture tests**

Create a local fixture archive whose `confighub` is an executable Bash program that prints `v1.2.3` for `version`. Generate its real SHA-256 manifest. After sourcing the installer, override only `download_file` to copy the matching fixture basename, then call:

```bash
install_cli_release v1.2.3 "$install_dir"
```

Assert the installed file is regular, executable, prints the version, and replaces an existing target only after validation. Add cases for a wrong hash, missing manifest entry, duplicate manifest entry, extra archive member, parent traversal member, and symlink binary; each must fail while preserving an existing target byte-for-byte.

- [ ] **Step 3: Run tests and confirm the installer is missing**

Run: `bash scripts/tests/install_cli_test.sh`

Expected: FAIL because `scripts/install-cli.sh` does not exist.

- [ ] **Step 4: Implement self-contained release discovery and download**

Use fixed constants:

```bash
github_repository='art-shier/config-hub'
github_web_root="https://github.com/$github_repository"
```

Latest resolution follows `releases/latest`, captures only curl's final effective HTTPS URL, extracts the final path segment, and re-validates the strict tag. Pinned versions never call latest resolution. `download_file` uses `curl --fail --silent --show-error --location --proto '=https' --tlsv1.2` and never accepts an arbitrary host.

- [ ] **Step 5: Implement exact checksum and safe archive validation**

Require exactly one two-column manifest line whose filename equals the selected archive, whose digest is 64 hexadecimal characters, and whose locally computed SHA-256 matches case-insensitively. Before extraction, list archive names and verbose types; accept only:

```text
config-hub-cli_VERSION_linux_ARCH/
config-hub-cli_VERSION_linux_ARCH/confighub
```

Reject absolute paths, path traversal, duplicate names, links, devices, and extra entries. Extract with owner/permission restoration disabled, then require the binary to be a regular non-symlink file.

- [ ] **Step 6: Implement atomic installation and executable version verification**

Create a `mktemp -d` workspace under `umask 077`. Run the extracted native binary's `version` command before touching the target. Install to `INSTALL_DIR/.confighub.install.$$` with mode `0755`, verify the temporary target, then use same-directory `mv` for atomic replacement and verify again. Trap removes temporary download and same-directory staging files without recursively deleting the install directory.

Use a main guard compatible with both `bash scripts/install-cli.sh` and `curl ... | bash` while allowing tests to source the file without executing it.

- [ ] **Step 7: Run green tests and commit**

Run:

```bash
bash -n scripts/install-cli.sh scripts/tests/install_cli_test.sh
bash scripts/tests/install_cli_test.sh
```

Expected: PASS.

Commit:

```bash
git add scripts/install-cli.sh scripts/tests/install_cli_test.sh
git commit -m "feat: install CLI from GitHub releases"
```

---

### Task 4: Build the independent Server deployment and upgrade script

**Files:**
- Create: `scripts/deploy-server.sh`
- Create: `scripts/tests/deploy_server_test.sh`

**Interfaces:**
- Consumes: `deploy-server.sh [--version TAG] [--public-url ORIGIN] [--admin-username USER] [--admin-password-file FILE]`.
- Produces: fixed-path systemd installation described in Global Constraints.
- Source-testable boundaries: `validate_public_origin`, `validate_admin_username`, `read_password_file`, `yaml_double_quote`, `write_initial_runtime_files`, `install_first_server`, `backup_before_upgrade`, `upgrade_server`, and wrapper functions for account ownership/systemd/readiness.

- [ ] **Step 1: Write failing input and secret-boundary tests**

Test exact accepted examples `https://config.example.com`, `https://config.example.com:8443`, and `https://[2001:db8::1]:8443`. Reject HTTP, credentials, path/query/fragment, whitespace, port zero, and port above 65535. Match the application's username pattern `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`.

Create password fixtures and assert `read_password_file` accepts one non-empty line from a regular `0600` file, trims only one LF or CRLF terminator, rejects `0640/0644`, symlinks, empty content, embedded newlines/control characters, and files larger than 4096 bytes. Verify YAML quoting preserves a password containing spaces, colon, hash, quotes, and backslash without creating another YAML field.

- [ ] **Step 2: Write failing first-install filesystem tests**

Source the deployment script, set its internal `deployment_root` to a test temporary directory, and override only these side-effect wrappers:

```bash
ensure_service_account() { :; }
set_confighub_owner() { :; }
systemctl_run() { printf '%s\n' "$*" >>"$systemctl_log"; }
wait_for_readiness() { return 0; }
```

Provide a verified extracted Server fixture and call `install_first_server`. Assert exact paths exist beneath the temporary root; configuration uses loopback, the provided HTTPS origin, absolute production paths, and local proxy CIDRs; users contain one enabled admin; the session key is at least 32 bytes; directories are `0700`; config/users/key are `0600`; binary is `0755`; and systemd enable/start calls were requested. Seed any one protected configuration path and assert first install fails without overwriting it.

- [ ] **Step 3: Write failing upgrade safety tests**

Create a complete managed v1 installation under the temporary root. Override `run_online_backup` to fail and assert `upgrade_server` returns non-zero without changing the old binary or unit. In the success case, make the backup wrapper create the requested backup, install v2, assert config/users/key/data bytes remain identical, and assert the previous binary is retained at `/usr/local/lib/confighub/confighub-server.previous`.

Override readiness to fail after restart and assert the script returns non-zero, keeps the new and previous binaries plus backup, and reports `journalctl -u confighub.service` and all recovery-relevant paths without printing password contents. Re-running the exact installed version with successful readiness must not back up, recreate credentials, or replace data.

- [ ] **Step 4: Run tests and confirm the deployment script is missing**

Run: `bash scripts/tests/deploy_server_test.sh`

Expected: FAIL because `scripts/deploy-server.sh` does not exist.

- [ ] **Step 5: Implement validation, download, and archive safety**

Duplicate the small release-validation/download boundary deliberately so the Server script remains self-contained. Server archive validation accepts exactly the top-level directory plus:

```text
confighub-server
config/config.example.yaml
config/users.example.yaml
deploy/confighub.service
```

Only the binary may be executable. Verify the extracted native Server reports the requested tag before installation.

- [ ] **Step 6: Implement first installation**

Production `main` requires effective UID zero and a running systemd environment before download. It requires `--public-url` only when no complete managed installation exists. If some but not all protected managed paths already exist, fail closed with an explicit manual-recovery message.

Create `confighub` with `useradd --system --home-dir /var/lib/confighub --shell /usr/sbin/nologin --user-group`; verify an existing account/group pair instead of recreating it. Write runtime files to same-directory temporary names and rename only after all render/permission steps succeed. Generated `config.yaml` uses:

```yaml
server:
  listen: 127.0.0.1:8080
  public_url: "<escaped origin>"
  trusted_proxy_cidrs:
    - 127.0.0.1/32
    - ::1/128
database:
  path: /var/lib/confighub/confighub.db
auth:
  users_file: /etc/confighub/users.yaml
  session_key_file: /etc/confighub/session.key
  session_ttl: 24h
backup:
  directory: /var/backups/confighub
```

Interactive password input reads twice from `/dev/tty` with echo disabled and restores terminal echo through a trap. Noninteractive operation requires `--admin-password-file`. Never print the password. Generate the session key with `openssl rand -base64 48`, set owner/modes, install the binary/unit atomically, daemon-reload, enable/start, and poll `http://127.0.0.1:8080/api/v1/health/ready` for at most 30 one-second attempts.

- [ ] **Step 7: Implement guarded upgrades**

Compare the installed `version` output to the target. Same-version + ready is a no-op. For a new version, invoke the currently installed binary as `confighub` through `runuser`:

```bash
/usr/local/bin/confighub-server backup \
  --config /etc/confighub/config.yaml \
  --output /var/backups/confighub/confighub-pre-upgrade-UTC_TIMESTAMP.db
```

Require the backup to exist, be regular, and be mode `0600`. Only then copy the old binary to `/usr/local/lib/confighub/confighub-server.previous`, install the new binary/unit atomically, daemon-reload, restart, and check readiness. Do not automatically restore binaries or SQLite after a failed new-version start; print exact retained paths and diagnostics.

- [ ] **Step 8: Run green deployment tests and commit**

Run:

```bash
bash -n scripts/deploy-server.sh scripts/tests/deploy_server_test.sh
bash scripts/tests/deploy_server_test.sh
```

Expected: PASS with no access to real `/etc`, system users, or systemd.

Commit:

```bash
git add scripts/deploy-server.sh scripts/tests/deploy_server_test.sh
git commit -m "feat: deploy Server from GitHub releases"
```

---

### Task 5: Add CI and atomic GitHub Release publication

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/release.yml`
- Create: `scripts/tests/workflow_contract_test.sh`

**Interfaces:**
- CI consumes pull requests and `main` pushes, with `contents: read` only.
- Release consumes strict version-tag pushes and publishes the five locked assets only after quality and package checks.

- [ ] **Step 1: Write failing workflow contract tests**

The Bash test requires both YAML files and asserts:

- CI has `pull_request`, `push` limited to `main`, `permissions: contents: read`, Go `1.25.x`, Node `24.15.0`, Playwright Chromium setup, shellcheck, actionlint, and `./scripts/check.sh`;
- Release has tag pattern `v*`, no branch trigger, explicit read permission plus job-scoped `contents: write`, checkout `fetch-depth: 0`, strict tag validation, `./scripts/check.sh`, `./scripts/package-release.sh`, exact five-asset verification, draft creation, upload, remote asset verification, and final draft publication;
- no workflow mentions Docker, a separate Web archive, or an unsupported architecture.

- [ ] **Step 2: Run the contract and confirm workflows are missing**

Run: `bash scripts/tests/workflow_contract_test.sh`

Expected: FAIL because `.github/workflows` does not exist.

- [ ] **Step 3: Implement `.github/workflows/ci.yml`**

Use `ubuntu-24.04`, `actions/checkout@v4`, `actions/setup-go@v5` with `go-version: 1.25.x`, and `actions/setup-node@v4` with `node-version: 24.15.0` plus npm cache keyed by `web/package-lock.json`. Run:

```bash
npm ci --include=dev --prefix web
npx --prefix web playwright install --with-deps chromium
sudo apt-get update
sudo apt-get install --yes shellcheck
shellcheck scripts/*.sh scripts/tests/*.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
./scripts/check.sh
```

Set a bounded job timeout and concurrency that cancels superseded runs for the same ref.

- [ ] **Step 4: Implement `.github/workflows/release.yml`**

Use the same runner/toolchain/lint/quality setup and `fetch-depth: 0`. Validate `GITHUB_REF_NAME` with Bash's strict regex before packaging. After `scripts/package-release.sh "$GITHUB_REF_NAME"`, compare sorted basenames in `dist/release` to the exact expected list computed from the version.

Publication is one `gh` step with `GH_TOKEN: ${{ github.token }}`:

1. refuse if `gh release view "$tag"` already succeeds;
2. create a draft Release with `--verify-tag --generate-notes --title "$tag"`;
3. install an `EXIT` trap that deletes only the draft created by this run if upload/verification fails;
4. upload the five explicit paths without clobber;
5. query `gh release view --json assets,isDraft`, compare exact sorted remote asset names, and require draft state;
6. publish with `gh release edit "$tag" --draft=false`, then disable cleanup.

- [ ] **Step 5: Run workflow contract and syntax validation**

Run:

```bash
bash scripts/tests/workflow_contract_test.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
```

Expected: PASS.

- [ ] **Step 6: Commit automation**

```bash
git add .github/workflows/ci.yml .github/workflows/release.yml scripts/tests/workflow_contract_test.sh
git commit -m "ci: publish verified GitHub releases"
```

---

### Task 6: Integrate checks and document operator workflows

**Files:**
- Create: `.gitattributes`
- Create: `scripts/tests/run.sh`
- Modify: `scripts/check.sh`
- Modify: `README.md`

**Interfaces:**
- `scripts/tests/run.sh` executes each `*_test.sh` except itself and returns non-zero on the first failure.
- `scripts/check.sh` runs Bash tests before expensive E2E/runtime acceptance.
- README exposes separate Server and CLI commands without implying that Server installs the CLI.

- [ ] **Step 1: Add the failing suite runner**

Create `scripts/tests/run.sh` with `set -euo pipefail`, resolve its own directory, and invoke in this fixed order:

```text
package_release_test.sh
install_cli_test.sh
deploy_server_test.sh
workflow_contract_test.sh
```

Run: `bash scripts/tests/run.sh`

Expected: PASS only after Tasks 2-5 are complete.

- [ ] **Step 2: Integrate the suite into `scripts/check.sh`**

Immediately after `go test -race -count=1 ./...`, add:

```bash
"$script_dir/tests/run.sh"
```

Keep all existing frontend, binary, Playwright, and runtime acceptance commands unchanged.

- [ ] **Step 3: Add LF normalization**

Create `.gitattributes`:

```gitattributes
*.sh text eol=lf
*.yml text eol=lf
*.yaml text eol=lf
```

Do not mechanically rewrite unrelated existing YAML files.

- [ ] **Step 4: Rewrite deployment-facing README sections**

Update the opening statement that currently says systemd is not provided. Add concise sections with exact commands for:

- Release asset split and supported targets;
- latest and `--version v1.2.3` CLI installation;
- Server script download/review plus `sudo bash deploy-server.sh --public-url https://config.example.com`;
- noninteractive Server deployment using `--admin-password-file` with `0600` permissions;
- same-script Server upgrade and upgrade backup behavior;
- `systemctl status/restart/reload`, journal, liveness/readiness, external reverse proxy, and remote trusted proxy edits;
- publisher flow `git tag vX.Y.Z && git push origin vX.Y.Z` only after `main` checks pass;
- CLI removal and conservative Server removal that leaves `/etc/confighub`, `/var/lib/confighub`, and `/var/backups/confighub` intact by default.

Never recommend direct public exposure, NFS SQLite, shared database access, plaintext password flags, automatic database rollback, or deleting data as part of uninstall.

- [ ] **Step 5: Run focused checks and commit**

Run:

```bash
bash scripts/tests/run.sh
git diff --check
```

Expected: PASS.

Commit:

```bash
git add .gitattributes scripts/tests/run.sh scripts/check.sh README.md
git commit -m "docs: document release installation workflows"
```

---

### Task 7: Perform clean packaging and full repository verification

**Files:**
- Modify only files implicated by a failing verification, with a red regression test before behavior fixes.

**Interfaces:**
- Produces evidence that a clean tagged checkout creates exactly the documented assets and that all existing project gates remain green.

- [ ] **Step 1: Run script syntax and behavior suites from a clean shell**

Run:

```bash
bash -n scripts/*.sh scripts/tests/*.sh
bash scripts/tests/run.sh
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run action and shell linters**

Run:

```bash
shellcheck scripts/*.sh scripts/tests/*.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
```

Expected: no findings.

- [ ] **Step 3: Exercise a real clean tagged package build without modifying the working repository**

Run in a temporary directory:

```bash
verification_dir="$(mktemp -d)"
git clone --shared . "$verification_dir/config-hub"
cd "$verification_dir/config-hub"
git tag v0.0.0
./scripts/package-release.sh v0.0.0
```

Require exactly these basenames:

```text
checksums.txt
config-hub-cli_0.0.0_linux_amd64.tar.gz
config-hub-cli_0.0.0_linux_arm64.tar.gz
config-hub-server_0.0.0_linux_amd64.tar.gz
config-hub-server_0.0.0_linux_arm64.tar.gz
```

Run `sha256sum --check checksums.txt`, list every archive member, reject extras, run the native amd64 binaries' `version` commands, and use `go version -m` to confirm both arm64 binaries record `GOOS=linux`, `GOARCH=arm64`, and the injected version ldflag. Remove only the validated temporary clone directory after evidence is captured.

- [ ] **Step 4: Run the full quality gate**

From the original repository run: `bash scripts/check.sh`

Expected: formatting, vet, race tests, Bash tests, frontend typecheck/tests/build, both host binaries, Playwright E2E, and runtime acceptance all PASS.

- [ ] **Step 5: Inspect final repository state**

Run:

```bash
git diff --check
git status --short --branch
git log --oneline -8
```

Expected: `main` is ahead by the planned commits, no tracked change remains uncommitted, and the pre-existing `.coder-studio/` is untouched.

- [ ] **Step 6: Record the external acceptance boundary**

Do not create or push a real tag in this implementation session. Report that the remaining external check is the first GitHub-hosted run after the user pushes a new `vX.Y.Z` tag; include the expected asset list and Server/CLI smoke commands in the handoff.
