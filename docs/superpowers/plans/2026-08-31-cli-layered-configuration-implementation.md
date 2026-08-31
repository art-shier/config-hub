# ConfigHub CLI Layered Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add strict local/global CLI configuration files for `server` and `token`, field-level merging and inspection commands, while allowing remote HTTP Server URLs and preserving existing flag, environment, and Token-file behavior.

**Architecture:** A focused loader discovers two platform-aware paths, securely decodes each YAML file, merges fields while retaining provenance, then applies environment and flag overrides before `export`, `run`, or `config` commands consume the result. The HTTP client remains unaware of files and receives only the resolved Server and Token; platform files provide the existing no-follow/non-reparse opening boundary.

**Tech Stack:** Go 1.25, Cobra 1.9, `gopkg.in/yaml.v3`, Unix syscalls, `golang.org/x/sys/windows`, Go tests/race detector, Windows-native tests, and Darwin arm64 cross-compilation.

**Spec:** `docs/superpowers/specs/2026-08-31-cli-layered-configuration-design.md`

## Global Constraints

- Local file: exactly `<current-working-directory>/.confighub.yaml`; never search parents.
- Global file: `filepath.Join(os.UserConfigDir(), "confighub", "config.yaml")`.
- Schema: one YAML document with only optional, non-empty string fields `server` and `token`; maximum file size 16 KiB.
- Merge: load global, overlay fields present in local; every existing invalid file is fatal even when a higher source overrides it.
- Server priority: `--server > CONFIGHUB_URL > local > global`.
- Token priority: `--token-file > CONFIGHUB_TOKEN > local > global`; never add plaintext `--token` or YAML `token_file`.
- `--project`, `--env`, `--service`, and format remain per-call arguments.
- `config show` masks Token; only explicit `config get token` prints it.
- Accept `http` and `https` on every otherwise-valid host; retain all other URL and redirect protections.
- Preserve Linux amd64/arm64, Darwin arm64, and Windows amd64.
- Work directly on `main`, do not use subagents/worktrees, and leave `.coder-studio/` untouched.
- `version` and help-only paths must not load configuration.

## File Map

- Create `internal/cli/config_file.go`: schema, strict decode, merge, provenance/status types.
- Create `internal/cli/config_loader.go`: path discovery and global/local secure loading.
- Create `internal/cli/connection_config.go`: environment/flag overlays and final required-value checks.
- Create `internal/cli/config_command.go`: `config path/show/get` and stable output.
- Create matching tests: `config_file_test.go`, `config_loader_test.go`, `config_loader_unix_test.go`, `connection_config_test.go`, `config_command_test.go`.
- Modify `token_file_unix.go` and `token_file_windows.go` to share their safe opener with config files.
- Modify `root.go`, `export_test.go`, and `run_test.go` to use one lazy connection resolver.
- Modify `client.go` and `client_test.go` to permit remote HTTP.
- Modify `.gitignore` and `README.md` for user-facing behavior and warnings.

---

### Task 1: Strict YAML Schema and Field Merge

**Files:**
- Create: `internal/cli/config_file.go`
- Create: `internal/cli/config_file_test.go`

**Interfaces:**
- Produces: `decodeCLIConfig(contents []byte) (cliFileConfig, error)`.
- Produces: `mergeCLIConfig(snapshot *configSnapshot, layer cliFileConfig, source configSource)`.
- Produces: `configSnapshot`, `configValue`, `configSource`, and `configFileStatus` for later tasks.

- [ ] **Step 1: Write the failing decoder table**

```go
func TestDecodeCLIConfigAcceptsOnlyOptionalServerAndToken(t *testing.T) {
	tests := []struct {
		name       string
		contents   string
		wantServer *string
		wantToken  *string
		wantErr    bool
	}{
		{name: "both", contents: "server: https://config.example.com\ntoken: ch_project_a\n", wantServer: stringPointer("https://config.example.com"), wantToken: stringPointer("ch_project_a")},
		{name: "server only", contents: "server: https://config.example.com\n", wantServer: stringPointer("https://config.example.com")},
		{name: "token only", contents: "token: ch_project_a\n", wantToken: stringPointer("ch_project_a")},
		{name: "unknown", contents: "profile: project-a\n", wantErr: true},
		{name: "duplicate", contents: "server: https://one.example\nserver: https://two.example\n", wantErr: true},
		{name: "second document", contents: "server: https://one.example\n---\ntoken: ch_secret\n", wantErr: true},
		{name: "empty server", contents: "server: ''\n", wantErr: true},
		{name: "empty token", contents: "token: ''\n", wantErr: true},
		{name: "non-string", contents: "token: 123\n", wantErr: true},
		{name: "token whitespace", contents: "token: 'ch secret'\n", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := decodeCLIConfig([]byte(test.contents))
			if (err != nil) != test.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if !equalStringPointers(config.Server, test.wantServer) ||
				!equalStringPointers(config.Token, test.wantToken) {
				t.Fatalf("config=%+v", config)
			}
		})
	}
}
```

