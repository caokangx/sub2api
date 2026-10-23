# Sub2API 二次开发版本构建与部署

本文说明如何将 `hhapi/main` 分支构建为 Docker 镜像，并通过 SSH 部署到远端服务器。

## 部署信息

- 服务器：`176.122.183.195`
- 本机 SSH 别名：`bwg-ssh`
- 部署目录：`/opt/sub2api`
- 二开分支：`hhapi/main`
- 容器镜像：`sub2api-custom:<Git Commit>`
- Compose 基础配置：`docker-compose.local.yml`

部署方式为：

1. 在本机构建目标服务器架构的镜像。
2. 通过 SSH 将镜像直接传输到服务器。
3. 使用 Docker Compose 启动 PostgreSQL、Redis 和 Sub2API。

此方案不依赖 Docker Hub 或 GHCR，适合单台服务器。生产环境使用 Git Commit 作为镜像标签，方便确认版本和回滚。

## 一、前置要求

### 本机

- Git
- Docker Desktop，并支持 `docker buildx`
- 已配置 `ssh bwg-ssh`

检查：

```bash
ssh bwg-ssh
docker buildx version
```

### 服务器

- Docker Engine
- Docker Compose V2
- 当前 SSH 用户可以执行 `sudo docker`

检查服务器：

```bash
ssh bwg-ssh '
  uname -m
  sudo docker --version
  sudo docker compose version
'
```

服务器架构与构建平台的对应关系：

- `x86_64`：使用 `linux/amd64`
- `aarch64` 或 `arm64`：使用 `linux/arm64`

下文默认服务器为 `x86_64`。如果服务器是 ARM，请将命令中的 `linux/amd64` 改为 `linux/arm64`。

## 二、首次部署

### 1. 准备本地代码

```bash
cd /Users/yikangcao/Documents/sub2api
git switch hhapi/main
git pull --ff-only origin hhapi/main
git status
```

建议只部署已经提交的代码。构建前确保 `git status` 没有非预期修改。

### 2. 构建镜像

```bash
cd /Users/yikangcao/Documents/sub2api

IMAGE_TAG="$(git rev-parse --short=12 HEAD)"
FULL_COMMIT="$(git rev-parse HEAD)"

docker buildx build \
  --platform linux/amd64 \
  --build-arg VERSION="custom-${IMAGE_TAG}" \
  --build-arg COMMIT="${FULL_COMMIT}" \
  --tag "sub2api-custom:${IMAGE_TAG}" \
  --load \
  .
```

确认镜像：

```bash
docker image inspect "sub2api-custom:${IMAGE_TAG}"
```

### 3. 初始化服务器目录

```bash
ssh bwg-ssh '
  sudo mkdir -p \
    /opt/sub2api/data \
    /opt/sub2api/postgres_data \
    /opt/sub2api/redis_data \
    /opt/sub2api/backups
  sudo chown -R "$USER":"$USER" /opt/sub2api
'
```

上传 Compose 和环境变量模板：

```bash
cd /Users/yikangcao/Documents/sub2api

scp deploy/docker-compose.local.yml \
  bwg-ssh:/opt/sub2api/docker-compose.yml

scp deploy/.env.example \
  bwg-ssh:/opt/sub2api/.env
```

创建二开镜像覆盖配置：

```bash
ssh bwg-ssh 'cat > /opt/sub2api/docker-compose.custom.yml <<'"'"'EOF'"'"'
services:
  sub2api:
    image: sub2api-custom:${IMAGE_TAG}
    pull_policy: never
EOF
chmod 600 /opt/sub2api/.env
'
```

`docker-compose.yml` 仍可跟随上游更新；`docker-compose.custom.yml` 只负责把官方镜像替换为二开镜像。

### 4. 配置生产环境

生成密码和密钥：

```bash
ssh bwg-ssh '
  echo "POSTGRES_PASSWORD=$(openssl rand -hex 32)"
  echo "JWT_SECRET=$(openssl rand -hex 32)"
  echo "TOTP_ENCRYPTION_KEY=$(openssl rand -hex 32)"
'
```

