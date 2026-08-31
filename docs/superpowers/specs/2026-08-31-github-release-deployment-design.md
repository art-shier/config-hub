# ConfigHub GitHub Release 与部署设计

## 目标

ConfigHub 通过 GitHub Actions 自动验证主分支，并在推送严格的 `vMAJOR.MINOR.PATCH` 标签时创建 GitHub Release。一次正式发布同时生成 Server 与 CLI 两类产品，但二者保持独立产物和独立部署入口：

- Server 是配置端，`confighub-server` 二进制内嵌 React/Vite Web，不存在独立 Web 服务或 Web 发布包；
- CLI 是读取端，只发布 `confighub` 客户端，不携带 Server 配置、systemd unit 或 Web 资源。

首版只支持 Linux `amd64` 和 Linux `arm64`。继续沿用单实例、本地磁盘 SQLite、外部 HTTPS 反向代理的生产架构，不引入 Docker、Kubernetes、自动反向代理配置、deb/rpm 或外部数据库。

## 发布触发与权限

仓库新增两个 GitHub Actions 工作流：

1. 主分支质量工作流在 pull request 和 `main` push 时运行现有完整质量门禁，包括 Go 格式、vet、race 测试、前端类型检查/单测/构建、浏览器 E2E、二进制构建和运行时验收。
2. Release 工作流仅由 `v*` 标签触发。工作流在写 Release 之前再次运行质量门禁，随后校验标签严格匹配 `v[0-9]+.[0-9]+.[0-9]+`、构建四个压缩包、生成统一校验文件，最后使用 GitHub CLI 创建 Release 并生成发布说明。

普通质量工作流只需要只读仓库权限。Release 工作流只给创建 Release 的作业 `contents: write`，其余权限保持最小化。发布失败时不创建部分成功的 Release；同名 Release 或资产已经存在时拒绝覆盖。

## 产物与版本

标签 `v1.2.3` 对应以下资产：

```text
config-hub-server_1.2.3_linux_amd64.tar.gz
config-hub-server_1.2.3_linux_arm64.tar.gz
config-hub-cli_1.2.3_linux_amd64.tar.gz
config-hub-cli_1.2.3_linux_arm64.tar.gz
checksums.txt
```

每个压缩包包含一个与压缩包同名的顶层目录，避免解压时污染当前目录。

Server 包内容：

```text
config-hub-server_1.2.3_linux_amd64/
├── confighub-server
├── config/
│   ├── config.example.yaml
│   └── users.example.yaml
└── deploy/
    └── confighub.service
```

CLI 包内容：

```text
config-hub-cli_1.2.3_linux_amd64/
└── confighub
```

`checksums.txt` 使用 SHA-256 覆盖四个压缩包。两个安装脚本只接受校验文件中与目标文件名完全匹配的一行，校验成功后才能解压和安装。SHA-256 用于发现下载损坏或资产不一致；传输和发布者身份继续依赖 GitHub HTTPS 与仓库权限。

构建固定使用 `GOOS=linux`、`CGO_ENABLED=0` 和 `GOARCH=amd64|arm64`，使用 `-trimpath` 并把完整标签（例如 `v1.2.3`）注入 `internal/buildinfo.Version`。构建前必须完成前端构建，使当前 Web 资源经现有 `go:embed` 路径进入 Server 二进制。

`confighub version` 和 `confighub-server version` 均向标准输出写一行注入后的完整版本。开发构建保持输出 `dev` 或现有本地构建版本，安装脚本用该命令验证落盘后的目标版本。

## 发布打包脚本

新增单一发布打包脚本负责：

1. 校验 Linux/Bash、工具链、标签格式、标签指向当前 `HEAD`，并拒绝脏工作树；
2. 构建 Web 资源并运行与发布构建直接相关的单元检查；
3. 对两个架构分别交叉编译 Server 和 CLI；
4. 按上述两个产品边界创建临时目录和压缩包；
5. 生成按文件名稳定排序的 `checksums.txt`；
6. 将最终资产写入专用的 `dist/release/`，不读取或打包运行时密钥、实际配置、数据库或备份。

脚本在临时目录中组装产物，失败时清理临时文件。最终文件在全部步骤成功后才移动到 `dist/release/`。压缩包内的普通文件权限固定，二进制为 `0755`，配置模板和 unit 为 `0644`。

## CLI 独立安装

`scripts/install-cli.sh` 是自包含的 Bash 安装器，可以从仓库下载后执行，不要求 clone 源码，也不依赖 Server 安装器。