`TestDecodeCLIConfigEnforcesTokenSize` builds `"token: "+strings.Repeat("a", size)+"\n"` for sizes 4096 and 4097, requiring success and `errInvalidCLIConfig` respectively.

- [ ] **Step 2: Run the focused tests and verify RED**

```powershell
go test ./internal/cli -run '^TestDecodeCLIConfig' -count=1
```

Expected: build failure for missing decoder/types.

- [ ] **Step 3: Implement schema and strict decoding**

```go
const (
	maxCLIConfigBytes  = 16 * 1024
	maxConfigTokenBytes = 4096
)

var errInvalidCLIConfig = errors.New("invalid CLI configuration")

type cliFileConfig struct {
	Server *string `yaml:"server"`
	Token  *string `yaml:"token"`
}

func decodeCLIConfig(contents []byte) (cliFileConfig, error) {
	if len(contents) > maxCLIConfigBytes {
		return cliFileConfig{}, errInvalidCLIConfig
	}
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return cliFileConfig{}, nil
		}
		return cliFileConfig{}, errInvalidCLIConfig
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return cliFileConfig{}, errInvalidCLIConfig
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return cliFileConfig{}, errInvalidCLIConfig
	}
	root := document.Content[0]
	seen := make(map[string]bool, 2)
	config := cliFileConfig{}
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" ||
			value.Kind != yaml.ScalarNode || value.Tag != "!!str" || seen[key.Value] {
			return cliFileConfig{}, errInvalidCLIConfig
		}
		seen[key.Value] = true
		copy := value.Value
		switch key.Value {
		case "server":
			config.Server = &copy
		case "token":
			config.Token = &copy
		default:
			return cliFileConfig{}, errInvalidCLIConfig
		}
	}
	if config.Server != nil {
		if *config.Server == "" {
			return cliFileConfig{}, errInvalidCLIConfig
		}
		if _, err := validateBaseURL(*config.Server); err != nil {
			return cliFileConfig{}, errInvalidCLIConfig
		}
	}
	if config.Token != nil &&
		(len(*config.Token) > maxConfigTokenBytes || !validToken(*config.Token)) {
		return cliFileConfig{}, errInvalidCLIConfig
	}
	return config, nil
}
```

The node walk must explicitly reject duplicate `server`/`token` keys; do not assume `KnownFields(true)` handles duplicates. Return only `errInvalidCLIConfig`, never raw YAML errors.

- [ ] **Step 4: Write failing field merge/provenance test**

```go
func TestMergeCLIConfigOverlaysOnlyPresentFields(t *testing.T) {
	snapshot := configSnapshot{}
	mergeCLIConfig(&snapshot, cliFileConfig{
		Server: stringPointer("https://config.example.com"),
		Token:  stringPointer("ch_global"),
	}, configSource{Kind: sourceGlobal, Path: "/config/global.yaml"})
	mergeCLIConfig(&snapshot, cliFileConfig{
		Token: stringPointer("ch_local"),
	}, configSource{Kind: sourceLocal, Path: "/workspace/project/.confighub.yaml"})

	if snapshot.Server.Value != "https://config.example.com" ||
		snapshot.Server.Source.Path != "/config/global.yaml" {
		t.Fatalf("server=%+v", snapshot.Server)
	}
	if snapshot.Token.Value != "ch_local" ||
		snapshot.Token.Source.Path != "/workspace/project/.confighub.yaml" {
		t.Fatalf("token=%+v", snapshot.Token)
	}
}
```

- [ ] **Step 5: Run the merge test and verify RED**

```powershell
go test ./internal/cli -run '^TestMergeCLIConfig' -count=1
```

Expected: build failure for missing snapshot/source types.

