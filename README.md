# ConfigHub

ConfigHub 是面向单个内部研发团队的轻量配置中心。管理员和成员通过 Web 管理项目、环境与不可变配置版本，CI、构建脚本和开发者通过限定到“项目 + 环境”的机器 Token 使用 HTTP API 或 `confighub` CLI 读取当前配置。

项目采用 Go、React/Vite 和单机 SQLite，原生构建后只产生两个二进制：

- `dist/confighub-server`：Web、HTTP API、账号同步、SQLite 与在线备份；
- `dist/confighub`：只读 CLI，不直接访问 SQLite。

仓库提供 Linux GitHub Release、独立安装/部署脚本和 systemd unit；不提供 Docker、Caddy 或外部数据库。

## 前置条件

- Linux 和 Bash；
- Go `1.25.x`；
- Node.js `^22.22.2`、`^24.15.0` 或 `>=26.0.0`，以及随 Node 提供的 npm；
- 执行浏览器验收时需要 OpenSSL 和 Chromium。测试会依次使用 `PLAYWRIGHT_CHROMIUM_EXECUTABLE`、系统 Chrome/Chromium，最后使用 Playwright 安装的 Chromium。没有系统浏览器时可执行 `cd web && npx playwright install chromium`。

Node.js 23、25 以及低于上述 patch 下限的版本不受支持，构建脚本会在安装依赖前拒绝它们。

## Release 产物与安装

推送严格格式的 `vMAJOR.MINOR.PATCH` 标签后，GitHub Release 会发布两个彼此独立的产品，支持 Linux `amd64` 和 `arm64`：

- `config-hub-server_VERSION_linux_ARCH.tar.gz`：`confighub-server`、内嵌 Web、配置示例和 systemd unit；
- `config-hub-cli_VERSION_linux_ARCH.tar.gz`：独立的 `confighub` CLI；
- `checksums.txt`：四个归档的 SHA-256 校验值。

Server 安装不会附带 CLI；需要在哪台机器使用 CLI，就在那台机器单独安装。两个脚本只从 `art-shier/config-hub` 的 GitHub Release 下载匹配当前 Linux 架构的归档，并在安装前验证校验值、归档结构和二进制版本。

### 安装 CLI

先下载并审阅安装脚本：

```bash
curl -fsSLO https://raw.githubusercontent.com/art-shier/config-hub/main/scripts/install-cli.sh
less install-cli.sh
```

安装最新版本到默认的 `/usr/local/bin`：

```bash
sudo bash install-cli.sh
```

安装固定版本，或安装到当前用户的自定义绝对目录：

```bash
sudo bash install-cli.sh --version v1.2.3
mkdir -p "$HOME/.local/bin"
bash install-cli.sh --version v1.2.3 --install-dir "$HOME/.local/bin"
```

安装后可用 `confighub version` 核对版本。自定义目录需要加入 `PATH`。

### 首次部署 Server + Web

Server 部署脚本要求 root、systemd、Linux `amd64` 或 `arm64`。它创建专用的 `confighub` 系统账号，将服务限制在 `127.0.0.1:8080`，并生成受限配置、管理员账号、Session 密钥和 SQLite/备份目录：

```bash
curl -fsSLO https://raw.githubusercontent.com/art-shier/config-hub/main/scripts/deploy-server.sh
less deploy-server.sh
sudo bash deploy-server.sh --public-url https://config.example.com
```

默认会通过 `/dev/tty` 交互读取两次初始管理员密码。非交互部署必须改用普通文件，且文件必须是非符号链接、只包含一行非空密码、权限严格为 `0600`：

```bash
sudo install -m 600 /dev/null /root/confighub-admin-password
sudoedit /root/confighub-admin-password
sudo bash deploy-server.sh \
  --public-url https://config.example.com \
  --admin-username admin \
  --admin-password-file /root/confighub-admin-password
sudo rm -f /root/confighub-admin-password
```

脚本安装的运行文件位于 `/etc/confighub`，SQLite 位于 `/var/lib/confighub`，在线备份位于 `/var/backups/confighub`。生产访问必须由外部 HTTPS 反向代理转发到回环地址；不要把服务监听地址直接暴露到公网。若代理不在本机，先在 `/etc/confighub/config.yaml` 的 `trusted_proxy_cidrs` 中加入精确的可信代理网段，再重启服务。

### 升级 Server + Web

再次下载并审阅当前部署脚本，然后直接执行即可升级到最新版本；也可用 `--version v1.2.3` 固定版本：

```bash
sudo bash deploy-server.sh
sudo bash deploy-server.sh --version v1.2.3
```

已安装相同版本且 readiness 正常时，脚本不做变更。升级到新版本前，脚本必须先用当前二进制完成 SQLite 在线备份；备份失败时不会替换二进制或 unit。升级后的服务若未就绪，脚本会保留新二进制、`/usr/local/lib/confighub/confighub-server.previous` 和升级前备份供人工恢复，但不会自动回滚 SQLite。

### systemd 运维与卸载

```bash
sudo systemctl status confighub.service
sudo systemctl restart confighub.service
sudo systemctl reload confighub.service
sudo journalctl -u confighub.service
curl -fsS http://127.0.0.1:8080/api/v1/health/live
curl -fsS http://127.0.0.1:8080/api/v1/health/ready
```