接口：

```text
install-cli.sh [--version vMAJOR.MINOR.PATCH] [--install-dir DIRECTORY]
```

- 未指定版本时，通过 GitHub `releases/latest` 的 HTTPS 重定向解析最新正式版，不选择 prerelease；
- 默认安装目录为 `/usr/local/bin`，目录不可写时明确提示使用 `sudo` 或传入另一个绝对目录；
- 仅支持 Linux，`x86_64|amd64` 映射到 `amd64`，`aarch64|arm64` 映射到 `arm64`，其他系统和架构在下载前失败；
- 下载目标 CLI 压缩包与 `checksums.txt` 到权限受限的临时目录；
- 完成精确 SHA-256 校验和安全解压后，在目标目录内先写临时文件，再原子改名为 `confighub`；
- 安装结束执行目标文件的 `version` 命令并要求结果等于请求的标签；
- 不创建配置、服务用户、systemd unit、数据库目录或环境变量。

文档提供可审阅的两步安装方式，避免把远程脚本直接交给 shell：

```bash
curl -fsSLO https://raw.githubusercontent.com/art-shier/config-hub/main/scripts/install-cli.sh
less install-cli.sh
sudo bash install-cli.sh
```

同时记录适合已理解风险场景的一行形式，以及 `--version` 固定版本示例。

## Server 独立部署

`scripts/deploy-server.sh` 是自包含的 Server 部署器，不调用 CLI 安装器。它要求 Linux、root、systemd、`curl`、`sha256sum`、`tar`、`openssl` 和常见账号/文件安装工具。

接口：

```text
deploy-server.sh [--version vMAJOR.MINOR.PATCH]
                 [--public-url HTTPS_URL]
                 [--admin-username USERNAME]
                 [--admin-password-file FILE]
```

`--version` 默认解析最新正式版。`--public-url` 只在首次部署时必需，必须是不含控制字符的绝对 HTTPS URL。管理员用户名默认 `admin`，只接受稳定的 ASCII 用户名字符集。首次部署的管理员密码来源为：

- 有控制终端时从 `/dev/tty` 隐藏读取两次，并要求两次一致；
- 无控制终端时必须使用 `--admin-password-file`，该文件必须为普通文件且不能给 group/other 任何权限；
- 密码永不接受命令行明文参数，写入 YAML 前执行正确转义，禁止换行和 NUL。

首次部署执行以下操作：

1. 识别架构、解析版本、下载 Server 压缩包和校验文件并验证；
2. 创建不可登录的 `confighub` 系统用户和同名组；
3. 创建 `/etc/confighub`、`/var/lib/confighub`、`/var/backups/confighub`，所属人为 `confighub:confighub`，目录权限不宽于 `0700`；
4. 生成 `/etc/confighub/config.yaml`，固定监听 `127.0.0.1:8080`，写入提供的公开 HTTPS URL，默认只信任本机 IPv4/IPv6 代理，并使用绝对数据路径；
5. 生成只有一个启用管理员的 `/etc/confighub/users.yaml`，用 OpenSSL 生成 `/etc/confighub/session.key`；三个文件所属人为 `confighub:confighub` 且权限为 `0600`；
6. 原子安装 `/usr/local/bin/confighub-server` 和 `/etc/systemd/system/confighub.service`；
7. 执行 `systemctl daemon-reload`、`enable --now confighub.service`，轮询本机 readiness 端点；
8. 成功后只报告配置位置、服务状态命令和反向代理下一步，不回显管理员密码。

默认配置适用于反向代理与 ConfigHub 位于同一主机的场景。远程代理网段属于显式运维配置，管理员部署后编辑 `trusted_proxy_cidrs` 并重启服务；脚本不自动安装或修改 Nginx/Caddy。

## Server 升级与幂等性

部署脚本检测到现有受管安装时进入升级路径：

1. 校验当前二进制、配置文件、systemd unit 和运行目录均为预期的明确路径；
2. 下载并校验目标版本，不修改已有 `config.yaml`、`users.yaml`、`session.key`、SQLite 文件或备份；
3. 使用当前已安装的 Server 二进制和当前配置执行在线备份，备份失败则在替换任何文件之前中止；
4. 在 `/usr/local/lib/confighub/` 保存一份上一版二进制，随后原子替换目标二进制和更新受管 unit；
5. daemon-reload、重启服务并轮询 readiness；
6. readiness 失败时保留新二进制、上一版二进制和升级前备份，输出诊断命令与精确路径，不自动恢复 SQLite 或混用 WAL/SHM。

