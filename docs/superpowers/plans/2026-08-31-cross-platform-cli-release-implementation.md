# ConfigHub Cross-Platform CLI Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing GitHub Release pipeline and script-only CLI installation path to macOS arm64 and Windows amd64 while preserving all current Linux Server and CLI behavior.

**Architecture:** Ubuntu remains the single deterministic packager and produces two Linux Server archives plus four CLI archives. The Bash installer becomes Bash-3.2-compatible for Linux and macOS, a PowerShell 5.1-compatible installer owns Windows installation and user PATH updates, and GitHub Actions runs the packaged CLI on native Apple Silicon and Windows runners before the existing atomic publication transaction.

**Tech Stack:** Go 1.25, Bash 3.2+, Windows PowerShell 5.1, PowerShell 7, .NET ZIP APIs, GNU tar/gzip/zip on Ubuntu, GitHub Actions, shellcheck, actionlint.

**Spec:** `docs/superpowers/specs/2026-08-31-cross-platform-cli-release-design.md`

## Global Constraints

- Work directly on `main`; do not create a worktree or feature branch.
- Do not use subagents in this session; execute inline with `superpowers:executing-plans`.
- Preserve the user's unrelated untracked `.coder-studio/` directory.
- CLI targets are exactly `linux/amd64`, `linux/arm64`, `darwin/arm64`, and `windows/amd64`.
- Server+Web targets remain exactly `linux/amd64` and `linux/arm64`; do not change Server archive members or deployment behavior.
- A new Release contains exactly six archives plus `checksums.txt`; existing Linux archive names remain compatible.
- Linux and macOS use `.tar.gz`; Windows uses `.zip` and the binary name `confighub.exe`.
- All release binaries use `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`, and `-X confighub.local/internal/buildinfo.Version=TAG`.
- `scripts/install-cli.sh` must run with macOS system `/bin/bash` 3.2: no `mapfile`, associative arrays, `${name,,}`, or Bash 4+ syntax.
- `scripts/install-cli.ps1` must run unchanged under Windows PowerShell 5.1 and PowerShell 7+.
- Installers accept only the fixed `https://github.com/art-shier/config-hub` repository and remove quarantine/Mark-of-the-Web only after checksum and archive validation.
- Windows installation is per-user by default, updates user PATH only after a successful atomic install, and creates no GUI or installer registration.
- Code signing, Apple notarization, Homebrew, Winget, MSI, and PKG remain out of scope.
- Follow red-green-refactor for Bash and PowerShell behavior. Human documentation and workflow configuration are verified by their consumers rather than source-text tests.

---

## Locked File Map

### Cross-platform packaging

- `scripts/package-release.sh`: validates the product/OS/architecture matrix, builds six binaries, creates five tarballs plus one ZIP, and writes the six-entry checksum manifest.
- `scripts/tests/package_release_test.sh`: tests target naming, invalid combinations, ZIP layout, and unchanged systemd unit validation.

### CLI installers

- `scripts/install-cli.sh`: portable Linux/macOS installer and shared safe-tar/checksum boundary.
- `scripts/tests/install_cli_test.sh`: Bash-3.2-compatible Linux/macOS mapping, checksum, origin-mark ordering, archive, and atomic-install behavior tests.
- `scripts/install-cli.ps1`: self-contained Windows amd64 downloader, verifier, ZIP reader, atomic installer, and user PATH updater.
- `scripts/tests/install_cli_windows_test.ps1`: PowerShell 5.1/7 behavior tests without Pester or network access.

### Publication and automation

- `scripts/publish-release.sh`: validates and atomically publishes the seven-file Release.
- `scripts/tests/publish_release_test.sh`: exercises the seven-file local and remote publication transaction.
- `.github/workflows/ci.yml`: adds macOS arm64 and Windows amd64 CLI jobs to existing Ubuntu checks.
- `.github/workflows/release.yml`: separates quality, packaging, native smoke, and publication jobs using one internal Actions artifact.
- `.gitattributes`: normalizes PowerShell scripts as LF text while preserving current Bash/workflow rules.

### Documentation

- `README.md`: documents all supported products, platform-specific script installation, unsigned-source-marker handling, upgrade, version check, and uninstall.

---

### Task 1: Extend the deterministic Release package matrix

**Files:**
- Modify: `scripts/tests/package_release_test.sh`
- Modify: `scripts/package-release.sh`

**Interfaces:**
- Produces: `validate_release_target PRODUCT OS ARCH`.
- Produces: `release_archive_base PRODUCT TAG OS ARCH`.
- Produces: `release_archive_name PRODUCT TAG OS ARCH`.
- Produces: `release_binary_name PRODUCT OS`.
- Produces: `create_zip_archive PACKAGE_ROOT ARCHIVE_BASE OUTPUT_PATH SOURCE_EPOCH`.
- Later tasks consume the exact archive names emitted by `release_archive_name`.