`reload` 发送 `SIGHUP`，用于重新载入 `/etc/confighub/users.yaml`。若要卸载 CLI，只需删除实际安装目录中的 `confighub`。保守卸载 Server 时先停止并禁用服务，再移除 unit 和已安装二进制；默认保留配置、数据库和备份：

```bash
sudo systemctl disable --now confighub.service
sudo rm -f /etc/systemd/system/confighub.service \
  /usr/local/bin/confighub-server \
  /usr/local/lib/confighub/confighub-server.previous
sudo systemctl daemon-reload
```

不要把 `/etc/confighub`、`/var/lib/confighub` 或 `/var/backups/confighub` 作为常规卸载的一部分删除。确认数据保留与备份策略后，再单独决定是否移除 `confighub` 系统账号和这些目录。

## 源码运行：首次配置

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

## 源码运行：构建与启动

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

恢复必须在停服状态下原子切换，不能直接覆盖活动的 `database.path`。以下示例需要 `sqlite3` 和 GNU coreutils，并且应由实际运行 `confighub-server` 的账号执行；先把变量改成绝对路径：

```bash
set -euo pipefail
umask 077

db_path=/srv/confighub/data/confighub.db
backup_path=/srv/confighub/backups/confighub-20260829-120000.db
data_dir="$(dirname -- "$db_path")"
restore_id="$(date -u +%Y%m%d-%H%M%S)"
rollback_dir="$data_dir/restore-rollback-$restore_id"
staged_db="$data_dir/.confighub-restore-$restore_id.tmp"
expected_uid="$(id -u)"
expected_gid="$(id -g)"

test -f "$db_path"
test -f "$backup_path"

# 先通过原有进程管理方式停止服务；这里确认该运行账号下没有遗漏的实例。
if pgrep -u "$expected_uid" -f '[c]onfighub-server.*serve' >/dev/null; then
  printf '%s\n' 'confighub-server is still running' >&2
  exit 1
fi

test "$(stat -c '%a' "$data_dir")" = 700
test "$(sqlite3 -readonly "$backup_path" 'PRAGMA integrity_check;')" = ok
test ! -e "$rollback_dir"
install -d -m 700 -- "$rollback_dir"
test "$(stat -c '%a' "$rollback_dir")" = 700
test "$(stat -c '%u' "$rollback_dir")" = "$expected_uid"
test "$(stat -c '%g' "$rollback_dir")" = "$expected_gid"
for active_file in "$db_path" "$db_path-wal" "$db_path-shm"; do
  if [[ -e "$active_file" ]]; then
    mv -- "$active_file" "$rollback_dir/"
  fi
done
test ! -e "$db_path"
test ! -e "$db_path-wal"
test ! -e "$db_path-shm"
test ! -e "$staged_db"
sync -f "$data_dir"

# 在目标目录内生成新文件，避免跨文件系统 rename；此时仍不占用活动路径。
install -m 600 -- "$backup_path" "$staged_db"
test "$(sqlite3 -readonly "$staged_db" 'PRAGMA integrity_check;')" = ok
test "$(stat -c '%a' "$staged_db")" = 600
test "$(stat -c '%u' "$staged_db")" = "$expected_uid"
test "$(stat -c '%g' "$staged_db")" = "$expected_gid"

# 先同步文件内容，再同目录原子 rename，最后同步 rename 所在文件系统。
sync -d "$staged_db"
mv -T -- "$staged_db" "$db_path"
sync -d "$db_path"
sync -f "$data_dir"
```

随后启动唯一的 `confighub-server` 实例，让当前二进制执行嵌入式迁移。依次确认 readiness 返回成功、管理员能够登录、当前配置内容正确、版本历史与预期一致。确认完成前保留整个 `rollback_dir`，不要把其中旧数据库的 `-wal`/`-shm` 与新数据库混用。

如果启动、迁移或业务验证失败，先再次完全停服。把这次恢复生成的数据库及其新 sidecar 一并移到另一个 `0700` 故障留存目录，再将 `rollback_dir` 中原来同一代的数据库、`-wal`、`-shm` 一并移回原名，执行 `sync -f "$data_dir"` 后再启动验证；不要直接覆盖任一仍在使用的文件。

SQLite 数据库和所有备份都以明文保存业务配置。任何能够读取这些文件的人都能读取全部配置，备份必须采用与运行数据库相同的访问控制和保管级别。

## 检查

完整质量门禁：

```bash
./scripts/check.sh
```

它执行 Go 格式检查、vet、race 测试、发布/安装/部署 Bash 行为测试、前端类型检查/单元测试/构建、两个二进制构建、真实 Chromium E2E 和真实运行时/在线备份验收。浏览器失败时的截图和 trace 只写入 `output/playwright/`；成功后不保留业务运行数据。

维护者发布新版本前，先确保 `main` 的完整质量门禁通过，再创建并推送标签：

```bash
./scripts/check.sh
git tag v1.2.3
git push origin v1.2.3
```

标签推送会触发发布流水线：重新执行质量门禁，构建四个归档，核对资产与校验清单，在验证全部上传资产后才公开 GitHub Release。标签必须严格匹配 `vMAJOR.MINOR.PATCH`。

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
- 通过 Web 管理完整配置文件或证书，以及多主机部署编排；
- 移动端复杂编辑体验。

完整产品与安全取舍见 `docs/superpowers/specs/2026-08-28-confighub-design.md`。