- [ ] **Step 6: Implement stable types and merge**

```go
type configSourceKind string

const (
	sourceNone       configSourceKind = "none"
	sourceGlobal     configSourceKind = "global"
	sourceLocal      configSourceKind = "local"
	sourceEnvironment configSourceKind = "environment"
	sourceServerFlag configSourceKind = "--server"
	sourceTokenFile  configSourceKind = "--token-file"
)

type configSource struct {
	Kind configSourceKind
	Path string
}

type configValue struct {
	Value   string
	Present bool
	Source  configSource
}

type configFileState string

const (
	configMissing     configFileState = "missing"
	configLoaded      configFileState = "loaded"
	configUnavailable configFileState = "unavailable"
)

type configFileStatus struct {
	Path  string
	State configFileState
}

type configSnapshot struct {
	Server configValue
	Token  configValue
	Global configFileStatus
	Local  configFileStatus
}
```

`mergeCLIConfig` replaces value/source only for non-nil fields.

- [ ] **Step 7: Format and run Task 1 tests**

```powershell
gofmt -w internal/cli/config_file.go internal/cli/config_file_test.go
go test ./internal/cli -run '^(TestDecodeCLIConfig|TestMergeCLIConfig)' -count=1
```

Expected: selected tests pass.

- [ ] **Step 8: Commit**

```powershell
git add internal/cli/config_file.go internal/cli/config_file_test.go
git commit -m "feat: parse layered CLI configuration"
```

### Task 2: Secure Discovery and Layer Loading

**Files:**
- Create: `internal/cli/config_loader.go`
- Create: `internal/cli/config_loader_test.go`
- Create: `internal/cli/config_loader_unix_test.go`
- Modify: `internal/cli/token_file_unix.go`
- Modify: `internal/cli/token_file_windows.go`

**Interfaces:**
- Consumes Task 1 decoder/types.
- Produces `configLocations` and `loadCLIConfig(configLocations) (configSnapshot, error)`.

- [ ] **Step 1: Write failing real-file path and merge test**

```go
func TestLoadCLIConfigDiscoversAndMergesGlobalAndCurrentDirectory(t *testing.T) {
	workingDir := t.TempDir()
	userConfigDir := t.TempDir()
	globalPath := filepath.Join(userConfigDir, "confighub", "config.yaml")
	localPath := filepath.Join(workingDir, ".confighub.yaml")
	writeCLIConfig(t, globalPath, "server: https://config.example.com\n", 0o644)
	writeCLIConfig(t, localPath, "token: ch_project_a\n", 0o600)

	snapshot, err := loadCLIConfig(configLocations{
		Getwd: func() (string, error) { return workingDir, nil },
		UserConfigDir: func() (string, error) { return userConfigDir, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Server.Value != "https://config.example.com" ||
		snapshot.Token.Value != "ch_project_a" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.Global.Path != globalPath || snapshot.Global.State != configLoaded ||
		snapshot.Local.Path != localPath || snapshot.Local.State != configLoaded {
		t.Fatalf("global=%+v local=%+v", snapshot.Global, snapshot.Local)
	}
}
```

Implement these exact additional tests: `TestLoadCLIConfigUsesGlobalOnly`, `TestLoadCLIConfigUsesLocalOnly`, `TestLoadCLIConfigMarksBothMissing`, `TestLoadCLIConfigMarksGlobalUnavailable`, `TestLoadCLIConfigRejectsInvalidGlobalBeforeLocalOverride`, `TestLoadCLIConfigRejectsInvalidLocal`, and `TestLoadCLIConfigRejectsOversizedFile`. Each injects temporary `Getwd`/`UserConfigDir` functions, asserts both `configFileStatus` values, and declares `var loadErr *configLoadError` before calling `errors.As(err, &loadErr)` to assert the failing layer/path without matching any secret value.

- [ ] **Step 2: Run loader tests and verify RED**

```powershell
go test ./internal/cli -run '^TestLoadCLIConfig' -count=1
```

Expected: build failure for missing loader/location interfaces.

- [ ] **Step 3: Refactor the platform opener**

On Unix, rename the current opener and retain the wrapper exactly as:

```go
func openRestrictedFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

func openTokenFile(path string) (*os.File, error) {
	return openRestrictedFile(path)
}
```

