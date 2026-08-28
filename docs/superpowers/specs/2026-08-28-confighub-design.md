# ConfigHub 产品与技术设计

日期：2026-08-28
状态：已完成对话评审，等待书面规格评审

## 1. 产品定位

ConfigHub 是面向单个内部研发团队的轻量配置中心。团队成员通过 Web 管理项目配置，构建任务、脚本和开发者通过带范围的机器 Token 调用 HTTP API 或 CLI 拉取当前最新配置。

ConfigHub 解决以下问题：

- 不便写入业务 Git 仓库的配置需要一个集中保存位置；
- 项目需要按照环境组织配置；
- 团队成员需要查看、编辑、比较和回滚配置；
- CI、构建脚本和本地命令需要以稳定接口拉取配置；
- 一把机器 Token 只能读取被明确授权的项目和环境。

ConfigHub 是独立项目，与 `/home/spencer/workspace/be-better` 及 DayOrder 没有代码或运行时关系。项目位置为 `/home/spencer/workspace/config-hub`。

## 2. 用户与部署假设

- 服务对象是一个内部小团队，不支持多个组织注册使用。
- 服务运行在单台 Linux 服务器上，只启动一个 API 实例。
- 服务默认监听 `127.0.0.1:8080`。
- 公网 HTTPS、证书和反向代理由服务器现有基础设施负责，不属于本项目。
- 项目不提供 Dockerfile、Docker Compose、Caddy 或 Kubernetes 配置。
- 项目提供完整的原生构建、启动、检查和备份脚本。
- SQLite 数据文件位于服务器本地磁盘，不放在 NFS 或其他共享文件系统。

## 3. MVP 范围

### 3.1 包含

- 管理员配置账号，成员使用用户名和密码登录；
- 项目与环境管理；
- 字符串键值配置的查看、批量编辑和删除；
- 可选的 service 标签和筛选；
- 环境级不可变版本快照、差异和回滚；
- 项目级 viewer/editor 成员授权；
- 机器身份、项目环境授权、Token 签发、过期和撤销；
- 版本化 HTTP API；
- CLI 的 JSON、dotenv 导出和子进程环境注入；
- SQLite 在线备份和恢复说明；
- 健康检查、统一错误响应和必要的运行时安全保护。

### 3.2 不包含

- 多租户、组织注册、计费或公开 SaaS；
- 配置值静态加密；
- 普通配置与敏感配置的分类；
- 配置值遮罩或二次验证查看；
- 独立审计事件表和机器读取审计；
- 草稿、审批、发布和版本固定；
- 机器写入配置；
- 运行时 Sidecar、配置推送、热刷新或动态凭据；
- 多实例部署、水平扩容和分布式锁；
- 网页修改账号或密码；
- 完整配置文件、证书和二进制文件管理；
- 移动端复杂编辑体验。

配置和 SQLite 备份均包含明文业务配置值。能读取数据库或备份文件的人可以读取全部配置，这是 MVP 接受的安全取舍。登录密码、用户 Session 和机器 Token 不采用明文数据库存储。

## 4. 技术架构

### 4.1 技术栈

- 服务端：Go；
- 管理端：React、TypeScript、Vite；
- 数据库：SQLite；
- CLI：Go；
- Web 资源：构建后嵌入服务端二进制；
- 部署：原生 Linux 进程和 Shell 脚本。

### 4.2 进程与二进制

构建产生两个二进制：

- `confighub-server`：长期运行，托管管理页面和 `/api/v1`，负责认证、授权、版本和 SQLite 访问；
- `confighub`：面向开发者和 CI 的只读客户端，通过 HTTPS 调用服务端 API，不直接访问 SQLite。

入口目录保持为：

```text
cmd/
├── server/
│   └── main.go
└── cli/
    └── main.go
```

入口只负责参数解析、配置加载、依赖组装、启动和退出码。业务实现放入 `internal/` 下的独立包。

### 4.3 运行数据流

