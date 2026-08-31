# ConfigHub CLI 分层配置设计

## 目标与范围

ConfigHub CLI 在保留现有命令行参数、环境变量和受限 Token 文件能力的基础上，增加跨平台的 YAML 配置文件。配置文件只保存 ConfigHub Server 地址和机器 Token；项目、环境、服务与输出格式仍由每次 `export` 或 `run` 调用显式传入，不进入配置文件。

本设计解决以下场景：

- 多个项目位于不同工作目录，各自使用不同机器 Token；
- 多个项目共享一个全局 Server 地址；
- 当前工作目录可以只覆盖全局配置中的一个字段；
- 用户能够查看配置候选路径、最终生效值和每个值的来源；
- CLI 可以连接任意合法主机上的 HTTP 或 HTTPS Server。

本阶段不增加命名 context/profile、父目录递归搜索、项目或环境默认值、配置写入命令、系统钥匙串集成，也不改变 Server 的 `public_url` HTTPS 约束。CLI 仍是只读配置客户端。

## 配置路径与格式

当前工作目录的本地配置固定为：

```text
<current-working-directory>/.confighub.yaml
```

CLI 只检查进程启动时的精确工作目录，不向父目录递归搜索。全局配置使用 Go `os.UserConfigDir()` 返回的目录，再追加 `confighub/config.yaml`。典型路径为：

```text
Linux:   $XDG_CONFIG_HOME/confighub/config.yaml
         或 $HOME/.config/confighub/config.yaml
macOS:   $HOME/Library/Application Support/confighub/config.yaml
Windows: %AppData%\confighub\config.yaml
```

配置是单文档 YAML，只允许以下两个字段：

```yaml
server: http://config.example.com
token: ch_xxx
```

两个字段均可省略，但只要字段出现，其值就必须是非空且合法的字符串。未知字段、重复字段、额外 YAML 文档、非字符串值和超过大小上限的文件均视为无效配置。配置文件大小上限固定为 16 KiB；Token 继续使用现有 UTF-8、非空、不含空白或控制字符且不超过 4096 字节的边界。

## 加载、合并与优先级

需要连接信息的命令按以下顺序工作：

1. 确定全局和当前工作目录配置路径；
2. 如果全局配置存在，则安全打开、严格解析并验证；
3. 如果本地配置存在，则安全打开、严格解析并验证；
4. 以字段为单位将本地配置覆盖到全局配置；
5. 再应用环境变量和命令行参数；
6. 记录最终每个字段的来源，供配置查看命令使用。

本地文件不是对全局文件的整体替换。例如：

```yaml
# ~/.config/confighub/config.yaml
server: http://config.example.com
```

```yaml
# project-a/.confighub.yaml
token: ch_project_a
```

最终得到全局 Server 和项目 A Token。项目 B 可以在自己的目录中保存另一个 Token，而不重复 Server。

完整优先级为：

```text
Server: --server > CONFIGHUB_URL > 本地 server > 全局 server
Token:  --token-file > CONFIGHUB_TOKEN > 本地 token > 全局 token
```

命令行显式提供空 `--server`、空或无效 `--token-file` 时继续直接失败，不回退。非空但无效的环境变量也直接失败；空环境变量视为未提供。任何存在但无效的本地或全局文件都直接失败，即使更高优先级来源能够覆盖其中的字段，也不会静默忽略损坏配置。

`version` 和根帮助等不需要连接信息的命令不加载配置，因此用户配置损坏不会阻止查看版本或帮助。

## 配置查看命令

根命令新增 `config` 命令组，只提供只读接口：

```text
confighub config path
confighub config show
confighub config get server
confighub config get token
```

不增加 `config set`、`config unset` 或 `config init`。配置文件由用户使用编辑器或自动化配置管理工具创建。

### `config path`

`config path` 显示全局和本地两个绝对候选路径，以及每个文件是 `loaded` 还是 `missing`。它执行与业务命令相同的安全打开和严格解析；存在但无效的文件使命令失败，避免把不可用文件报告为已加载。无法确定全局用户配置目录时显示 `unavailable`，但只要其他来源足够，业务命令仍可继续。

示例输出：

```text
global: /home/alice/.config/confighub/config.yaml (loaded)
local: /workspace/project-a/.confighub.yaml (loaded)
```

### `config show`

`config show` 显示应用全部优先级后的 Server、脱敏 Token，以及各字段来源。未设置字段显示 `<unset>` 和 `none`。文件来源包含对应绝对路径；命令行、环境变量和 Token 文件来源只显示来源类型，不回显 Token 文件内容。

示例输出：

```text
server: http://config.example.com
server_source: global (/home/alice/.config/confighub/config.yaml)
token: ch_*********7f2a
token_source: local (/workspace/project-a/.confighub.yaml)
```

Token 脱敏保留一个稳定但不暴露主体的短前缀和末尾至多四个字符；过短 Token 全部替换为 `*`。该命令不发起网络请求。

### `config get`

`config get server` 只向标准输出写最终 Server 和换行。`config get token` 是唯一会向标准输出写完整解析后 Token 的配置查看接口；这是用户已明确选择的显式取密操作。字段未设置或无效时命令失败且标准输出为空。两个命令都遵守命令行参数、环境变量、本地配置和全局配置的相同优先级。

`config show`、错误、帮助和日志永不包含完整 Token。CLI 继续不提供明文 `--token` 参数，以免 Token 进入 shell history 和进程列表。