On Windows, rename the existing `openTokenFile` implementation to `openRestrictedFile` without changing its `filepath.Abs`, drive-letter, ADS, `CreateFile`, `FILE_FLAG_OPEN_REPARSE_POINT`, attribute, handle-close, or `os.NewFile` steps; then add the same two-line `openTokenFile` wrapper shown above. This is a symbol extraction, not a rewrite of the Windows checks.

Keep `tokenFilePermissionsValid` unchanged. Generalize internal error strings from “token file” to “file”; callers already map errors to safe messages.

- [ ] **Step 4: Implement loader paths and safe reads**

```go
type configLocations struct {
	Getwd         func() (string, error)
	UserConfigDir func() (string, error)
}

func defaultConfigLocations() configLocations {
	return configLocations{Getwd: os.Getwd, UserConfigDir: os.UserConfigDir}
}
```

`loadCLIConfig` must:

1. resolve `filepath.Abs(filepath.Join(cwd, ".confighub.yaml"))`;
2. resolve global `filepath.Join(userDir, "confighub", "config.yaml")` or mark unavailable;
3. load global then local;
4. use `openRestrictedFile` and `io.LimitReader(maxCLIConfigBytes+1)`;
5. treat only `os.ErrNotExist` as missing;
6. require a regular file;
7. require `tokenFilePermissionsValid(info.Mode())` only when decoded `Token != nil`;
8. return typed `configLoadError{Layer, Path}` that never wraps YAML or values.

- [ ] **Step 5: Write Unix security and exact-directory tests**

Create a `//go:build !windows` file covering:

```go
func TestLoadCLIConfigRejectsReadableTokenConfig(t *testing.T) {
	dir := t.TempDir()
	writeCLIConfig(t, filepath.Join(dir, ".confighub.yaml"), "token: ch_secret\n", 0o640)
	_, err := loadCLIConfig(configLocations{
		Getwd: func() (string, error) { return dir, nil },
		UserConfigDir: func() (string, error) { return t.TempDir(), nil },
	})
	if err == nil {
		t.Fatal("load succeeded")
	}
}
```

Implement `TestLoadCLIConfigAllowsReadableServerOnlyConfig`, `TestLoadCLIConfigAllowsRestrictedTokenConfig`, `TestLoadCLIConfigRejectsSymlink`, `TestLoadCLIConfigRejectsFIFOWithoutBlocking`, and `TestLoadCLIConfigDoesNotSearchParents`. The FIFO test creates the node with `syscall.Mkfifo(path, 0o600)` and runs the load synchronously; the parent-search test writes only `<parent>/.confighub.yaml`, injects `<parent>/child` as CWD, and requires local state `missing` plus no Token.

- [ ] **Step 6: Format and run loader plus existing Token tests**

```powershell
gofmt -w internal/cli/config_loader.go internal/cli/config_loader_test.go internal/cli/config_loader_unix_test.go internal/cli/token_file_unix.go internal/cli/token_file_windows.go
go test ./internal/cli -run '^(TestLoadCLIConfig|TestExecuteRejectsUnsafeToken|TestExecuteUsesToken)' -count=1
```

Expected: selected native tests pass; Unix-only cases run in WSL later.

- [ ] **Step 7: Run platform compilation**

Windows:

```powershell
& 'C:\Program Files\Go\bin\go.exe' test -count=1 ./internal/cli
```

Darwin arm64 from WSL, using a validated temporary directory rather than a fixed output path:

```bash
cd /mnt/c/Users/yeshaopeng/workspace/config-hub
temp_dir="$(mktemp -d /tmp/confighub-cli-darwin.XXXXXX)"
trap 'rm -rf -- "$temp_dir"' EXIT
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go test -c -o "$temp_dir/cli.test" ./internal/cli
```

Expected: Windows tests pass and Darwin test package compiles.

- [ ] **Step 8: Commit**

```powershell
git add internal/cli/config_loader.go internal/cli/config_loader_test.go internal/cli/config_loader_unix_test.go internal/cli/token_file_unix.go internal/cli/token_file_windows.go
git commit -m "feat: load local and global CLI configuration"
```

### Task 3: Runtime Priority and Inspection Commands

**Files:**
- Create: `internal/cli/connection_config.go`
- Create: `internal/cli/connection_config_test.go`
- Create: `internal/cli/config_command.go`
- Create: `internal/cli/config_command_test.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/export_test.go`
- Modify: `internal/cli/run_test.go`

