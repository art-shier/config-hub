# ConfigHub CLI 跨平台发布设计

## 目标与范围

ConfigHub 在现有 Linux Release 与部署能力上增加 macOS 和 Windows CLI。Server 与内嵌 Web 的产品边界、Linux 目标、systemd 部署脚本和运行路径保持不变。

CLI 的受支持目标固定为：

- Linux `amd64`；
- Linux `arm64`；
- macOS（Go `darwin`）`arm64`；
- Windows `amd64`。

macOS Intel 和 Windows ARM64 不在本阶段范围内。CLI 只通过命令行脚本安装，安装后作为普通的 `confighub` 或 `confighub.exe` 命令使用，不提供安装向导、快捷方式、控制面板卸载项、Homebrew、Winget 或 MSI/PKG。

首版跨平台 CLI 不做 Apple Developer ID、公证或 Windows Authenticode 签名。发布资产继续依赖固定 GitHub 仓库的 HTTPS、严格版本标签、完整归档结构检查和 SHA-256 校验。文档明确说明未签名边界，但脚本安装流程在校验成功后处理互联网来源标记，避免无意义的 Gatekeeper 或 SmartScreen 提示；系统杀毒扫描不被禁用。

本设计扩展 `2026-08-31-github-release-deployment-design.md` 中的 CLI 与资产章节。未在本文修改的 Server、备份、升级、systemd 和保守卸载约束继续有效。

## Release 资产契约

标签 `v1.2.3` 精确对应以下七个 GitHub Release 资产：

```text
config-hub-server_1.2.3_linux_amd64.tar.gz
config-hub-server_1.2.3_linux_arm64.tar.gz
config-hub-cli_1.2.3_linux_amd64.tar.gz
config-hub-cli_1.2.3_linux_arm64.tar.gz
config-hub-cli_1.2.3_darwin_arm64.tar.gz
config-hub-cli_1.2.3_windows_amd64.zip
checksums.txt
```

`darwin` 是 Go 的正式 `GOOS` 名称，在文件名中稳定表示 macOS。Linux 与 macOS 使用 `.tar.gz`，Windows 使用 `.zip`。`checksums.txt` 以文件名稳定排序，精确覆盖六个归档，不包含自身。

Unix CLI 归档只包含：

```text
config-hub-cli_1.2.3_OS_ARCH/
└── confighub
```

Windows CLI 归档只包含：

```text
config-hub-cli_1.2.3_windows_amd64/
└── confighub.exe
```

两个 Server 归档的名称、成员、模式和 Linux-only 约束不变。没有独立 Web 归档，也不会在 Server 归档中加入 CLI。

本地发布目录、checksum manifest、GitHub draft Release 和远端公开 Release 都必须精确匹配这七个名称。缺少、重复、拼写错误或额外资产均使发布失败。同名 Release 继续拒绝覆盖。

## 集中式跨平台打包

`scripts/package-release.sh` 仍在 Ubuntu 上作为唯一资产组装入口。它在相同标签、提交和前端构建结果上完成：

1. Server：`GOOS=linux`，`GOARCH=amd64|arm64`；
2. CLI：`linux/amd64`、`linux/arm64`、`darwin/arm64`、`windows/amd64`；
3. 所有 Go 构建固定 `CGO_ENABLED=0`、`-trimpath`、`-buildvcs=false`，并通过 `-ldflags -X` 注入完整标签；
4. Unix 二进制命名为 `confighub` 且归档模式为 `0755`；Windows 二进制命名为 `confighub.exe`；
5. Unix 归档使用可复现的 GNU tar/gzip 参数，Windows ZIP 清除多余元数据并固定成员布局；
6. 全部六个归档成功后才生成 `checksums.txt` 并替换 `dist/release`。

打包接口显式区分产品、操作系统、架构、二进制名和归档类型。Server 只接受两个 Linux 目标；CLI 只接受上面列出的四个目标。不能通过任意字符串组合生成未支持资产。

发布脚本的预期资产清单从五个文件扩展为七个文件，成功消息不再写死旧数量。上传仍使用 draft 事务：创建 draft、上传七个明确路径、读取并比较远端资产、最后公开；任何失败只删除本次创建且仍为 draft 的 Release。

## Linux 与 macOS Bash 安装器

`scripts/install-cli.sh` 从 Linux-only 安装器扩展为 Linux/macOS 共用安装器，命令行接口保持：

```text
install-cli.sh [--version vMAJOR.MINOR.PATCH] [--install-dir ABSOLUTE_DIRECTORY]
```

脚本必须兼容 macOS 系统自带的 `/bin/bash` 3.2，不要求 Homebrew Bash。生产脚本和共享 Bash 测试不得使用 `mapfile`、关联数组、`${name,,}` 或其他 Bash 4+ 语法。

平台映射为：

- `Linux x86_64|amd64` → `linux_amd64`；
- `Linux aarch64|arm64` → `linux_arm64`；
- `Darwin arm64|aarch64` → `darwin_arm64`；
- 其他组合在网络访问前失败。