编辑配置：

```bash
ssh bwg-ssh
cd /opt/sub2api
nano .env
```

确保至少设置以下内容：

```dotenv
IMAGE_TAG=这里填写构建出的Git Commit短标签

POSTGRES_USER=sub2api
POSTGRES_DB=sub2api
POSTGRES_PASSWORD=替换为随机密码

JWT_SECRET=替换为随机密钥
TOTP_ENCRYPTION_KEY=替换为随机密钥

ADMIN_EMAIL=替换为管理员邮箱
ADMIN_PASSWORD=替换为高强度管理员密码

TZ=Asia/Shanghai
SERVER_PORT=8080
BIND_HOST=127.0.0.1
```

注意：

- `.env` 不得提交到 Git。
- `JWT_SECRET` 和 `TOTP_ENCRYPTION_KEY` 部署后必须保持不变。
- `ADMIN_EMAIL` 和 `ADMIN_PASSWORD` 主要用于首次初始化，系统创建管理员后，修改它们不会自动修改现有管理员。
- 使用 Caddy 或 Nginx 提供 HTTPS 时保持 `BIND_HOST=127.0.0.1`。
- 只有在暂时需要直接访问 `http://176.122.183.195:8080` 时，才设置 `BIND_HOST=0.0.0.0`，并通过防火墙限制访问来源。

### 5. 上传镜像

在本机执行：

```bash
cd /Users/yikangcao/Documents/sub2api
IMAGE_TAG="$(git rev-parse --short=12 HEAD)"

docker save "sub2api-custom:${IMAGE_TAG}" \
  | gzip \
  | ssh bwg-ssh 'gunzip | sudo docker load'
```

将镜像版本写入服务器 `.env`：

```bash
ssh bwg-ssh "cd /opt/sub2api && \
  if grep -q '^IMAGE_TAG=' .env; then
    sed -i.bak 's/^IMAGE_TAG=.*/IMAGE_TAG=${IMAGE_TAG}/' .env
  else
    printf '\nIMAGE_TAG=%s\n' '${IMAGE_TAG}' >> .env
  fi"
```

### 6. 启动服务

```bash
ssh bwg-ssh '
  cd /opt/sub2api
  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml \
    up -d
'
```

检查容器和日志：

```bash
ssh bwg-ssh '
  cd /opt/sub2api
  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml \
    ps

  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml \
    logs --tail=200 sub2api
'
```

健康检查：

```bash
ssh bwg-ssh '
  for attempt in $(seq 1 30); do
    if curl -fsS http://127.0.0.1:8080/health; then
      exit 0
    fi
    sleep 5
  done
  echo "Sub2API health check failed" >&2
  exit 1
'
```

首次启动会自动执行数据库迁移，可能需要等待一段时间。

## 三、日常更新

### 1. 同步并构建新版本

```bash
cd /Users/yikangcao/Documents/sub2api
git switch hhapi/main
git pull --ff-only origin hhapi/main

IMAGE_TAG="$(git rev-parse --short=12 HEAD)"
FULL_COMMIT="$(git rev-parse HEAD)"

docker buildx build \
  --platform linux/amd64 \
  --build-arg VERSION="custom-${IMAGE_TAG}" \
  --build-arg COMMIT="${FULL_COMMIT}" \
  --tag "sub2api-custom:${IMAGE_TAG}" \
  --load \
  .
```

### 2. 上传新镜像

```bash
docker save "sub2api-custom:${IMAGE_TAG}" \
  | gzip \
  | ssh bwg-ssh 'gunzip | sudo docker load'
```

如果上游修改了 Compose 配置，同时更新服务器配置：

```bash
scp deploy/docker-compose.local.yml \
  bwg-ssh:/opt/sub2api/docker-compose.yml
```

该操作不会覆盖 `.env` 和数据目录。

### 3. 更新前备份数据库

服务器只保留最近一次备份：新备份生成并校验通过后，删除 `backups/` 下的其余内容。