- [ ] **Step 1: Write failing matrix and archive-name tests**

Change the existing naming assertions to the four-argument interface and add literal expectations:

```bash
assert_eq "config-hub-server_1.2.3_linux_arm64" \
  "$(release_archive_base server v1.2.3 linux arm64)"
assert_eq "config-hub-cli_1.2.3_linux_amd64.tar.gz" \
  "$(release_archive_name cli v1.2.3 linux amd64)"
assert_eq "config-hub-cli_1.2.3_darwin_arm64.tar.gz" \
  "$(release_archive_name cli v1.2.3 darwin arm64)"
assert_eq "config-hub-cli_1.2.3_windows_amd64.zip" \
  "$(release_archive_name cli v1.2.3 windows amd64)"
assert_eq "confighub" "$(release_binary_name cli darwin)"
assert_eq "confighub.exe" "$(release_binary_name cli windows)"

assert_fails validate_release_target server darwin arm64
assert_fails validate_release_target server windows amd64
assert_fails validate_release_target cli darwin amd64
assert_fails validate_release_target cli windows arm64
assert_fails validate_release_target cli linux riscv64
assert_fails release_archive_name web v1.2.3 linux amd64
```

The production change these tests protect is accidentally publishing an unsupported product/platform combination or changing a compatible Linux filename.

- [ ] **Step 2: Run the focused test and verify RED**

Run in WSL/Linux:

```bash
bash scripts/tests/package_release_test.sh
```

Expected: FAIL because the current helpers accept only three arguments, force `linux`, and do not produce ZIP names.

- [ ] **Step 3: Implement the explicit target and naming helpers**

Use this matrix, with no permissive fallback:

```bash
validate_release_target() {
  local product="$1"
  local operating_system="$2"
  local architecture="$3"
  case "$product/$operating_system/$architecture" in
    server/linux/amd64 | server/linux/arm64 | \
      cli/linux/amd64 | cli/linux/arm64 | \
      cli/darwin/arm64 | cli/windows/amd64) ;;
    *) die "unsupported release target: $product/$operating_system/$architecture" ;;
  esac
}

release_binary_name() {
  local product="$1"
  local operating_system="$2"
  case "$product/$operating_system" in
    server/linux) printf '%s\n' 'confighub-server' ;;
    cli/linux | cli/darwin) printf '%s\n' 'confighub' ;;
    cli/windows) printf '%s\n' 'confighub.exe' ;;
    *) die "unsupported release binary: $product/$operating_system" ;;
  esac
}
```

`release_archive_base` must call `validate_release_target` and print `config-hub-PRODUCT_VERSION_OS_ARCH`. `release_archive_name` appends `.zip` only for `cli/windows/amd64`; every other valid target appends `.tar.gz`.

- [ ] **Step 4: Run matrix tests and verify GREEN**

Run:

```bash
bash scripts/tests/package_release_test.sh
```

Expected: PASS, including the existing strict-tag and systemd unit assertions.

- [ ] **Step 5: Add a failing real ZIP layout test**

Create a temporary package directory containing only `BASE/confighub.exe`, call the missing `create_zip_archive`, and assert the literal `unzip -Z1` output:

```text
config-hub-cli_1.2.3_windows_amd64/
config-hub-cli_1.2.3_windows_amd64/confighub.exe
```

Also assert that `unzip -Z -v` reports no extra comment or member. This test catches a packaging change that flattens the ZIP or includes the temporary package root.

- [ ] **Step 6: Run the ZIP test and verify RED**

Run:

```bash
bash scripts/tests/package_release_test.sh
```

Expected: FAIL with `create_zip_archive: command not found`.

- [ ] **Step 7: Implement deterministic ZIP creation and all six build targets**

`create_zip_archive` must set the directory and executable mtimes to `SOURCE_EPOCH`, then run from `PACKAGE_ROOT`:

```bash
TZ=UTC touch -d "@$source_epoch" -- "$archive_base" "$archive_base/confighub.exe"
TZ=UTC LC_ALL=C zip -X -q "$output_path" "$archive_base/" "$archive_base/confighub.exe"
```

Refactor `build_release` to:

- build Server only for `linux amd64` and `linux arm64`, preserving the current Server package layout;
- build CLI for the four exact targets, writing `confighub.exe` only for Windows;
- call `create_archive` for tarballs and `create_zip_archive` for Windows;
- require `zip` in `verify_release_source`;
- generate checksums from `config-hub-*.tar.gz` and `config-hub-*.zip`;
- require exactly six archive files before moving `dist/release`.