默认安装路径继续是 `/usr/local/bin/confighub`。用户可以用 `sudo` 写入默认目录，也可以通过绝对 `--install-dir` 安装到自己的目录。脚本不修改 shell profile 或 Unix `PATH`。

安装器保持固定 GitHub 仓库、最新正式版重定向解析、严格标签、受限临时目录、精确 checksum 行、严格归档成员和同目录原子替换。为兼容 macOS：

- SHA-256 优先使用 `sha256sum`，不存在时使用系统自带 `shasum -a 256`；
- tar 列表、类型检查和解压不依赖 GNU-only 选项；
- 文件安装和临时目录创建只使用 Linux 与 macOS 均支持的参数；
- 归档解压后只把经过验证的普通文件复制到同目录暂存目标，不恢复归档所有者或宽权限。

完成 SHA-256 与成员检查后，脚本检查暂存二进制的 `com.apple.quarantine` 扩展属性。只有属性存在时才调用 `xattr -d` 删除它；Linux 不执行此步骤。随后执行暂存二进制的 `version`，要求输出等于请求标签，再原子替换现有目标并复验。校验或版本验证失败时，已有 CLI 字节保持不变。

## Windows PowerShell 安装器

新增 `scripts/install-cli.ps1`，兼容系统自带 Windows PowerShell 5.1 和 PowerShell 7+。公开接口为：

```powershell
.\install-cli.ps1
.\install-cli.ps1 -Version v1.2.3
.\install-cli.ps1 -InstallDir C:\Tools\ConfigHub
```

默认安装目标为：

```text
%LOCALAPPDATA%\ConfigHub\bin\confighub.exe
```

默认和自定义安装目录都必须解析为本机绝对文件系统路径。安装成功后，脚本按不区分大小写的 Windows 路径语义去重，将目录加入用户级 `PATH`，并同步更新当前 PowerShell 进程的 `PATH`。用户无需管理员权限。当前 PowerShell 会话可以立即解析 `confighub`；其他已经打开的终端需要重新打开。

安装流程为：

1. 校验参数、Windows 操作系统和 `AMD64` 进程/系统架构；
2. 从固定 `https://github.com/art-shier/config-hub` 解析最新正式标签，或校验提供的严格标签；
3. 只下载对应 ZIP 和 `checksums.txt` 到随机的用户临时目录；
4. 使用 `Get-FileHash -Algorithm SHA256` 对精确且唯一的 manifest 项做不区分大小写比较；
5. 通过 .NET ZIP API 枚举全部成员，只接受预期顶层目录和一个 `confighub.exe`，拒绝绝对路径、父目录穿越、重复、额外成员和特殊链接语义；
6. 只把预期文件流复制到安装目录内的随机暂存文件，不调用宽泛的递归解压；
7. SHA-256 和成员验证成功后，对暂存文件调用 `Unblock-File`，只移除该文件的 `Zone.Identifier`/Mark-of-the-Web；
8. 执行暂存文件的 `version` 并校验标签；
9. 使用 Windows 原子替换能力将暂存文件移动为 `confighub.exe`，复验后再更新用户 `PATH`；
10. 成功和失败均清理下载与暂存文件，不递归删除安装目录。

原子替换必须覆盖“目标不存在”和“目标已存在”两种情况。任何校验、ZIP 解析、版本执行或替换错误都不能提前修改用户 `PATH`，也不能损坏已安装目标。脚本不写服务、快捷方式、文件关联、程序卸载注册表或其他产品配置。

PowerShell 安装脚本本身的文档流程为：下载到本地、用 `Get-Content` 审阅、对已审阅脚本执行 `Unblock-File`、再调用脚本。另可记录适合已经理解远程脚本风险的一行命令，但不把图形安装器作为入口。

## 来源标记与未签名边界

macOS quarantine 和 Windows Mark-of-the-Web 是下载来源元数据，不是二进制内容，也与是否存在安装界面无关。未签名程序携带这些标记时，Gatekeeper 或 SmartScreen 可能在首次执行时提示。

安装器只在以下条件全部满足后处理目标二进制的来源标记：

1. 下载 URL 来自固定仓库与已验证标签；
2. 归档 SHA-256 与唯一 manifest 项一致；
3. 归档成员与目标平台布局完全一致；
4. 目标只是安装目录内的随机暂存普通文件。

来源标记处理后立即执行版本检查。脚本不改变系统级 Gatekeeper、SmartScreen、Defender、杀毒软件或执行策略配置，也不为其他文件批量解除标记。若操作系统或安全软件仍拒绝执行，安装失败并保留旧版本。

代码签名和 Apple 公证需要外部证书、密钥保护与供应链权限，明确留待后续独立设计；本阶段不创建占位 secret 或弱化签名校验的分支。

## GitHub Actions 流水线

采用“集中打包、原生验收、统一发布”的单条 Release 流水线。

### Quality