```text
浏览器 ──────────────┐
ConfigHub CLI ───────┼─ HTTPS ─ 现有反向代理 ─ HTTP ─ confighub-server ─ SQLite
CI / 构建脚本 ───────┘                                  │
                                                        ├─ users.yaml
                                                        └─ React 静态资源
```

反向代理不由项目安装或启动。服务端默认只接受来自本机网络入口的流量。

## 5. 仓库结构

```text
config-hub/
├── cmd/
│   ├── server/
│   └── cli/
├── internal/
│   ├── auth/
│   ├── permissions/
│   ├── projects/
│   ├── revisions/
│   ├── machineaccess/
│   ├── database/
│   ├── config/
│   └── httpapi/
├── migrations/
├── web/
├── scripts/
│   ├── build.sh
│   ├── start.sh
│   ├── backup.sh
│   └── check.sh
├── config/
│   ├── config.example.yaml
│   └── users.example.yaml
├── docs/
├── dist/                  # 构建产物，不进入 Git
└── data/                  # 运行数据，不进入 Git
```

## 6. 账号、认证与会话

### 6.1 账号来源

`users.yaml` 是账号、系统角色和启用状态的唯一配置来源。文件由部署管理员维护，不提供注册、邀请或网页密码管理。

示例：

```yaml
users:
  - username: admin
    display_name: Administrator
    password: change-me
    role: admin
    enabled: true

  - username: developer-a
    display_name: Developer A
    password: another-password
    role: member
    enabled: true
```

密码按产品决策以明文写在 `users.yaml`。该文件不得进入 Git，权限必须不宽于 `0600`，并只能由运行账号和部署管理员读取。

服务启动和收到 `SIGHUP` 时同步账号：

- 新账号写入 SQLite，并将密码转换为 Argon2id 哈希；
- 相同明文密码能够通过现有哈希验证时，不重复更新凭据；
- 密码变化时更新 Argon2id 哈希并撤销该账号全部 Session；
- `enabled` 变为 false 或账号从文件移除时禁用账号并撤销 Session；
- 用户名变化视为删除旧账号并创建新账号；
- 运行期间的无效文件不会覆盖上一份有效账号状态，并记录不含密码正文的错误；
- 首次启动时文件无效或没有 enabled admin，服务拒绝监听端口。

### 6.2 人类会话

- 登录使用用户名和密码；
- 密码验证使用 Argon2id；
- Session Cookie 使用 `HttpOnly`、生产环境 `Secure` 和 `SameSite=Lax`；
- Session 可服务端撤销，并有明确过期时间；
- Session 凭据只以哈希形式存入 SQLite；
- 登出、账号禁用和密码变化立即使相关 Session 失效；
- 写请求校验 Origin/CSRF；
- 登录接口按来源和用户名进行内存限流，进程重启后限流状态可以丢失。

运行配置包含独立的 `session.key` 文件，用于会话签名和 CSRF 派生。它不用于加密业务配置，权限必须不宽于 `0600`。

## 7. 权限模型

### 7.1 系统角色

- `admin`：拥有全部项目访问权限，可以创建项目、管理环境、配置项目成员、管理机器身份和 Token；
- `member`：只能访问明确授予的项目。

### 7.2 项目权限

- `viewer`：查看项目、环境、当前配置、版本和差异；
- `editor`：包含 viewer 权限，并可新增、修改、删除和回滚配置；
- 项目权限不继续细分到环境；
- 账号管理和机器身份管理仅限 admin。

管理员修改项目授权时直接更新 SQLite。账号文件只管理系统角色，不承载项目级授权。

### 7.3 机器身份

机器身份是代表 CI、部署任务或脚本的非人类账号。

- 一个机器身份可以拥有一个或多个“项目 + 环境”读取授权；
- 一个机器身份可以同时拥有多个 Token，以支持无中断轮换；
- Token 继承机器身份当前的授权范围；
- 机器身份和 Token 均可禁用或撤销；
- 机器权限固定为读取当前配置，不能调用管理写接口；
- Token 使用高熵随机不透明值，带 `ch_` 前缀；
- Token 明文只在签发时展示一次；
- SQLite 只保存 Token 的 SHA-256 哈希、可识别前缀、创建时间、过期时间和撤销状态；
- MVP 不保存机器读取历史和 last-used 审计数据。