- [ ] **Step 8: Run syntax and package tests GREEN**

Run:

```bash
bash -n scripts/package-release.sh scripts/tests/package_release_test.sh
bash scripts/tests/package_release_test.sh
```

Expected: PASS.

- [ ] **Step 9: Commit the packaging task**

```bash
git add scripts/package-release.sh scripts/tests/package_release_test.sh
git commit -m "feat: package macOS and Windows CLI releases"
```

---

### Task 2: Make the Bash installer portable to macOS arm64

**Files:**
- Modify: `scripts/tests/install_cli_test.sh`
- Modify: `scripts/install-cli.sh`

**Interfaces:**
- Produces: `detect_cli_target UNAME_SYSTEM UNAME_MACHINE`, printing exactly `OS ARCH`.
- Produces: `sha256_file FILE`, printing one lowercase 64-character digest using `sha256sum` or `shasum`.
- Produces: `remove_download_origin_mark OS FILE`.
- Preserves: `install_cli_release TAG INSTALL_DIRECTORY` and existing public flags.

- [ ] **Step 1: Write failing Linux/macOS target tests**

Replace `detect_linux_arch` expectations with:

```bash
assert_eq "linux amd64" "$(detect_cli_target Linux x86_64)"
assert_eq "linux amd64" "$(detect_cli_target Linux amd64)"
assert_eq "linux arm64" "$(detect_cli_target Linux aarch64)"
assert_eq "linux arm64" "$(detect_cli_target Linux arm64)"
assert_eq "darwin arm64" "$(detect_cli_target Darwin arm64)"
assert_eq "darwin arm64" "$(detect_cli_target Darwin aarch64)"
assert_fails detect_cli_target Darwin x86_64
assert_fails detect_cli_target Windows_NT AMD64
assert_fails detect_cli_target Linux riscv64
```

- [ ] **Step 2: Run target tests and verify RED**

Run:

```bash
bash scripts/tests/install_cli_test.sh
```

Expected: FAIL because `detect_cli_target` does not exist and Darwin is rejected.

- [ ] **Step 3: Implement the target mapping with Bash 3.2 syntax**

Use a `case "$system/$machine"` with the six accepted aliases and no arrays. `install_cli_release` reads the two words into local `operating_system` and `architecture`, then selects `config-hub-cli_VERSION_OS_ARCH.tar.gz`.

Remove every use of `mapfile` and `${value,,}` from the production script. Count exact manifest matches with `awk`, normalize digests with `tr '[:upper:]' '[:lower:]'`, and iterate tar member text with a here-string so counters remain in the current shell.

- [ ] **Step 4: Add failing portable checksum and origin-mark ordering tests**

Extend the fixture to generate its manifest using the new `sha256_file`. On Linux, assert the `sha256sum` result for a literal file. For the fallback case, run `sha256_file` with PATH containing only fixture copies of `awk` and a controlled `shasum` executable, with no `sha256sum`, and assert the same lowercase digest. The macOS native job then executes the real system `shasum` path.

Override `remove_download_origin_mark` to append `origin-mark` to an event file. Make the fixture CLI append `version` to the path in `CLI_INSTALL_TEST_EVENT_LOG` immediately before printing its version. For a wrong hash, assert the event file remains absent. For a valid archive, assert the two literal event lines are `origin-mark` followed by `version`. This catches moving quarantine removal before validation.

- [ ] **Step 5: Run checksum/origin tests and verify RED**

Run:

```bash
bash scripts/tests/install_cli_test.sh
```

Expected: FAIL because `sha256_file` and `remove_download_origin_mark` do not exist.

- [ ] **Step 6: Implement portable checksum, tar extraction, and quarantine handling**

`sha256_file` chooses an available backend and prints only the digest:

```bash
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -- "$file" | awk '{ print $1 }'
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 -- "$file" | awk '{ print $1 }'
else
  die 'required SHA-256 command not found: sha256sum or shasum'
fi
```

`remove_download_origin_mark` is a no-op for Linux. For Darwin it requires `xattr`, captures `xattr FILE` to list attribute names, and fails if that listing command fails. If and only if one listed line equals `com.apple.quarantine`, call `xattr -d com.apple.quarantine FILE`; an empty successful listing is a no-op.

Replace GNU-only extraction flags with validated-member extraction into the private temporary directory, then require a non-symlink regular file and explicitly `chmod 0755` it. Keep same-directory staging and atomic `mv` behavior.

- [ ] **Step 7: Make the end-to-end fixture platform-derived**