```bash
ssh bwg-ssh "IMAGE_TAG=${IMAGE_TAG} bash -s" <<'EOF'
set -e
cd /opt/sub2api
mkdir -p backups
latest="backups/sub2api-$(date +%Y%m%d-%H%M%S)-pre-${IMAGE_TAG}.dump"
sudo docker exec sub2api-postgres \
  pg_dump -U sub2api -Fc sub2api > "$latest"
test -s "$latest"
sudo docker exec -i sub2api-postgres pg_restore -l < "$latest" > /dev/null
find backups -mindepth 1 -maxdepth 1 ! -path "$latest" -exec rm -rf {} +
ls -lh backups
EOF
```

备份失败或校验不通过时，`set -e` 会中止脚本，旧备份不会被删除。

Sub2API 的数据库迁移是单向的。包含数据库迁移的版本不能只靠切换旧镜像完整回滚，必要时需要同时恢复数据库备份。

### 4. 切换到新版本

```bash
ssh bwg-ssh "cd /opt/sub2api && \
  if grep -q '^IMAGE_TAG=' .env; then
    sed -i.bak 's/^IMAGE_TAG=.*/IMAGE_TAG=${IMAGE_TAG}/' .env
  else
    printf '\nIMAGE_TAG=%s\n' '${IMAGE_TAG}' >> .env
  fi

  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml \
    up -d sub2api"
```

随后执行健康检查：

```bash
ssh bwg-ssh '
  for attempt in $(seq 1 30); do
    if curl -fsS http://127.0.0.1:8080/health; then
      exit 0
    fi
    sleep 5
  done

  cd /opt/sub2api
  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml \
    logs --tail=200 sub2api
  exit 1
'
```

## 四、回滚

查看服务器已有镜像：

```bash
ssh bwg-ssh 'sudo docker images sub2api-custom'
```

选择旧的 `IMAGE_TAG`，然后执行：

```bash
OLD_IMAGE_TAG=替换为旧版本标签

ssh bwg-ssh "cd /opt/sub2api && \
  sed -i.bak 's/^IMAGE_TAG=.*/IMAGE_TAG=${OLD_IMAGE_TAG}/' .env && \
  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml \
    up -d sub2api"
```

如果新版本已经执行了不兼容的数据库迁移，应停止服务并恢复对应的 PostgreSQL 备份，而不是只切换旧镜像。

## 五、常用运维命令

查看状态：

```bash
ssh bwg-ssh '
  cd /opt/sub2api
  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml ps
'
```

查看应用日志：

```bash
ssh bwg-ssh '
  cd /opt/sub2api
  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml logs -f sub2api
'
```

重启应用：

```bash
ssh bwg-ssh '
  cd /opt/sub2api
  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml restart sub2api
'
```

停止全部服务但保留数据：

```bash
ssh bwg-ssh '
  cd /opt/sub2api
  sudo docker compose \
    -f docker-compose.yml \
    -f docker-compose.custom.yml down
'
```

不要执行 `docker compose down -v`，否则可能删除数据库和 Redis 数据卷。当前配置虽然使用本地目录保存数据，但生产操作中仍应避免养成使用 `-v` 的习惯。

## 六、重要注意事项

1. 不要在 Sub2API 管理后台使用官方“在线更新”，否则可能将二开程序替换为官方版本。
2. 生产部署只使用已经提交的 Git Commit，不直接部署未提交的本地修改。
3. 每次升级前备份 PostgreSQL，尤其是在同步上游数据库迁移之后；服务器只保留最近一次备份。
4. 不要覆盖服务器上的 `.env`、`data`、`postgres_data` 和 `redis_data`。
5. 不要把 PostgreSQL、Redis 端口直接暴露到公网。
6. 建议通过 Caddy 或 Nginx 配置域名和 HTTPS，不建议长期暴露 `8080`。
7. 定期将 `/opt/sub2api/backups` 复制到另一台机器或对象存储；服务器本机备份不能防止整机故障。