## 8. 配置与版本模型

### 8.1 层级

```text
Project
└── Environment
    └── Revision
        └── Config Entry
```

- 项目和环境均使用稳定 ID 与人类可读 slug；
- 同一项目内环境 slug 唯一；
- 同一环境内配置 key 唯一；
- 配置值始终是 UTF-8 字符串；
- key 必须能安全导出为环境变量，采用 `[A-Za-z_][A-Za-z0-9_]*`；
- service 是可选标签，只用于列表和拉取筛选，不参与 key 唯一性；
- 没有 service 查询参数时返回环境内全部配置；指定 service 时只返回该标签的配置。

### 8.2 不可变快照

每次保存以环境的完整配置快照为一个 Revision。保存事务执行：

1. 验证 `base_revision` 等于环境当前 Revision；
2. 创建新的 Revision 元数据；
3. 写入新 Revision 的全部 Config Entry；
4. 原子更新 Environment 的 `current_revision_id`；
5. 提交事务。

若 `base_revision` 已过期，返回 `409 Conflict`，不提供强制覆盖。

Revision 保存递增版本号、修改用户、创建时间和可选变更说明。历史版本不修改、不删除，也不设置自动保留期限。

回滚会复制目标历史快照，创建一个新的当前 Revision。例如当前版本为 12，回滚到 10 后会创建内容等于 10 的版本 13。

## 9. SQLite 设计

核心表：

- `users`；
- `sessions`；
- `projects`；
- `project_members`；
- `environments`；
- `revisions`；
- `revision_entries`；
- `machine_identities`；
- `machine_grants`；
- `access_tokens`；
- `schema_migrations`。

数据库设置：

- 开启 foreign keys；
- 开启 WAL；
- 设置有限的 busy timeout；
- 写操作使用显式事务；
- 只允许一个 ConfigHub 服务实例访问运行数据库；
- 迁移失败时服务不开始监听；
- 磁盘不足、busy timeout 或事务错误不会更新 current Revision。

业务配置值在 SQLite 和备份中以明文保存。数据库文件和备份目录权限必须限制为运行账号可读写。

## 10. HTTP API

所有业务接口位于 `/api/v1`，错误使用统一 JSON envelope：

```json
{
  "error": {
    "code": "revision_conflict",
    "message": "Configuration changed since it was loaded",
    "request_id": "req_...",
    "fields": {}
  }
}
```

主要接口：

- `POST /auth/login`；
- `POST /auth/logout`；
- `GET /auth/session`；
- 项目、环境和项目成员的管理接口；
- 当前配置的读取与替换接口；
- Revision 列表、详情、差异和回滚接口；
- admin 使用的机器身份、授权和 Token 管理接口；
- `GET /projects/{project}/environments/{environment}/config` 机器读取接口；
- `GET /health/live`；
- `GET /health/ready`。

机器读取示例：

```http
GET /api/v1/projects/shop/environments/production/config?service=api
Authorization: Bearer ch_...
```

```json
{
  "project": "shop",
  "environment": "production",
  "revision": 13,
  "values": {
    "DATABASE_URL": "postgres://...",
    "PORT": "8080"
  }
}
```

- 不指定 `service` 时返回当前 Revision 的全部键；
- 指定 `service` 时返回标签完全匹配的键；
- 响应设置 `Cache-Control: no-store`；
- API 和访问日志不得记录 Authorization、Cookie、密码和配置正文；
- API 始终返回当前 Revision，不支持指定历史版本。

状态码：

- `400`：请求无法解析；
- `401`：Session 或 Token 无效；
- `403`：缺少项目、环境或管理权限；
- `404`：资源不存在；
- `409`：Revision 基线冲突或唯一性冲突；
- `422`：字段或配置内容不合法；
- `429`：登录尝试过多；
- `503`：数据库暂时忙、迁移未完成或服务未就绪。