Ubuntu `quality` 作业运行现有完整 `scripts/check.sh`、ShellCheck、Actionlint、systemd unit 测试和发布资产行为测试。它不授予 Release 写权限。

### Package

Ubuntu `package` 作业只在 quality 成功后运行，检出完整标签历史，调用 `scripts/package-release.sh` 生成七个文件，重新比较精确资产清单并上传为内部 Actions artifact。内部 artifact 不是公开 Release，不能跳过后续原生验收。

### Native CLI smoke

两个并行原生作业下载相同内部 artifact：

- `macos-14`：GitHub 标准 Apple Silicon M1/arm64 runner。使用系统 `/bin/bash` 3.2 和 SHA-256 工具验证 manifest，检查 tar 成员，运行 `confighub version`，并运行 Bash 安装器行为测试；
- `windows-2025`：GitHub 标准 x64 runner。使用 PowerShell 验证 checksum 和 ZIP 成员，运行 `confighub.exe version`，分别用 Windows PowerShell 5.1 与 PowerShell 7+ 执行安装器行为测试。

原生作业必须验证 runner 架构与预期一致，不能在错误架构上跳过执行。它们不创建或修改 GitHub Release。

### Publish

Ubuntu `publish` 作业依赖 package 和两个原生 smoke 作业。它下载未经修改的内部 artifact，重新验证七个名称和六个 checksum，再调用 `scripts/publish-release.sh` 完成 draft 事务。只有 publish 作业拥有 `contents: write`。

PR 与 `main` 的 CI 保留现有 Ubuntu 完整门禁，并增加轻量的 `macos-14` 和 `windows-2025` CLI 作业。macOS 作业运行 Bash 安装器测试、CLI 单测和原生版本 smoke；Windows 作业运行 PowerShell 5.1/7 安装器测试、CLI 单测和原生版本 smoke。这样跨平台错误在普通开发提交中暴露，而不是等到正式标签。

## 测试设计

实现继续遵循测试先行，所有脚本测试断言实际退出状态、文件副作用和二进制输出，不只搜索源文本。

### 打包与发布测试

- 资产命名测试覆盖四个 CLI 目标、两个 Server 目标和非法组合；
- 真实临时标签打包生成六个归档加 checksum，验证每个成员、模式、ZIP 布局、Go 平台元数据和注入版本；
- 发布行为 fixture 精确包含七个文件，覆盖缺少 macOS、缺少 Windows、额外资产、manifest 不完整、上传失败和远端不一致；
- draft 清理仍只作用于当前进程创建的 Release。

### Bash 安装器测试

- 系统/架构映射覆盖 Linux 两架构、Darwin arm64 与所有拒绝组合；
- Linux 与 macOS checksum 工具边界均被执行；
- tar 成员、链接、穿越、额外文件和重复项被拒绝；
- quarantine 只在 checksum 与成员验证之后处理；
- 安装失败保留原目标，同目录暂存与成功替换可观察；
- macOS 原生 runner 执行真实 Darwin 二进制的版本检查。

### PowerShell 安装器测试

使用不依赖 Pester 的自包含断言脚本，以便 Windows PowerShell 5.1 和 PowerShell 7 运行同一测试：

- 严格标签、最新版重定向、Windows amd64 检测和参数拒绝；
- 精确 checksum、错误/缺失/重复 manifest 项；
- ZIP 穿越、绝对路径、重复、额外成员和非预期布局；
- 错误 hash 或版本时旧目标与用户 PATH 均不改变；
- `Unblock-File` 发生在完整校验之后；
- 成功安装执行真实 `confighub.exe version`，原子替换旧文件，并对用户 PATH 做不区分大小写的去重；
- 默认目录、自定义绝对目录和重复安装保持幂等。

### 完整验收

本地 Linux 验收完成脚本语法、ShellCheck、Actionlint、Go/前端完整门禁和真实交叉打包。Windows/macOS 的原生执行证据由 GitHub Actions 对同一内部 artifact 提供。首次正式跨平台 Release 发布后，还需从全新 Windows amd64 和 macOS arm64 终端按 README 命令各安装一次，核对 `confighub version`、PATH 和一个真实只读 API 调用。

## 文档与升级兼容性

README 的 Release 表从四个归档更新为六个归档，并分别提供 Linux、macOS 和 Windows 的下载、审阅、安装、固定版本、自定义目录、版本验证与卸载命令。

现有 Linux 文件名和安装命令保持兼容。Server 部署器仍只读取 Server Linux 资产，不受新增 CLI 文件影响。旧版本 Release 只有五个资产也不需要迁移；新发布脚本只对新标签要求七个资产，不修改或覆盖历史 Release。

CLI 升级继续通过重复运行对应平台安装脚本完成。Unix 卸载只删除实际安装的 `confighub`；Windows 卸载删除 `confighub.exe`，并由用户决定是否从用户 PATH 移除安装目录。脚本不自动删除包含其他文件的自定义目录。