**Interfaces:**
- Consumes `loadCLIConfig` and `configSnapshot`.
- Produces `resolveConnectionConfig(root, snapshot, serverFlag, tokenFile, getenv) (configSnapshot, error)`.
- Produces `newConfigCommand(...) *cobra.Command`.

- [ ] **Step 1: Write failing priority tests**

Create a snapshot containing local values and assert explicit flags beat environment and files:

```go
func TestResolveConnectionConfigAppliesRuntimePriority(t *testing.T) {
	snapshot := configSnapshot{
		Server: configValue{Value: "https://local.example", Present: true, Source: configSource{Kind: sourceLocal, Path: "/local"}},
		Token:  configValue{Value: "ch_local", Present: true, Source: configSource{Kind: sourceLocal, Path: "/local"}},
	}
	tokenPath := writeTokenFile(t, "preferred", "ch_file\n", 0o600)
	root, serverFlag, tokenFile := newResolverTestRoot(t, "https://flag.example", tokenPath)
	resolved, err := resolveConnectionConfig(root, snapshot, serverFlag, tokenFile, mapEnvironment(map[string]string{
		"CONFIGHUB_URL": "https://env.example",
		"CONFIGHUB_TOKEN": "ch_env",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Server.Value != "https://flag.example" || resolved.Server.Source.Kind != sourceServerFlag {
		t.Fatalf("server=%+v", resolved.Server)
	}
	if resolved.Token.Value != "ch_file" || resolved.Token.Source.Kind != sourceTokenFile {
		t.Fatalf("token=%+v", resolved.Token)
	}
}
```

Implement separate tests named `TestResolveConnectionConfigUsesEnvironmentBeforeFiles`, `TestResolveConnectionConfigUsesLocalBeforeGlobal`, `TestResolveConnectionConfigFallsBackToGlobal`, `TestResolveConnectionConfigTreatsEmptyEnvironmentAsAbsent`, `TestResolveConnectionConfigRejectsInvalidEnvironmentWithoutFallback`, `TestResolveConnectionConfigRejectsExplicitEmptyServerWithoutFallback`, and `TestResolveConnectionConfigRejectsInvalidTokenFileWithoutFallback`. Each asserts both final value and `configSourceKind`; rejection tests also assert no secret appears in `err.Error()`.

- [ ] **Step 2: Run resolver tests and verify RED**

```powershell
go test ./internal/cli -run '^TestResolveConnectionConfig' -count=1
```

Expected: build failure for the missing resolver.

- [ ] **Step 3: Implement one shared runtime overlay**

`resolveConnectionConfig` copies the snapshot, applies non-empty environment values, then changed persistent flags. It reads `--token-file` only when changed, validates each chosen value, and preserves path/status metadata. Add `requireConnectionConfig` for `export`/`run`; inspection may retain unset fields.

Delete old duplicate logic or keep `resolveServerURL`/`resolveToken` only as thin wrappers if a focused legacy test still calls them.

- [ ] **Step 4: Write failing exact command output tests**

Use an injected loader and byte-exact assertions:

```go
func TestConfigShowDisplaysSourcesAndMasksToken(t *testing.T) {
	snapshot := configSnapshot{
		Server: configValue{Value: "http://config.example.com", Present: true, Source: configSource{Kind: sourceGlobal, Path: "/home/alice/.config/confighub/config.yaml"}},
		Token:  configValue{Value: "ch_project_secret_7f2a", Present: true, Source: configSource{Kind: sourceLocal, Path: "/workspace/project/.confighub.yaml"}},
		Global: configFileStatus{Path: "/home/alice/.config/confighub/config.yaml", State: configLoaded},
		Local:  configFileStatus{Path: "/workspace/project/.confighub.yaml", State: configLoaded},
	}
	code, stdout, stderr := executeWithConfigLoader(t, []string{"config", "show"}, snapshot, nil)
	want := "server: http://config.example.com\n" +
		"server_source: global (/home/alice/.config/confighub/config.yaml)\n" +
		"token: ch_***************7f2a\n" +
		"token_source: local (/workspace/project/.confighub.yaml)\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
```

Also test:

- `config path` loaded/missing/unavailable output;
- `config show` unset fields and short Token masking;
- `config get server` raw line;
- `config get token` exact raw secret line;
- invalid key/extra argument gives exit 2 and empty stdout;
- writer error gives exit 1 without values;
- invalid local/global reports only layer/path.