## 11. CLI

CLI 只调用 HTTPS API，不读取本地服务端 SQLite。

连接信息优先从环境变量或受限文件读取：

- `CONFIGHUB_URL`；
- `CONFIGHUB_TOKEN`；
- 可选的 Token 文件参数。

CLI 不提供明文 Token 命令行参数，避免 Token 出现在 shell history 和进程列表。

### 11.1 导出

```bash
confighub export \
  --project shop \
  --env production \
  --format dotenv
```

支持 `json` 和 `dotenv`。结果写入标准输出，CLI 不自行创建 `.env` 文件。dotenv 输出必须正确处理空格、换行、引号和特殊字符，不进行 shell 命令插值。

### 11.2 注入子进程

```bash
confighub run \
  --project shop \
  --env production \
  -- npm run build
```

- 远端同名键覆盖当前进程环境；
- 配置只注入子进程；
- 拉取失败时不启动子进程；
- 不使用旧缓存或部分结果；
- CLI 转发必要信号并返回子进程退出码。

## 12. 管理端页面

路由：

```text
/login
/projects
/projects/:project
/machine-access
/members
/system
```

### 12.1 项目详情

项目详情包含环境切换和三个标签页：

- Configuration：表格显示 Key、Value、Service，支持搜索、筛选和批量编辑；
- Versions：显示版本时间线、修改人、说明、完整值差异和回滚入口；
- Members：显示项目成员及 viewer/editor 权限。

配置值按产品决策直接显示，不遮罩。viewer 不显示写操作；editor 可以进入批量编辑模式。一次保存只产生一个 Revision。

### 12.2 机器访问

Machine Access 仅 admin 可见，用于：

- 创建和禁用机器身份；
- 分配项目环境范围；
- 签发、轮换、过期和撤销 Token；
- 在创建完成页一次性复制 Token 明文。

### 12.3 成员与系统

- Members 展示从 `users.yaml` 同步的账号和状态，不提供账号密码编辑；
- System 只展示构建版本、数据库就绪状态和账号文件最后一次成功同步时间，不展示文件正文。

### 12.4 交互保护

- 离开存在未保存内容的编辑页前确认；
- 删除配置和回滚前二次确认；
- `409` 冲突展示刷新并比较入口，不提供强制覆盖；
- Token 创建窗口关闭后不能再次查看明文；
- 保存失败时保留用户尚未提交成功的本地编辑内容。

## 13. 构建、启动与配置

### 13.1 运行时文件

以下文件不进入 Git：

```text
config/config.yaml
config/users.yaml
config/session.key
data/confighub.db
backups/
```

仓库只提供无真实凭据的示例文件。

`config/config.yaml` 的运行时结构固定为：

```yaml
server:
  listen: 127.0.0.1:8080
  public_url: https://config.example.com
  trusted_proxy_cidrs:
    - 127.0.0.1/32
    - ::1/128

database:
  path: ./data/confighub.db

auth:
  users_file: ./config/users.yaml
  session_key_file: ./config/session.key
  session_ttl: 24h

backup:
  directory: ./backups
```

服务只信任来自 `trusted_proxy_cidrs` 的代理来源头，避免客户端伪造来源地址绕过登录限流。所有相对路径以项目运行目录为基准。

### 13.2 构建

```bash
./scripts/build.sh
```

脚本依次执行：

1. 校验 Go 和 Node.js 版本；
2. 安装锁定的前端依赖；
3. 执行前端类型检查和测试；
4. 构建 React 静态资源；
5. 执行 Go 测试；
6. 将静态资源嵌入并构建两个 Go 二进制。

输出：

```text
dist/confighub-server
dist/confighub
```

### 13.3 启动

```bash
./scripts/start.sh
```

脚本和服务端执行：

