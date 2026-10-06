# OpenIPShift

![OpenIPShift mark](frontend/public/openipshift.svg)

**An OpenRealm project** · IPv4 rotation simulation console.

中文概要：OpenIPShift 支持自定义节点数量（首次启动为零节点，可按需新增、编辑、删除）和访问 Token 登录。**当前仅 Mock 模拟**，尚未实现真实 AWS Lightsail 操作、DNS 更新或代理协议验证。

Go backend + Next.js console for rotating static IPv4 on a user-defined number of V2Board/V2bX Lightsail nodes. The delivered MVP defaults to **mock mode**: it does not call AWS, Cloudflare, or any proxy.

## Node Management And Access

Fresh installations start with zero nodes. Add, edit, or delete any number of nodes in the console. Existing state files are preserved. Node configuration is persisted with task history; unresolved tasks block editing or deleting the corresponding node.

Set `ROTATOR_API_TOKEN` on the backend and enter that token on the console login screen. All backend routes require Bearer authentication; missing configuration or a wrong token returns 401. Rotation requests also require an Idempotency-Key. The frontend does not poll before login. Tokens remain in page memory only, never in localStorage, URLs, or public environment variables. Logout clears the token and displayed data.

Node routes: `GET/POST /api/nodes`, `PUT/DELETE /api/nodes/:id`. IDs are unique and immutable. Each record includes name, AWS region, instance name, current IPv4, DNS hostname, and proxy host:port.

## Run

Backend (requires Go 1.22+):

```bash
cd backend
export ROTATOR_API_TOKEN="$(openssl rand -hex 32)"
# Enter this generated value in the console; do not commit or share it.
export STATE_PATH="$PWD/data/state.json"
go run ./cmd/server
```

The backend binds to `127.0.0.1:8080` by default. It refuses non-loopback binding unless `ALLOW_NON_LOOPBACK=1` is explicitly set. The POST API requires `Authorization: Bearer $ROTATOR_API_TOKEN` and an `Idempotency-Key`.

Frontend (requires Node 22+):

```bash
cd frontend
npm ci
npm run dev
```

Open <http://localhost:3000>, enter the same token, and add a node, then use “模拟轮换”. The UI polls `/api/state` and displays each task's progress log.

## Docker / GHCR（推荐部署）

**仍然是 MOCK 模拟**，不会修改 AWS、DNS 或代理配置。两个镜像分别运行 Go API 和 Next.js standalone 服务，均以 UID/GID `10001` 非 root 运行。Compose 只暴露前端，后端没有宿主机端口；状态保存在 `state` 命名卷，重启/升级不会丢失。不要执行 `docker compose down -v`，除非你确实要删除状态。

```bash
# 在仓库根目录执行；保存生成的 token 到密码管理器，登录页面需要它。
export ROTATOR_API_TOKEN="$(openssl rand -hex 32)"
docker compose pull
docker compose up -d --no-build
# 默认只绑定本机 http://127.0.0.1:3000
```

也可本地构建：`docker compose up -d --build`。必须先设置非空 `ROTATOR_API_TOKEN`；也可复制 `.env.example` 为 **不入库的** `.env`，替换占位 token。Token 只在运行时传给后端，不在 build args、镜像或浏览器构建环境中。

镜像：
- `ghcr.io/openrealmcn/openipshift-backend:main`
- `ghcr.io/openrealmcn/openipshift-frontend:main`

`main` / `latest` 为主分支滚动标签；发布 `v1.2.3` 格式 tag 会生成 `1.2.3` / `1.2` / `latest`，每次发布还有 `sha-<短提交>`。生产可用 `IMAGE_TAG=sha-<短提交>` 固定版本，或在 Compose 中将 image 固定为 `@sha256:...`。发布后 `docker compose pull && docker compose up -d --no-build` 更新服务。

浏览器只请求当前站点的 `/api/*`，Next 服务使用运行时、server-only `API_INTERNAL_URL=http://backend:8080` 转发；不存在浏览器访问自己 `127.0.0.1:8080` 的部署问题。登录 token 由浏览器在请求头传递，不由代理注入或缓存。

远程访问请在前端前面配置可信 HTTPS 反向代理；**不要将 Bearer token 通过明文 HTTP 在公网传输**。默认 loopback 绑定可使用 SSH 隧道。需要在受保护内网暴露时设置 `BIND_ADDR`，切勿直接公开后端。非 Docker 开发时，`API_INTERNAL_URL` 默认指向本机后端。

