---
name: confighub
description: Install and use the ConfigHub CLI to read, pull, inject, or update configuration for a specified project and environment on a ConfigHub server.
---

# ConfigHub

Use this guide when the user asks to operate ConfigHub. Follow the user's requested project, environment, service, output directory, and operation. Ask for missing task parameters only when they cannot be determined from the conversation or local project configuration.

## Identify the server

When reading this skill from a URL, remove `/skills/confighub/SKILL.md` from that URL to obtain the ConfigHub server base URL, retaining any deployment prefix. For example, `https://config.example.com/skills/confighub/SKILL.md` identifies `https://config.example.com`. When reading a downloaded copy, use the server address supplied by the user. Do not guess an address or send existing credentials to a different server.

This document is public and requires no login or Authorization header. It contains no token or project data. Reading it does not grant API access or install a persistent skill into the agent's environment.

## Check the CLI before operating

Run `confighub version` and `confighub --help`. Check the help for the intended command, for example `confighub pull --help`. A present CLI may be older than this guide; do not assume its commands or flags are supported.

If the command is missing, check PATH and the usual user installation first:

- Linux/macOS: `command -v confighub`, then `$HOME/.local/bin/confighub`.
- Windows PowerShell: `Get-Command confighub -ErrorAction SilentlyContinue`, then `$env:LOCALAPPDATA\ConfigHub\bin\confighub.exe`.

If an existing executable works by absolute path, use it or add its directory to the current process PATH. Install only if it is absent; upgrade if the required command is unavailable. Keep any version explicitly pinned by the user or project; report incompatible pins instead of silently overriding them.

## Install a missing CLI

Official repository: https://github.com/art-shier/config-hub

Prebuilt targets: Linux amd64/arm64, macOS arm64, Windows amd64. Detect the platform and architecture (`uname -s; uname -m` on Unix; `$env:PROCESSOR_ARCHITECTURE` and `$env:PROCESSOR_ARCHITEW6432` on Windows). Unsupported targets need a separately agreed source build; do not substitute a different architecture.

Download the appropriate installer to a temporary directory, inspect it, then execute it. The official installers download GitHub Release archives and validate SHA-256, archive layout, and executable version before replacing the CLI. Do not bypass those checks. They install the CLI only, not a ConfigHub server.

### Linux / macOS

Requires Bash, curl, tar, and either sha256sum or shasum. Prefer the current user's directory so installation does not need sudo:

```bash
confighub_install_dir="$(mktemp -d)"
curl -fsSL https://raw.githubusercontent.com/art-shier/config-hub/main/scripts/install-cli.sh \
  -o "$confighub_install_dir/install-cli.sh"
cat "$confighub_install_dir/install-cli.sh"
```

After inspecting the downloaded script:

```bash
mkdir -p "$HOME/.local/bin"
/bin/bash "$confighub_install_dir/install-cli.sh" --install-dir "$HOME/.local/bin"
export PATH="$HOME/.local/bin:$PATH"
confighub version
confighub --help
```

Without `--version`, the installer selects the latest release. For a required release, add `--version vMAJOR.MINOR.PATCH` with the actual version. The PATH change above applies to the current shell; use the absolute executable path in separate agent shell calls if necessary. Modify shell startup files only if persistent PATH setup is part of the user's request.

### Windows PowerShell

```powershell
$confighubInstallerPath = Join-Path ([IO.Path]::GetTempPath()) ("confighub-install-" + [guid]::NewGuid() + ".ps1")
Invoke-WebRequest https://raw.githubusercontent.com/art-shier/config-hub/main/scripts/install-cli.ps1 -OutFile $confighubInstallerPath
Get-Content -Raw $confighubInstallerPath
```

After inspecting the downloaded script:

```powershell
Unblock-File -LiteralPath $confighubInstallerPath
& $confighubInstallerPath
confighub version
confighub --help
```

If local execution policy blocks the reviewed installer, use `Set-ExecutionPolicy -Scope Process Bypass -Force` for this process and retry; do not override organization-enforced policy. Default installation is `%LOCALAPPDATA%\ConfigHub\bin`; the installer updates user PATH and the current PowerShell PATH. Other existing terminals may need restarting. Use `& "$env:LOCALAPPDATA\ConfigHub\bin\confighub.exe" version` to check the executable directly. A custom absolute directory uses `-InstallDir C:\Tools\ConfigHub`; a required release uses `-Version vMAJOR.MINOR.PATCH` with the actual version.

