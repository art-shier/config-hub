# ConfigHub

ConfigHub 是面向单个内部研发团队的轻量配置中心。管理员和成员通过 Web 管理项目、环境与不可变配置版本，CI、构建脚本和开发者通过限定到“项目 + 环境”的机器 Token 使用 HTTP API 或 `confighub` CLI 读取当前配置。

项目采用 Go、React/Vite 和单机 SQLite，原生构建后只产生两个二进制：

- `dist/confighub-server`：Web、HTTP API、账号同步、SQLite 与在线备份；
- `dist/confighub`：只读 CLI，不直接访问 SQLite。

仓库不提供 Docker、Caddy、外部数据库或 systemd 配置。

## 前置条件

- Linux 和 Bash；
- Go `1.25.x`；
- Node.js `^22.22.2`、`^24.15.0` 或 `>=26.0.0`，以及随 Node 提供的 npm；
- 执行浏览器验收时需要 OpenSSL 和 Chromium。测试会依次使用 `PLAYWRIGHT_CHROMIUM_EXECUTABLE`、系统 Chrome/Chromium，最后使用 Playwright 安装的 Chromium。没有系统浏览器时可执行 `cd web && npx playwright install chromium`。

Node.js 23、25 以及低于上述 patch 下限的版本不受支持，构建脚本会在安装依赖前拒绝它们。

## 首次配置

从示例创建运行文件，并在写入任何密码或密钥前收紧权限：

```bash
cp config/config.example.yaml config/config.yaml
cp config/users.example.yaml config/users.yaml
chmod 600 config/config.yaml config/users.yaml
umask 077
openssl rand -base64 48 > config/session.key
chmod 600 config/session.key
mkdir -p data backups
chmod 700 data backups
```

然后编辑 `config/config.yaml` 和 `config/users.yaml`。相对路径以 `config.yaml` 所在目录为基准。至少保留一个 `enabled: true` 的 admin。

这些文件和目录包含安全相关数据，不得提交到 Git：

- `config/config.yaml`；
- `config/users.yaml`：包含明文登录密码；
- `config/session.key`：用于 Session 签名和 CSRF 派生，不用于加密业务配置；
- `data/confighub.db` 及 SQLite WAL/SHM；
- `backups/`。

`config.yaml`、`users.yaml`、`session.key`、SQLite 文件和备份文件权限必须不宽于 `0600`，数据与备份目录必须不宽于 `0700`。运行账号应是这些文件的唯一所有者和日常读写者。

## 构建与启动

完整构建：

```bash
./scripts/build.sh
```

脚本锁定安装前端依赖，执行类型检查、前后端测试和前端构建，再生成两个二进制。启动前台服务：

```bash
./scripts/start.sh
```

默认读取 `config/config.yaml`。也可以指定其他受限配置文件：

```bash
CONFIGHUB_CONFIG=/srv/confighub/config/config.yaml ./scripts/start.sh
```

服务默认只监听 `127.0.0.1:8080`。生产访问应由服务器已有的 HTTPS 反向代理终止 TLS，再转发到这个回环地址；反向代理不由 ConfigHub 安装或管理。`server.public_url` 必须填写用户实际访问的 HTTPS Origin（包括非默认端口），`trusted_proxy_cidrs` 只列出可信代理来源网段。不要把服务监听地址直接暴露到公网。

停止进程时发送 `SIGTERM` 或 `SIGINT`，服务会停止接收新请求并等待已接收请求完成。只允许一个 `confighub-server` 实例访问运行数据库；SQLite 文件必须位于本地磁盘，不能放在 NFS 或其他共享文件系统。

## 账号与 SIGHUP

`users.yaml` 是账号、系统角色、启用状态和密码的唯一来源，Web 不修改账号或密码。服务启动时会同步该文件：密码在文件中是明文，在 SQLite 中只保存 Argon2id 哈希。

修改 `users.yaml` 并保持 `0600` 后，向服务发送 `SIGHUP` 重新同步，而无需重启：

```bash
kill -HUP "$(cat /run/confighub.pid)"
```

具体 PID 文件由你的进程管理器维护。密码变化、账号禁用或从文件移除会撤销该账号的现有 Session；无效的重载文件不会覆盖上一份有效账号状态。

## 健康检查

本机反向代理或进程管理器可检查：

```bash
curl -fsS http://127.0.0.1:8080/api/v1/health/live
curl -fsS http://127.0.0.1:8080/api/v1/health/ready
```

- `/api/v1/health/live` 表示进程生命周期仍在运行；
- `/api/v1/health/ready` 只在数据库可用、迁移完成且最近一次有效账号同步已完成时成功。

对外检查应使用反向代理提供的 HTTPS URL。

## CLI

CLI 只读取机器身份当前被授权的配置。Token 明文仅在 Web 签发时展示一次。优先由 CI 的 secret 设施注入连接信息：

```bash
export CONFIGHUB_URL=https://config.example.com
export CONFIGHUB_TOKEN='ch_一次性签发的机器Token'

./dist/confighub export --project shop --env production --format json
./dist/confighub export --project shop --env production --format dotenv
./dist/confighub export --project shop --env production --service api --format dotenv
./dist/confighub run --project shop --env production -- npm run build
```

`export` 只写标准输出，不会自行创建 `.env` 文件。`run` 中远端同名键覆盖父进程环境，且只注入子进程；拉取失败时不会启动子进程。

若不希望 Token 常驻环境变量，可使用受限文件。CLI 故意不接受明文 Token 命令行参数，以免进入 shell history 或进程列表：

```bash
umask 077
printf '%s\n' 'ch_一次性签发的机器Token' > /run/confighub-shop.token
chmod 600 /run/confighub-shop.token

CONFIGHUB_URL=https://config.example.com \
  ./dist/confighub --token-file /run/confighub-shop.token \
  export --project shop --env production --format json
```

## 备份与恢复

在线备份不需要停止正在运行的服务：

```bash
./scripts/backup.sh
```

脚本使用 UTC 时间生成 `backups/confighub-YYYYMMDD-HHMMSS.db`，并调用 SQLite 在线 Backup API；它不会简单复制活动数据库文件。也可显式指定目标：

```bash
./dist/confighub-server backup \
  --config ./config/config.yaml \
  --output ./backups/manual-before-upgrade.db
```

目标文件已存在时不会覆盖。每次升级迁移前先完成一次成功备份，并定期做真实恢复演练。

恢复流程：

1. 停止 `confighub-server`，确认没有第二个实例访问数据库；
2. 在隔离位置打开备份并执行 SQLite `PRAGMA integrity_check`，结果必须为 `ok`；
3. 保留当前数据库及其 `-wal`、`-shm` 作为可回退副本；
4. 将已验证备份复制到 `database.path`，设为 `0600`，数据目录设为 `0700`；不要把 WAL/SHM 与另一份数据库混用；
5. 启动服务，让当前二进制检查并执行嵌入式迁移；
6. 确认 readiness、登录、当前配置和版本历史后，再处理旧副本。

SQLite 数据库和所有备份都以明文保存业务配置。任何能够读取这些文件的人都能读取全部配置，备份必须采用与运行数据库相同的访问控制和保管级别。

## 检查

完整质量门禁：

```bash
./scripts/check.sh
```

它执行 Go 格式检查、vet、race 测试、前端类型检查/单元测试/构建、两个二进制构建、真实 Chromium E2E 和真实运行时/在线备份验收。浏览器失败时的截图和 trace 只写入 `output/playwright/`；成功后不保留业务运行数据。

## MVP 明确不包含

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

完整产品与安全取舍见 `docs/superpowers/specs/2026-08-28-confighub-design.md`。