- [ ] **Step 5: Run command tests and verify RED**

```powershell
go test ./internal/cli -run '^TestConfig' -count=1
```

Expected: failure because `config` is not registered.

- [ ] **Step 6: Implement command formatting**

Create a no-network Cobra group:

```go
func newConfigCommand(load configLoader, resolve runtimeConfigResolver, stdout io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use: "config", Short: "Inspect ConfigHub CLI configuration", Args: cobra.NoArgs,
	}
	command.AddCommand(newConfigPathCommand(load, stdout))
	command.AddCommand(newConfigShowCommand(load, resolve, stdout))
	command.AddCommand(newConfigGetCommand(load, resolve, stdout))
	return command
}
```

Use a checked writer shared with `version` semantics. Source labels are exactly `none`, `environment`, `--server`, `--token-file`, or `local/global (ABSOLUTE_PATH)`. Masking preserves `ch_` only when present and up to the last four characters, replacing the middle with `*`; fully mask Tokens too short to expose both safely.

- [ ] **Step 7: Add lazy loader injection to root execution**

```go
type configLoader func() (configSnapshot, error)

func Execute(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	return execute(ctx, args, getenv, stdout, stderr, func() (configSnapshot, error) {
		return loadCLIConfig(defaultConfigLocations())
	})
}
```

The unexported `execute` is the test seam. `export`, `run`, `config path`, `config show`, and `config get` call the loader lazily; `version` and help do not. Add safe typed diagnostics for `configLoadError` with layer/path only.

- [ ] **Step 8: Add export/run file fallback tests**

For `export`, inject an HTTP test Server and Token in a snapshot and assert the Bearer header and response. For `run`, inject a snapshot and assert fetched values reach the child. In both files, add cases proving flags/Token file and environment still override snapshot values. Call unexported `execute(..., configLoader)`; never write a real user-global config.

- [ ] **Step 9: Format and run the complete CLI package**

```powershell
gofmt -w internal/cli/connection_config.go internal/cli/connection_config_test.go internal/cli/config_command.go internal/cli/config_command_test.go internal/cli/root.go internal/cli/export_test.go internal/cli/run_test.go
go test ./internal/cli -count=1
```

Expected: all tests pass, including assertions that no plaintext `--token` exists.

- [ ] **Step 10: Commit**

```powershell
git add internal/cli/connection_config.go internal/cli/connection_config_test.go internal/cli/config_command.go internal/cli/config_command_test.go internal/cli/root.go internal/cli/export_test.go internal/cli/run_test.go
git commit -m "feat: inspect resolved CLI configuration"
```

### Task 4: Permit HTTP Without Weakening URL Validation

**Files:**
- Modify: `internal/cli/client.go`
- Modify: `internal/cli/client_test.go`

**Interfaces:**
- Keeps `validateBaseURL(raw string) (*url.URL, error)` unchanged.
- Expands accepted schemes to HTTP and HTTPS for all valid hosts.

- [ ] **Step 1: Make URL table tests express the new policy**

Add successful rows:

```go
{name: "remote http", baseURL: "http://config.example.com"},
{name: "remote http port", baseURL: "http://192.0.2.10:8080"},
{name: "remote http ipv6", baseURL: "http://[2001:db8::1]:8080"},
{name: "remote http prefix", baseURL: "http://gateway.example/config-hub"},
```

Keep failing rows for FTP, userinfo, query, fragment, invalid ports, malformed IPs, relative URLs, and dot path segments.

- [ ] **Step 2: Run the focused test and verify RED**

```powershell
go test ./internal/cli -run '^TestNewClientValidatesBaseURL$' -count=1
```

Expected: remote HTTP rows fail with invalid Server URL.

- [ ] **Step 3: Make the minimal scheme change**

```go
switch parsed.Scheme {
case "http", "https":
	return parsed, nil
default:
	return nil, errors.New("invalid URL")
}
```

Remove `isLoopbackHost` only if unused. Do not alter redirect, port, path, timeout, response-size, or diagnostic behavior.

- [ ] **Step 4: Run client and CLI regressions**