Build the fixture base from `detect_cli_target "$(uname -s)" "$(uname -m)"` so the same test selects a Linux archive on Ubuntu and a Darwin archive on `macos-14`. Keep all existing wrong-hash, extra-member, link, old-target-preservation, and successful-version assertions.

- [ ] **Step 8: Verify Bash 3.2-compatible behavior GREEN**

Run on Linux:

```bash
bash -n scripts/install-cli.sh scripts/tests/install_cli_test.sh
bash scripts/tests/install_cli_test.sh
shellcheck scripts/install-cli.sh scripts/tests/install_cli_test.sh
```

Expected: PASS with no Bash 4-only constructs reported by a literal scan:

```bash
if grep -En 'mapfile|declare -A|\$\{[^}]+,,\}' scripts/install-cli.sh scripts/tests/install_cli_test.sh; then
  exit 1
fi
```

- [ ] **Step 9: Commit the portable Bash installer**

```bash
git add scripts/install-cli.sh scripts/tests/install_cli_test.sh
git commit -m "feat: install CLI on Apple Silicon macOS"
```

---

### Task 3: Add the Windows PowerShell installer

**Files:**
- Create: `scripts/install-cli.ps1`
- Create: `scripts/tests/install_cli_windows_test.ps1`

**Interfaces:**
- Public parameters: `-Version TAG` and `-InstallDir ABSOLUTE_DIRECTORY`.
- Produces: `Assert-ReleaseTag`, `Assert-AbsoluteInstallDirectory`, `Resolve-WindowsCliTarget`, `Get-ReleaseTagFromUri`, `Get-LatestReleaseTag`, `Get-ManifestDigest`, `Assert-ArchiveChecksum`, `Expand-VerifiedCliArchive`, `Remove-InternetMark`, `Assert-CliVersion`, `Move-FileAtomically`, `Add-UserPathEntry`, `Install-CliRelease`, and `Invoke-InstallCliMain`.
- Side-effect wrappers for tests: `Invoke-DownloadFile`, `Get-UserPathValue`, and `Set-UserPathValue`.
- Dot-sourcing the script defines functions without executing installation.

- [ ] **Step 1: Create failing parameter, tag, platform, and source-guard tests**

Create a self-contained test runner with `$ErrorActionPreference = 'Stop'`, `Set-StrictMode -Version 2`, `Assert-Equal`, `Assert-Throws`, and a final `Windows CLI installer tests passed` line. Dot-source the missing production script, then assert:

```powershell
Assert-Equal 'windows amd64' (Resolve-WindowsCliTarget -IsWindows $true -Architecture 'AMD64' -Wow64Architecture '')
Assert-Equal 'windows amd64' (Resolve-WindowsCliTarget -IsWindows $true -Architecture 'x86' -Wow64Architecture 'AMD64')
Assert-Throws { Resolve-WindowsCliTarget -IsWindows $false -Architecture 'AMD64' -Wow64Architecture '' }
Assert-Throws { Resolve-WindowsCliTarget -IsWindows $true -Architecture 'ARM64' -Wow64Architecture '' }
Assert-ReleaseTag 'v1.2.3'
Assert-Throws { Assert-ReleaseTag 'v1.2.3-rc1' }
Assert-Throws { Assert-AbsoluteInstallDirectory 'relative\bin' }
Assert-Equal 'v1.2.3' (Get-ReleaseTagFromUri ([Uri]'https://github.com/art-shier/config-hub/releases/tag/v1.2.3'))
Assert-Throws { Get-ReleaseTagFromUri ([Uri]'https://github.com/another/config-hub/releases/tag/v1.2.3') }
Assert-Throws { Get-ReleaseTagFromUri ([Uri]'https://github.com/art-shier/config-hub/releases/tag/v1.2.3-rc1') }
```

- [ ] **Step 2: Run the PowerShell test and verify RED**