重复部署同一版本是安全的：配置和数据仍不覆盖，脚本确认版本与 readiness 后可以结束；如果受管 unit 有更新，可以更新 unit 并重启，但不重复创建用户或重置密钥。

自动回滚数据库不在脚本范围内，因为新版本可能已经完成不可逆迁移。运维人员按现有停机恢复流程评估并恢复升级前备份。上一版二进制只保留一份，避免无限增长。

## systemd 服务

Server 发布包包含 `deploy/confighub.service`，部署脚本将其安装到 `/etc/systemd/system/confighub.service`。unit 的稳定约束为：

- `User=confighub`、`Group=confighub`、`UMask=0077`；
- 前台执行 `/usr/local/bin/confighub-server serve --config /etc/confighub/config.yaml`；
- `ExecReload` 向主进程发送 `SIGHUP`，复用现有安全账号同步行为；
- `Restart=on-failure`，正常 SIGTERM 继续走应用现有优雅退出；
- 配置目录只读，只有 `/var/lib/confighub` 和 `/var/backups/confighub` 可写；
- 启用适合 Go/SQLite 网络服务的 systemd 沙箱选项，但不限制本机 IPv4/IPv6、Unix 文件访问或 SQLite 所需系统调用；
- 日志写入 journal，不创建额外日志文件。

unit 不拥有 TLS、证书或反向代理职责。

## 错误处理与安全边界

两个安装器均使用 `set -euo pipefail`、权限受限的 `mktemp -d` 和退出清理 trap。版本、系统、架构、依赖、目标目录和下载结果在发生文件替换前校验。归档解压前检查成员路径，只接受设计中列出的相对路径和文件类型，拒绝绝对路径、`..`、符号链接和额外可执行文件。

所有 URL 由固定 GitHub 仓库、已验证版本和已验证产品/架构拼接，不接受任意下载主机。curl 启用失败即退出、HTTPS 和重定向；安装器不会关闭 TLS 验证。

Server 部署器只管理列出的固定路径，不递归删除宽泛目录。配置和数据不属于可覆盖的发布产物。升级中断后，临时下载可清理，已有服务与数据保持原位；只有通过校验后的二进制才可能进入安装路径。

## 测试与验证

实现遵循测试先行：

- Go 单测覆盖两个 `version` 命令的标准输出、开发版本和参数错误；
- Bash 测试覆盖严格版本格式、最新版解析、系统/架构映射、资产命名、校验缺失、校验不匹配、安全归档检查和 CLI 原子安装；
- Server 部署测试使用临时根目录与受控命令替身验证首次部署文件布局、权限意图、配置不覆盖、密码文件权限拒绝、升级前备份门禁、同版本幂等和 readiness 失败报告，不接触开发机真实 systemd、账号或 `/etc`；
- 打包验收实际交叉编译四个二进制，检查归档成员边界、文件模式、注入版本和统一 checksums；
- 工作流 YAML 通过 actionlint，所有 Bash 文件通过 `bash -n` 和 shellcheck；
- GitHub 主分支工作流运行 `scripts/check.sh` 与新增发布/部署测试；
- Release 工作流在上传前再次列出并校验全部五个资产，缺少任一产品或架构即失败。

本地最终验收至少包括新增脚本测试、相关 Go 测试、完整 `scripts/check.sh` 和一次不发布资产的本地打包演练。真实 GitHub Release 只能在推送正式标签后由 GitHub 环境完成；文档给出首次发布的验证清单。

## 文档与运维流程

README 新增以下内容：

- `main`/PR 检查与 `vX.Y.Z` 发布规则；
- GitHub Release 中 Server/CLI 两类资产的区别；
- CLI 最新版、固定版本和自定义目录安装；
- Server 首次部署、非交互密码文件、服务状态、journal、readiness 与账号 SIGHUP；
- Server 原地升级、升级前备份、失败后的人工恢复边界；
- 外部 HTTPS 反向代理仍需管理员配置；
- CLI 卸载只移除目标二进制；Server 卸载先停止/禁用 unit，再移除受管二进制和 unit，配置、数据库与备份默认保留，只有管理员明确决定后才单独处理数据。

发布者流程为：确认 `main` 完整通过，创建并推送正式标签，等待 Release 工作流成功，然后从全新 Linux amd64/arm64 环境分别验证 CLI 安装和 Server 部署。任何失败使用新的补丁版本标签修复，不覆盖已经公开的版本资产。