### Verify and recover

After installation or upgrade, rerun `confighub version` and the intended command's help. If the latest published release still lacks that command, report that a supporting CLI release is needed; do not repeatedly reinstall or pretend the operation succeeded. If downloads fail or checksums do not match, stop installation and report the specific failure. Restricted networks may need the same official release assets supplied through an approved channel.

## Configure access

Use the server identified above and a machine token scoped to the intended project/environment. An administrator issues tokens and grants in the Web **Machine Access** page. Read operations need a read grant; `set` and `unset` need a write grant. A token does not allow managing users, projects, memberships, or machine identities.

Reuse an existing credential source: `CONFIGHUB_TOKEN`, a restricted file passed with `--token-file`, or existing CLI configuration. If no credential is available, ask the user to provision one locally; do not request that they paste a token into chat or put it in the skill URL. Use `confighub config show` for masked diagnostics; `confighub config get token` prints the plaintext token and must not be used for diagnostic output.

Set `CONFIGHUB_URL` to the intended base URL, or use the explicit `--server` flag to avoid a conflicting saved server address. For example, with the token already provisioned:

```bash
confighub --server https://config.example.com config show
confighub --server https://config.example.com pull --project shop --env production --dir ./config --format json
```

Replace example addresses and slugs with the user's actual target. Use HTTPS for remote production credentials. Do not print tokens or configuration secrets in command logs or final replies.

## Choose the operation

### Pull files for application startup

```bash
confighub pull --project shop --env production --dir ./config --format json
confighub pull --project shop --env production --dir . --format env --filename .env.production
confighub pull --project shop --env production --service api --dir ./config --format yaml
```

- `--project`, `--env`, and `--dir` are required. Missing directories are created.
- Formats: `json` (default), `jsonc`, `env` / `dotenv`, `yaml` / `yml`. Default filenames: `config.json`, `config.jsonc`, `.env`, `config.yaml` / `config.yml`.
- `--filename` is a basename, not a path. `--service` is an optional server-side service filter.
- JSON/JSONC/YAML contain only flat configuration key/value pairs, all strings. JSONC is valid JSON without added comments.
- ENV uses quoted dotenv syntax. Use a compatible dotenv loader, not shell execution; variable expansion differs by loader.
- Existing files are protected. Use `--force` only when replacing the target file is intended. Pull/encoding failure preserves the old file. Non-overwrite writes require a filesystem supporting hard links.
- Keep generated secret files out of version control. Pull does not modify the application's loader: configure it to read the generated file if requested, and launch only after a successful pull.

### Export or inject

```bash
confighub export --project shop --env production --format json
confighub export --project shop --env production --format dotenv
confighub run --project shop --env production -- npm start
```

`export` writes to stdout and may expose secrets in tool output. Its JSON contains project, environment, revision, and a `values` map; it is not the same shape as `pull` JSON. Prefer `pull` for application files. `run` fetches before launching, overrides same-named child environment variables, and does not start the child if fetching fails.

### Update a requested key

```bash
confighub set --project shop --env production --service api PORT=8080
confighub unset --project shop --env production LEGACY_FLAG
```

Perform writes only for the user's requested changes. `set` splits at the first `=`. Omitting `--service` preserves an existing key's service; a new key is global. `unset` deletes the key and accepts no service flag. Values passed to `set` are command-line arguments visible to process inspection/history; do not use that path to handle secrets when those surfaces cannot be kept private.

On revision conflict, inspect current state and reconcile with the user's intended change before retrying; never blindly retry a write. Authentication or authorization failures require correcting the token/grant, not creating a different identity or changing unrelated permissions.

## Report results

For pull, report the output path, format, and revision without values. For writes, report the affected key and returned revision. CLI exit codes are `0` success, `2` invalid usage/local configuration, and `1` request/runtime failure; `run` also propagates the child's exit code. Do not claim a file was generated or a remote value changed if the command failed.