## HTTP 与 URL 校验

CLI 的 Server URL 接受任意合法主机或 IP 上的 `http` 和 `https`：

```text
http://config.example.com
http://192.0.2.10:8080
https://config.example.com
http://gateway.local/config-hub
```

继续拒绝以下输入：

- 非绝对 URL、空主机和不支持的 scheme；
- URL 用户名或密码、query、fragment；
- 空端口、端口 0、超过 65535 的端口；
- 畸形 IPv4/IPv6、未加方括号的 IPv6；
- 包含首尾空白、控制字符或 `.`/`..` 路径段的地址。

路径前缀继续受支持，现有安全路径拼接和拒绝跨主机重定向的行为不变。放开 HTTP 只影响 CLI 客户端连接地址，不修改 Server 配置、部署脚本或 Web 的 HTTPS 生产约束。文档必须明确：HTTP 会以明文传输 Bearer Token，只适合用户明确接受该网络风险的受信网络或开发环境。

## 文件安全与错误边界

全局和本地配置都必须是普通文件。Unix 使用不跟随符号链接的打开方式并拒绝 FIFO、设备和目录；Windows 只接受本地盘符路径，并拒绝目录、reparse point 和 alternate data stream 语义。

当配置包含 `token` 字段时，Unix 文件不得向 group 或 other 授予任何权限，即 `mode & 0077 == 0`，通常使用：

```bash
chmod 600 .confighub.yaml
```

只包含 `server` 的文件可以使用普通只读权限。Windows 沿用现有本地卷与 non-reparse 检查；本阶段不引入不可靠的跨语言 ACL 推断。

YAML 解析器错误不得原样输出，因为原始错误未来可能包含用户值。CLI 只报告安全诊断，例如配置层级和绝对路径：

```text
confighub: invalid local configuration: /workspace/project-a/.confighub.yaml
```

诊断可以包含 `local`、`global`、`missing`、`invalid` 和文件路径，但不能包含 Server URL、Token、原始 YAML 行或 Token 文件路径。网络和 API 错误继续使用现有脱敏诊断。

仓库 `.gitignore` 增加 `/.confighub.yaml`，README 明确提醒其他项目也应忽略本地配置文件。CLI 不自动修改用户项目的 `.gitignore`。

## 实现结构

新增独立的 CLI 配置单元，负责：

- 计算本地与全局路径；
- 安全读取和严格解析单个文件；
- 合并两个文件并记录字段来源；
- 将命令行与环境变量覆盖应用到文件结果；
- 为 `config path/show/get` 提供不含展示逻辑的结构化结果。

路径发现、文件打开和环境读取通过小型依赖边界注入测试，不依赖修改真实用户配置目录。根命令只负责 Cobra 参数绑定、调用解析器和稳定输出；HTTP Client 继续只接收最终 Server 与 Token，不感知配置文件。

`export` 和 `run` 共享同一个连接配置解析入口，避免两个命令产生不同优先级。`--token-file` 继续复用现有安全读取实现，并只在它成为最终最高优先级来源时读取。配置文件安全打开可以复用相同的平台文件边界，但配置格式、大小限制和来源错误保持独立类型。

## 测试设计

实现遵循测试先行，至少覆盖：

### 配置解析与文件边界

- 全局仅 Server、本地仅 Token 的字段合并；
- 本地单字段覆盖全局同字段；
- 只存在全局、只存在本地、两个文件都不存在；
- 未知字段、重复字段、第二份 YAML 文档、错误类型、空值和超大文件；
- Unix 权限、符号链接、FIFO 和特殊文件；
- Windows 本地卷、reparse point、目录和 alternate data stream；
- 当前工作目录精确查找且不递归父目录；
- `os.UserConfigDir()` 的 Linux、macOS 和 Windows 路径拼接。

### 优先级与命令

- Server 的 flag、环境变量、本地和全局四层优先级；
- Token 的 Token 文件、环境变量、本地和全局四层优先级；
- 无效高优先级来源不回退；
- 无效低优先级配置即使被覆盖也会失败；
- `config path` 的 loaded、missing 和 unavailable；
- `config show` 的字段来源、未设置状态和 Token 脱敏；
- `config get server` 与显式 `config get token` 的精确标准输出；
- 所有非显式取密路径均不泄漏 Token。

### URL 与回归门禁

- 公网 HTTP 主机、HTTP IPv4/IPv6、端口和路径前缀成功；
- HTTPS 与现有 localhost HTTP 继续成功；
- FTP、用户信息、query、fragment、非法端口和畸形地址继续失败；
- 现有 `export`、`run`、环境变量和 `--token-file` 测试保持通过；
- Windows 原生 CLI 测试、Darwin arm64 交叉编译、Go race、完整脚本与前端门禁保持通过。

## 文档与兼容性

README 增加：

- 本地和全局配置文件路径；
- 全局 Server 加项目目录 Token 的示例；
- 四层优先级与字段合并规则；
- Unix 文件权限和 `.gitignore` 提醒；
- Windows 配置路径与安全边界；
- `config path/show/get` 示例和 `config get token` 的明文风险；
- HTTP Token 明文传输警告。

现有只使用参数和环境变量的调用不需要迁移。现有 `--token-file` 保持最高 Token 优先级和原有安全检查。配置文件只是新增的低优先级来源，不改变 `--project`、`--env`、`--service`、`export`、`run` 或输出格式的接口。