```powershell
gofmt -w internal/cli/client.go internal/cli/client_test.go
go test ./internal/cli -count=1
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```powershell
git add internal/cli/client.go internal/cli/client_test.go
git commit -m "feat: allow HTTP ConfigHub endpoints"
```

### Task 5: Documentation and Secret Hygiene

**Files:**
- Modify: `.gitignore`
- Modify: `README.md`

**Interfaces:** Documents exactly what Tasks 1–4 implement.

- [ ] **Step 1: Ignore only the repository-root local secret file**

Add under runtime configuration:

```gitignore
/.confighub.yaml
```

Do not add broad YAML or recursive patterns that hide test fixtures.

- [ ] **Step 2: Document layered files and priority**

Add the global Server example:

```yaml
# Linux: ~/.config/confighub/config.yaml
server: https://config.example.com
```

Add the project Token example:

```yaml
# <project>/.confighub.yaml
token: ch_一次性签发的机器Token
```

Document `chmod 600 .confighub.yaml`, Windows/macOS global paths, exact-current-directory lookup, field merge, and the two priority chains copied from Global Constraints.

- [ ] **Step 3: Document inspection and secret output**

Include:

```bash
confighub config path
confighub config show
confighub config get server
confighub config get token
```

State that `show` masks Token, `get token` emits the full secret to stdout, every consuming repository should ignore `.confighub.yaml`, and diagnostics never print config values.

- [ ] **Step 4: Document HTTP risk while retaining HTTPS deployment guidance**

State that CLI HTTP sends Bearer Token without transport encryption and should be used only where the operator accepts that risk. Do not relax Server `public_url`, reverse-proxy, or production deployment HTTPS guidance.

- [ ] **Step 5: Verify and commit docs**

```powershell
git diff --check
rg -n "\.confighub\.yaml|config path|config show|config get token|HTTP.*Token|--token-file.*CONFIGHUB_TOKEN" README.md .gitignore
git add README.md .gitignore
git commit -m "docs: explain layered CLI configuration"
```

Expected: diff check succeeds and every required behavior is present.

### Task 6: Cross-Platform and Full Verification

**Files:**
- Modify only files implicated by a failure, always test-first.

**Interfaces:** Verifies all release platforms and existing project behavior.

- [ ] **Step 1: Run formatting, focused tests, and diff checks**

```powershell
gofmt -w internal/cli/*.go
go test ./internal/cli -count=1
git diff --check
```

Expected: all exit 0.

- [ ] **Step 2: Run Windows-native CLI tests**

```powershell
& 'C:\Program Files\Go\bin\go.exe' test -count=1 ./internal/cli
```

Expected: Windows config paths, non-reparse file opening, config commands, and Runner tests pass.

- [ ] **Step 3: Run Linux and Darwin gates from WSL**

Use the existing Go 1.25 toolchain and validate the temporary directory before cleanup:

```bash
cd /mnt/c/Users/yeshaopeng/workspace/config-hub
go test -count=1 ./internal/cli
temp_dir="$(mktemp -d /tmp/confighub-cli-config-gate.XXXXXX)"
trap 'resolved="$(realpath -- "$temp_dir")"; case "$resolved" in /tmp/confighub-cli-config-gate.*) rm -rf -- "$resolved";; *) exit 1;; esac' EXIT
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go test -c -o "$temp_dir/cli.test" ./internal/cli
go version -m "$temp_dir/cli.test" | grep -F 'GOOS=darwin'
go version -m "$temp_dir/cli.test" | grep -F 'GOARCH=arm64'
```

Expected: Linux tests pass and Darwin metadata is `darwin/arm64`.

- [ ] **Step 4: Run the complete clean-clone repository gate**

```bash
bash -n scripts/*.sh scripts/tests/*.sh
bash scripts/tests/run.sh
shellcheck scripts/*.sh scripts/tests/*.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
./scripts/check.sh
```

Expected: Bash tests, ShellCheck, Actionlint, Go/race, frontend unit tests, production build, Playwright E2E, and runtime acceptance all pass. Report new exact frontend/E2E counts if they change.

- [ ] **Step 5: Inspect final repository state**

```powershell
git diff --check
git status --short --branch
git log --oneline -10
git tag --list v0.0.0
```

Expected: only intended commits on `main`; `.coder-studio/` remains untouched; no `v0.0.0` tag or Release is created.

- [ ] **Step 6: Handle verification failures test-first**

For any failure: add a focused regression test that reproduces it, run that test to see RED, make the smallest fix, rerun the focused test and complete gate, then commit only the implicated files. If all gates pass without edits, do not create an empty commit.