`.github/workflows/docker-publish.yml` 在 `main` push、`vX.Y.Z` tag push 或手动运行时测试并发布；PR 仅测试/构建，不登录、不推送。GHCR 登录使用内置 `GITHUB_TOKEN`（`contents: read` / `packages: write`），无需额外凭证。Docker Buildx 使用 GitHub Actions cache，镜像名称统一小写。当前构建目标为 Linux amd64。

**包可见性独立于仓库可见性**：若 `docker compose pull` 提示 denied，包 owner 需要在 GitHub Packages → 对应包 → Package settings → Change visibility 设为 Public，或先 `docker login ghcr.io`（私有包拉取需要适当 `read:packages` 权限）。工作流在每个构建 job 的 Summary 中记录镜像 digest。

## Workflow and safety

The durable task state is written atomically to `STATE_PATH` with mode `0600`. A node can have only one unresolved task, and repeated requests with the same `Idempotency-Key` return the same task. The state machine is:

`planned -> allocated -> bound -> egress_verified -> dns_updated -> dns_verified -> proxy_verified -> cooling -> completed`

Every externally meaningful step is persisted before and after its call. If a call fails, the engine attempts to restore the old binding and DNS record, then enters `manual`; both IPs remain retained. On restart, an in-flight phase is treated as unknown and marked `manual`. It is never blindly replayed or released.

Deferred release is only attempted after the cooldown and after fresh egress, DNS, and proxy verification. It also requires provider ownership and “unattached” checks. Failure or uncertainty blocks release. Waiting for DNS TTL/cache expiry never proves old connections are healthy and is not used as proof of recoverability. IPv6 is not changed by this IPv4 workflow.

## Mock versus real capability

- `internal/adapters.MockProvider`: in-memory simulation using RFC 5737 documentation IPs; no AWS calls.
- `internal/adapters.MockDNS`: in-memory A records; no Cloudflare calls.
- `internal/adapters.MockVerifier`: simulated pass/fail only. Its “proxy verified” event explicitly says no real proxy request was made.
- `internal/adapters.EgressChecker`: optional real TCP reachability helper only. TCP reachability is not a proxy handshake and cannot be wired as proxy verification.
- AWS Lightsail SDK v2 and Cloudflare adapters are **not implemented** in this MVP. No claim of real allocation, binding, DNS update, protocol handshake, or release is made. Production adapters must implement ownership checks, provider request idempotency, actual egress observation from the node, and protocol-specific proxy handshakes.

## API

- `GET /healthz`: local health and mode.
- `GET /api/state`: nodes and all task logs.
- `POST /api/rotations` with `{"nodeId":"node-1"}`: starts one rotation; requires auth and `Idempotency-Key`.
- `POST /api/tick`: checks cooling tasks and may release only after all guards pass; requires auth.
- `GET /api/tasks/:id`: task detail.

The API is intentionally minimal and has bearer-token authentication only. Keep it on loopback or place it behind a properly authenticated trusted proxy; do not expose it directly to the Internet.

## Tests and verification

From `backend`, run `go test ./...` and `go vet ./...`. Tests cover proxy-failure rollback, durable state reload, idempotency and per-node locking, restart ambiguity, and refusal to release without ownership. From `frontend`, run `npm run typecheck` and `npm run build`.

Use Go 1.22+; if installed under `/tmp/go`, run `/tmp/go/bin/go test ./...` and `/tmp/go/bin/go vet ./...`. The frontend currently uses Next.js 14.2.35.

## Configuration

`.env.example` contains placeholders only. Do not commit AWS, Cloudflare, panel, or proxy credentials. Static-IP quotas, regional pricing, release semantics, DNS provider limits, and TTL behavior must be checked against current provider documentation/account state before any real adapter is enabled. Environment files are not loaded automatically by the backend: export backend settings in your shell. `frontend/.env.local` may contain server-only `API_INTERNAL_URL`, never an access token. Browser requests use same-origin `/api/*`; the Next.js server forwards authentication and idempotency headers to the backend. `NEXT_PUBLIC_API_URL` is no longer used. Local environment files, runtime state, keys, logs, dependency folders, and build artifacts must remain untracked.