Run on Windows:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\tests\install_cli_windows_test.ps1
```

Expected: FAIL because `scripts/install-cli.ps1` does not exist.

- [ ] **Step 3: Implement the sourceable PowerShell 5.1 skeleton and validation**

Start the production script with an ASCII-only parameter block and strict error behavior:

```powershell
[CmdletBinding()]
param(
    [string]$Version = '',
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'ConfigHub\bin')
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
```

Use .NET APIs available in Windows PowerShell 5.1; do not use ternary operators, null-coalescing operators, `ForEach-Object -Parallel`, or `System.IO.File.Move(source, target, overwrite)`. End with this source guard:

```powershell
if ($MyInvocation.InvocationName -ne '.') {
    Invoke-InstallCliMain -RequestedVersion $Version -RequestedInstallDir $InstallDir
}
```

Implement strict tag, local absolute path, fixed GitHub URL, latest redirect, and Windows amd64 validation. `Invoke-WebRequest` must use `-UseBasicParsing`, and the script must enable TLS 1.2 without disabling newer protocols.

- [ ] **Step 4: Run validation tests GREEN**

Run the Step 2 command. Expected: validation assertions PASS; the test continues to the next missing archive function.

- [ ] **Step 5: Add failing checksum and ZIP safety tests**

Use `[System.IO.Compression.ZipArchive]` to create fixture ZIPs with literal entry names. Add cases for:

- one valid directory entry plus `confighub.exe`;
- wrong, missing, and duplicate manifest entries;
- extra ZIP member;
- `../confighub.exe` and `/confighub.exe`;
- duplicate executable entries;
- a Unix symlink mode encoded in `ZipArchiveEntry.ExternalAttributes`.

Encode the link fixture explicitly:

```powershell
$linkEntry = $zip.CreateEntry('config-hub-cli_1.2.3_windows_amd64/confighub.exe')
$linkEntry.ExternalAttributes = [int](0xA000 -shl 16)
```

Assert invalid cases throw before `Remove-InternetMark` is invoked and leave an existing target containing literal `old-cli` bytes unchanged.

- [ ] **Step 6: Run archive tests and verify RED**

Run the Step 2 command. Expected: FAIL because checksum and ZIP functions are missing.

- [ ] **Step 7: Implement exact checksum and manual ZIP extraction**

`Get-ManifestDigest` reads all manifest lines, escapes the selected basename with `[Regex]::Escape`, matches exactly `64-HEX`, two ASCII spaces, and that basename, and requires one match. `Assert-ArchiveChecksum` compares it to `(Get-FileHash -Algorithm SHA256).Hash` using ordinal-ignore-case semantics.

`Expand-VerifiedCliArchive` opens the ZIP read-only, validates the exact directory and executable entries, rejects `..`, rooted paths, backslashes in entry names, duplicates, extras, and Unix link file-type bits in `ExternalAttributes`. It opens only the expected executable entry stream and copies it to a caller-provided same-directory staging path; it never calls `Expand-Archive` on untrusted members.

- [ ] **Step 8: Add failing full-install, atomicity, marker-order, and PATH tests**

Compile this fixture console EXE using `Add-Type -OutputAssembly`, package it with the .NET ZIP API, and generate a real SHA-256 manifest:

```powershell
$fixtureSource = @'
using System;
public static class Program {
    public static int Main(string[] args) {
        if (args.Length == 1 && args[0] == "version") {
            Console.WriteLine("v1.2.3");
            return 0;
        }
        return 2;
    }
}
'@
Add-Type -TypeDefinition $fixtureSource -Language CSharp `
    -OutputAssembly $fixtureExe -OutputType ConsoleApplication
```

Override `Invoke-DownloadFile` to copy fixture basenames, `Remove-InternetMark` to record an event, and the user-PATH wrappers to use an in-memory string. For the atomic-move failure case only, temporarily replace `Move-FileAtomically` with a throwing function, then restore the real definition. Save `$env:Path` before the test and restore it in `finally`. Assert:

- valid installation creates a regular `confighub.exe` that prints `v1.2.3`;
- an existing target is replaced only after version validation;
- wrong hash, wrong embedded version, and atomic-move failure preserve old bytes;
- `Remove-InternetMark` occurs after checksum/ZIP validation and before version execution;
- PATH is unchanged on every failure;
- success adds the absolute directory once, case-insensitively, to user and process PATH;
- reinstallation does not duplicate PATH entries.

- [ ] **Step 9: Run full-install tests and verify RED**

Run the Step 2 command. Expected: FAIL because atomic movement, version, PATH, and orchestration functions are incomplete.

- [ ] **Step 10: Implement atomic installation and PATH update**

`Remove-InternetMark` calls `Unblock-File -LiteralPath` only for the one staged executable. `Assert-CliVersion` executes `& $Path version`, captures one line, requires exit code zero, and compares the exact tag.

Implement `Move-FileAtomically` through one `Add-Type` P/Invoke definition for `MoveFileExW` with `MOVEFILE_REPLACE_EXISTING` and `MOVEFILE_WRITE_THROUGH`; format the Win32 error code if it returns false. Stage under `INSTALL_DIR\.confighub.install.RANDOM.exe`, never under the download directory.

`Add-UserPathEntry` splits on `[IO.Path]::PathSeparator`, trims only empty entries, compares normalized full paths with `OrdinalIgnoreCase`, appends once through `Set-UserPathValue`, and updates `$env:Path` once. Call it only after the installed target passes its final version check.