1. 加载 `config/config.yaml`；
2. 校验监听地址、数据目录、`users.yaml` 和 `session.key`；
3. 校验受限文件权限；
4. 打开 SQLite 并执行嵌入式迁移；
5. 同步账号；
6. 以前台进程启动并将日志写到 stdout/stderr。

脚本不自行后台化、不安装 systemd unit，也不管理反向代理。服务端支持优雅退出。

## 14. 备份与恢复

```bash
./scripts/backup.sh
```

备份脚本生成带时间戳的目标路径，并调用：

```bash
./dist/confighub-server backup \
  --config ./config/config.yaml \
  --output ./backups/confighub-YYYYMMDD-HHMMSS.db
```

`backup` 是服务端二进制的运维子命令，只执行 SQLite 在线备份后退出。它不得通过简单复制正在写入的数据库文件实现备份。

要求：

- 备份文件原子完成后才使用最终文件名；
- 备份目录权限不宽于 `0700`，单个文件不宽于 `0600`；
- 备份包含明文配置，安全级别必须等同运行数据库；
- 升级迁移前必须先完成一次成功备份；
- 恢复时停止服务、校验备份可打开、替换数据库、执行迁移检查并重新启动；
- 运维人员定期执行真实恢复演练。

## 15. 错误与恢复策略

- 配置保存使用事务，失败时不改变当前 Revision；
- Revision 冲突明确返回 409，不自动合并或覆盖；
- SQLite busy 超过有限等待后返回 503；
- 迁移、账号初始同步或数据库完整性检查失败时 readiness 失败且业务端口不接受请求；
- 运行中账号文件重载失败时保留上一份有效配置；
- CLI 拉取失败时不运行目标命令；
- 服务端不维护配置缓存作为 SQLite 故障时的降级数据源；
- 所有错误响应包含稳定 code 和 request ID，不包含配置值、密码或 Token。

## 16. 测试与验收

### 16.1 Go 单元测试

- 密码和 Token 验证；
- 系统角色与项目权限；
- 配置 key 校验；
- Revision 差异与回滚；
- dotenv 转义；
- CLI 环境合并和退出码。

### 16.2 SQLite 集成测试

- 空库迁移和重复迁移；
- 账号同步、禁用和密码变化撤销 Session；
- 新 Revision 的事务原子性；
- 两个 editor 基于同一 Revision 保存时只允许一个成功；
- 项目成员隔离；
- 机器身份只能读取已授权项目环境；
- Token 过期和撤销立即生效；
- 在线备份可以独立打开并恢复。

### 16.3 HTTP 与前端测试

- 登录、登出、Session Cookie、Origin/CSRF 和限流；
- 统一错误 envelope 和状态码；
- viewer/editor/admin 的界面与接口权限一致；
- 批量编辑、未保存提醒和 409 冲突提示；
- 版本差异与回滚确认；
- Token 明文仅创建时显示一次。

### 16.4 端到端验收

`scripts/check.sh` 必须覆盖真实构建，并以临时 SQLite 文件启动服务完成：

1. 管理员登录；
2. 创建项目和两个环境；
3. 给成员分配 viewer/editor 权限；
4. 保存配置并生成多个 Revision；
5. 创建机器身份和限定范围 Token；
6. 使用 CLI 导出 JSON 和 dotenv；
7. 使用 CLI 将配置注入子进程；
8. 验证未授权环境被拒绝；
9. 回滚并确认 CLI 读取新的当前版本；
10. 创建在线备份并验证可以打开。

## 17. 完成标准

MVP 只有在以下条件全部满足时才算完成：

- 两个二进制能够由单个构建脚本可重复生成；
- 无 Docker、Caddy 或外部数据库依赖；
- 原生启动脚本能从空数据目录完成迁移并启动；
- Web 可以完成项目、环境、配置、版本和成员权限的核心流程；
- 机器 Token 权限不会越过项目环境范围；
- CLI 可以可靠导出和注入当前配置；
- 并发编辑不会静默丢失更新；
- SQLite 备份与恢复经过自动化验证；
- 类型检查、单元测试、集成测试、端到端测试和真实构建全部通过。