`Install-CliRelease` owns a random `%TEMP%\confighub-cli-install-*` directory and a same-directory stage path in `try/finally`, downloading only the selected ZIP and `checksums.txt`. Cleanup removes those exact temporary paths and never the install directory.

- [ ] **Step 11: Run Windows PowerShell 5.1 tests GREEN**

Run:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\tests\install_cli_windows_test.ps1
```

Expected: PASS with `Windows CLI installer tests passed` and no changes to the real user PATH because the test overrides its wrappers.

If `pwsh` is installed locally, also run:

```powershell
pwsh -NoProfile -File scripts/tests/install_cli_windows_test.ps1
```

PowerShell 7 remains a required GitHub Actions gate even when the local executable is unavailable.

- [ ] **Step 12: Commit the Windows installer**

```powershell
git add scripts/install-cli.ps1 scripts/tests/install_cli_windows_test.ps1
git commit -m "feat: install CLI on Windows amd64"
```

---

### Task 4: Expand the atomic publication transaction to seven assets

**Files:**
- Modify: `scripts/tests/publish_release_test.sh`
- Modify: `scripts/publish-release.sh`

**Interfaces:**
- Preserves: `publish-release.sh TAG RELEASE_DIRECTORY`.
- Produces: `expected_asset_names TAG` with seven sorted lines.
- Validates: six manifest entries covering all `.tar.gz` and `.zip` archives.

- [ ] **Step 1: Update the publication fixture first**

Make `make_release_fixture` create literal content for all six archives and compute one manifest over them. Set the exact expected list to:

```text
checksums.txt
config-hub-cli_1.2.3_darwin_arm64.tar.gz
config-hub-cli_1.2.3_linux_amd64.tar.gz
config-hub-cli_1.2.3_linux_arm64.tar.gz
config-hub-cli_1.2.3_windows_amd64.zip
config-hub-server_1.2.3_linux_amd64.tar.gz
config-hub-server_1.2.3_linux_arm64.tar.gz
```

Add separate pre-create failure cases for a missing Darwin archive, a missing Windows ZIP, an extra old-style filename, and a checksum manifest missing the Windows entry.

- [ ] **Step 2: Run publication tests and verify RED**

Run:

```bash
bash scripts/tests/publish_release_test.sh
```

Expected: FAIL because production still expects exactly five files and four checksum entries.

- [ ] **Step 3: Implement the seven-file expected asset contract**

Update `expected_asset_names` to the exact sorted list above with the requested version. Compare manifest basenames to every expected line except `checksums.txt`; do not filter only `.tar.gz`. Change errors to `exact seven assets` and `exact six archives`. Change the success line to:

```text
Published ConfigHub Release TAG with verified cross-platform assets.
```

Keep draft ownership, remote `isDraft`, remote name comparison, no-clobber upload, and owned-draft cleanup behavior unchanged.

- [ ] **Step 4: Run publication tests GREEN**

Run:

```bash
bash -n scripts/publish-release.sh scripts/tests/publish_release_test.sh
bash scripts/tests/publish_release_test.sh
```

Expected: PASS.

- [ ] **Step 5: Run the stable Bash suite**

```bash
bash scripts/tests/run.sh
```

Expected: all four Bash test groups PASS in their existing fixed order.

- [ ] **Step 6: Commit publication changes**

```bash
git add scripts/publish-release.sh scripts/tests/publish_release_test.sh
git commit -m "feat: publish cross-platform CLI assets"
```

---

### Task 5: Add native macOS and Windows CI/Release gates

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `.gitattributes`

**Interfaces:**
- CI produces independent Ubuntu, macOS arm64, and Windows amd64 job results.
- Release passes one immutable `config-hub-release-assets` Actions artifact from package through native smoke to publish.
- Only the final publish job receives `contents: write`.

- [ ] **Step 1: Add PowerShell LF normalization**

Append without renormalizing unrelated files:

```gitattributes
*.ps1 text eol=lf
```

- [ ] **Step 2: Add the two native CLI jobs to CI**

Keep the current Ubuntu `checks` behavior, add explicit `zip` and `unzip` packages for the new package helper test, and let shellcheck naturally include the modified Bash files. Add:

- `cli-macos`, `runs-on: macos-14`, with checkout, Go 1.25.x, an explicit `uname -m == arm64` assertion, `/bin/bash scripts/tests/install_cli_test.sh`, `go test ./internal/cli -count=1`, a native CLI build with version `ci`, and `./dist/confighub version == ci`;
- `cli-windows`, `runs-on: windows-2025`, with checkout, Go 1.25.x, a runtime x64 assertion, the PowerShell installer test under both `shell: powershell` and `shell: pwsh`, `go test ./internal/cli -count=1`, a native `dist\confighub.exe` build with version `ci`, and exact `ci` version output.

Use bounded job timeouts. Do not install Node or browsers in the CLI-only jobs.

- [ ] **Step 3: Refactor Release into quality and package jobs**

The `quality` job retains tag validation, browser/shell dependencies, actionlint, package helper tests, and `scripts/check.sh` on `ubuntu-24.04` with read-only contents. Its apt dependency list explicitly includes `shellcheck`, `zip`, and `unzip`.

The `package` job needs `quality`, uses `fetch-depth: 0`, Go 1.25.x, Node 24.15.0, and installs `zip`. Run `scripts/package-release.sh "$GITHUB_REF_NAME"`, compare the seven literal basenames, then upload `dist/release/*` using `actions/upload-artifact@v4` with:

```yaml
name: config-hub-release-assets
if-no-files-found: error
retention-days: 1
```

- [ ] **Step 4: Add native Release smoke jobs**

Both jobs check out source for installer tests and download `config-hub-release-assets` into `dist/release`.

On `macos-14`:

- require `uname -s == Darwin` and `uname -m == arm64`;
- run `/bin/bash scripts/tests/install_cli_test.sh` under the system Bash;
- select the exact Darwin archive and checksum line;
- compare `shasum -a 256` with the manifest;
- compare the two literal tar members;
- extract into `$RUNNER_TEMP/confighub-smoke` and require `confighub version == GITHUB_REF_NAME`.

On `windows-2025`:

- require `[Runtime.InteropServices.RuntimeInformation]::OSArchitecture` to be `X64`;
- run the Windows installer test once with Windows PowerShell 5.1 and once with PowerShell 7;
- select the exact Windows ZIP and unique checksum line;
- compare `Get-FileHash -Algorithm SHA256`;
- use .NET ZIP APIs to compare the two literal members;
- extract to `$env:RUNNER_TEMP\confighub-smoke` and require `confighub.exe version` to equal `$env:GITHUB_REF_NAME`.

- [ ] **Step 5: Add the final publication job**

`publish` needs `package`, `smoke-cli-macos`, and `smoke-cli-windows`. It runs on Ubuntu, has `contents: write`, downloads the same artifact, and calls:

```bash
./scripts/publish-release.sh "$GITHUB_REF_NAME" dist/release
```

Do not recreate or mutate archives after native smoke.

- [ ] **Step 6: Validate workflow consumers**

Run on Linux:

```bash
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
shellcheck scripts/*.sh scripts/tests/*.sh
git diff --check
```

Expected: no findings. Also run the PowerShell 5.1 test on Windows and the Bash suite on Linux; those real consumers protect the commands referenced by YAML.

- [ ] **Step 7: Commit workflow orchestration**

```bash
git add .gitattributes .github/workflows/ci.yml .github/workflows/release.yml
git commit -m "ci: verify CLI releases on macOS and Windows"
```

---

### Task 6: Document cross-platform installation and lifecycle

**Files:**
- Modify: `README.md`

**Interfaces:**
- Documents the same seven-file asset contract and only the flags actually implemented by the two installers.

- [ ] **Step 1: Update the Release asset and support table**

Replace the Linux-only wording with a concise mapping that distinguishes:

- Server+Web: Linux amd64/arm64 only;
- CLI: Linux amd64/arm64, macOS arm64, Windows amd64;
- six archives plus one `checksums.txt`.

Do not imply that installing Server installs any CLI.

- [ ] **Step 2: Split CLI installation by platform**

Keep the existing Linux download/review/latest/fixed/custom-directory examples. Add macOS arm64 examples using the same Bash script and `/usr/local/bin`, including `sudo`, fixed versions, a user-writable custom directory, and `confighub version`.

Add the Windows review-first flow:

```powershell
Invoke-WebRequest `
  https://raw.githubusercontent.com/art-shier/config-hub/main/scripts/install-cli.ps1 `
  -OutFile .\install-cli.ps1
Get-Content -Raw .\install-cli.ps1
Unblock-File .\install-cli.ps1
Set-ExecutionPolicy -Scope Process Bypass -Force
& .\install-cli.ps1
```

Add fixed/custom examples:

```powershell
& .\install-cli.ps1 -Version v1.2.3
& .\install-cli.ps1 -Version v1.2.3 -InstallDir C:\Tools\ConfigHub
confighub version
```

Explain the default `%LOCALAPPDATA%\ConfigHub\bin`, user PATH update, current PowerShell behavior, and reopening other terminals.

- [ ] **Step 3: Document unsigned and source-marker boundaries**

State that the first cross-platform release is not Apple-notarized or Authenticode-signed. Explain that the installers remove quarantine/Mark-of-the-Web only from the checksum-verified staged binary and do not disable Gatekeeper, SmartScreen, Defender, antivirus, or execution policy globally.

- [ ] **Step 4: Update upgrade and uninstall instructions**

State that rerunning the platform installer upgrades atomically. Linux/macOS uninstall removes only the actual `confighub`. Windows uninstall removes `%LOCALAPPDATA%\ConfigHub\bin\confighub.exe` or the custom target, while user PATH removal remains an explicit user choice; never recursively delete a non-empty custom directory.

- [ ] **Step 5: Update publisher expectations**

Keep the same tag commands but change the expected public assets to the seven-file list and mention that macOS and Windows native smoke must pass before publication.

- [ ] **Step 6: Verify and commit documentation**

Run:

```bash
git diff --check
```

Expected: PASS.

```bash
git add README.md
git commit -m "docs: explain cross-platform CLI installation"
```

---

### Task 7: Perform clean cross-platform packaging and repository verification

**Files:**
- Modify only files implicated by a failing verification, with a red regression test before any behavior fix.

**Interfaces:**
- Produces local evidence for all cross-build metadata and native Windows execution.
- Leaves native macOS execution as a required GitHub-hosted gate on the same internal artifact.

- [ ] **Step 1: Run Linux syntax, behavior, lint, and full repository gates**

From a WSL native ext4 clone of the current `main`, run:

```bash
bash -n scripts/*.sh scripts/tests/*.sh
bash scripts/tests/run.sh
shellcheck scripts/*.sh scripts/tests/*.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
./scripts/check.sh
```

Expected: all commands PASS, including Go race, frontend unit/build, Playwright, and runtime acceptance.

- [ ] **Step 2: Run native Windows installer tests**

From the Windows workspace run:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\tests\install_cli_windows_test.ps1
```

Expected: PASS. If `pwsh` is locally available, run the PowerShell 7 form too; otherwise record PowerShell 7 as an external GitHub Actions gate rather than installing a global tool silently.

- [ ] **Step 3: Build a real clean tagged package**

In a new WSL ext4 temporary clone:

```bash
git tag v0.0.0
./scripts/package-release.sh v0.0.0
```

Require exactly:

```text
checksums.txt
config-hub-cli_0.0.0_darwin_arm64.tar.gz
config-hub-cli_0.0.0_linux_amd64.tar.gz
config-hub-cli_0.0.0_linux_arm64.tar.gz
config-hub-cli_0.0.0_windows_amd64.zip
config-hub-server_0.0.0_linux_amd64.tar.gz
config-hub-server_0.0.0_linux_arm64.tar.gz
```

Run `sha256sum --check checksums.txt`, compare every tar/ZIP member to its literal contract, and verify Unix file modes.

- [ ] **Step 4: Verify platform metadata and executable versions**

- Run both Linux amd64 binaries' `version` commands and require `v0.0.0`.
- Use `go version -m` on Darwin and Windows CLI binaries to require `GOOS=darwin GOARCH=arm64` and `GOOS=windows GOARCH=amd64` respectively.
- Use `go tool nm -size` plus exact `v0.0.0` strings to verify the cross-built `buildinfo.Version` injection, because Go build metadata does not record linker flags.
- Copy only the validated Windows ZIP to a newly created Windows temporary directory, extract it with PowerShell, run native `confighub.exe version`, and require `v0.0.0`.

- [ ] **Step 5: Inspect final repository state**

Run with Windows Git:

```powershell
git diff --check
git status --short --branch
git log --oneline -10
git tag --list v0.0.0
```

Expected: the real workspace has no `v0.0.0` tag, no tracked changes remain, `main` contains the planned commits, and `.coder-studio/` is still the only unrelated untracked path.

- [ ] **Step 6: Clean only validated temporary directories**

Resolve each WSL and Windows temporary verification directory to an absolute path, require it to remain under the intended OS temp root with the task-specific prefix, then remove only those explicit directories. Do not remove tool caches, the workspace, or any user directory.

- [ ] **Step 7: Record the external native acceptance boundary**

Do not push a real tag in this implementation session. Report that the first real tag must make `macos-14` and `windows-2025` smoke jobs pass before publication, then manually install from that public Release on one clean Apple Silicon Mac and one clean Windows amd64 terminal and run:

```text
confighub version
```

After the version check, use an operator-provided acceptance account and machine token to perform one read-only `confighub export` against a dedicated existing test project/environment. Keep the token outside command-line arguments, logs, and the repository; the external environment owner selects those names at acceptance time.
