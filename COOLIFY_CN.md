# 使用 Coolify 部署此 Fork

本仓库使用 `coolify` 分支维护部署配置和后续二次开发，`main` 保持上游原样。Coolify 从此 Fork 的源码构建镜像，因此合并到 `coolify` 分支的代码会进入下一次部署。

## 1. 创建 Git 仓库应用

在 Coolify 的 Project / Environment 中选择 **Create New Resource**：

- 公开仓库可以通过 **Public Repository** 填入 `https://github.com/Nanako1900/CLIProxyAPI`。
- 如通过 GitHub App 管理仓库，在 **Sources** 中连接 GitHub App，授权此仓库后创建应用；私有仓库使用这一方式。

选择部署服务器和目标网络，并配置：

| 配置项 | 值 |
| --- | --- |
| Repository | `Nanako1900/CLIProxyAPI` |
| Branch | `coolify` |
| Build Pack | `Docker Compose` |
| Base Directory | `/` |
| Docker Compose Location | `/docker-compose.coolify.yml` |
| Compose deployment | 使用标准解析方式，保持 Raw 模式关闭 |

保存并加载 Compose 文件。如果希望代码合并后自动部署，可通过 GitHub App / Webhook 启用 Auto Deploy；手动部署也可以。

Compose 使用仓库原有的 `Dockerfile`，需要下载 Go 依赖并编译项目。构建耗时和内存、磁盘需求取决于服务器、缓存及网络，首次部署应观察构建日志和服务器资源。资源不足时，可改用独立构建服务器或由自己的 CI 构建 Fork 镜像；直接使用上游预构建镜像不会包含本仓库的二次开发。

## 2. 设置管理密码

在应用的 **Environment Variables** 中添加运行时变量：

```text
MANAGEMENT_PASSWORD=替换为独立生成的强随机密码
```

将 **Runtime** 设为可用，**Build time** 设为不可用，并保存变量。Coolify 构建时也会解析 Compose，但构建环境不包含仅运行时可用的密码；因此 Compose 允许构建阶段缺少此变量，容器启动时再检查。密码未设置、为空或只有空白字符时，容器会输出配置提示并退出，不会启动服务。

可在自己的终端执行 `openssl rand -hex 32` 生成密码，再粘贴到 Coolify。不要将实际密码提交到仓库或填入 Compose 文件。此密码用于管理界面和管理 API，与调用模型的客户端 API key 不同。

项目原生支持 `MANAGEMENT_PASSWORD`。非空时即使配置文件中的 `management.secret-key` 为空，也能通过该密码进行远程管理。修改这个环境变量后需要重新部署容器。不要在管理面板另设第二个管理密钥，以免产生多个有效入口。

## 3. 设置域名和 HTTPS

先将域名的 DNS A 记录指向部署服务器；使用 IPv6 时也核对 AAAA 记录。服务器需允许 Coolify 代理使用的公网 `80` / `443` 端口。

在应用的 **Domains** 中添加域名，选择 `cli-proxy-api` 服务：

| 字段 | 示例 |
| --- | --- |
| Protocol | `https` |
| Domain | `api.example.com` |
| Internal Port / Port | `8317` |
| Path | 留空 |

旧版 Coolify 若只有一个域名输入框，填写：

```text
https://api.example.com:8317
```

这里的 `8317` 是 Coolify 代理转发到的容器内部端口。浏览器和客户端实际访问 **`https://api.example.com`**，不带 `:8317`。HTTPS 由 Coolify 代理处理，应用内部使用 HTTP。

Compose 同时通过 `127.0.0.1:8317:8317` 将服务发布到服务器宿主机的回环地址。使用宿主机上的 Nginx、Caddy 等反向代理时，将整个域名转发到 `http://127.0.0.1:8317`；管理页为 `/management.html`，API 使用同一个端口。反向代理应支持 SSE 流式响应和 WebSocket。

如果反向代理也运行在 Docker 容器中，需加入 CPA 所在的网络，并使用该网络中的 CPA 服务名或别名（例如 `http://cli-proxy-api:8317`）；此时代理容器内的 `127.0.0.1` 指向代理容器自身。使用 Coolify 自带代理时，仍按上面的域名配置操作。端口映射仅绑定宿主机回环地址，无需将 `8317` 或 OAuth 回调端口暴露到公网。修改端口映射后需重新部署容器。

## 4. 部署、登录并接入账号

点击 **Deploy**，等待构建和启动完成，再检查 Runtime Logs。首次启动会：

1. 生成随机客户端 API key，并写入配置卷中的 `config.yaml`，密钥不会输出到启动日志。
2. 通过临时文件和原子重命名完成配置初始化，避免中断留下半份配置。
3. 使用持久化的配置和认证目录启动服务；已有非空配置不会被覆盖。

打开：

```text
https://api.example.com/management.html
```

管理服务器地址填写 `https://api.example.com`，管理密钥填写 Coolify 中的 `MANAGEMENT_PASSWORD`。管理页资源首次需要从 GitHub 下载，若出现 404，检查运行日志和服务器访问 GitHub 的网络状况。

登录后在 **API Keys / API 密钥** 页面复制自动生成的客户端密钥；需要时可在这里新增或轮换。接着在 OAuth / 认证相关页面添加自己使用的上游账号，或按项目配置添加上游 API key。容器启动成功不代表已经有可调用的上游模型。

### Codex 设备码登录

使用 Codex 账号时，可以在 Coolify 中打开 **`cli-proxy-api` 容器的 Terminal**，执行：

```sh
/CLIProxyAPI/CLIProxyAPI \
  --config /CLIProxyAPI/runtime/config.yaml \
  --codex-device-login
```

按终端提示，在自己的浏览器打开认证网址并输入设备码。此流程不依赖服务器浏览器或入站 OAuth 回调端口。必须保留上述 `--config` 路径，认证文件会存入本部署使用的持久化目录 `/root/.cli-proxy-api`。

## 5. 验证 API

在自己的终端执行，将域名和客户端密钥替换为实际值：

```sh
curl --fail-with-body https://api.example.com/v1/models \
  -H 'Authorization: Bearer 在管理页面复制的客户端API密钥'
```

返回的模型列表取决于已经接入的账号、上游及其权限。客户端采用 OpenAI 兼容协议时：

| 配置 | 值 |
| --- | --- |
| Base URL | `https://api.example.com/v1` |
| API key | 管理页面中的客户端 API key |
| Model | 从 `/v1/models` 返回值中选择 |

常见排查方向：

- **构建报 `MANAGEMENT_PASSWORD is missing a value`**：确认部署的提交已包含 `${MANAGEMENT_PASSWORD:-}` 和启动时密码检查，重新加载仓库中的 Compose 配置后部署。旧版 `${MANAGEMENT_PASSWORD:?…}` 会在构建阶段提前校验运行时密码。
- **容器提示 `Set MANAGEMENT_PASSWORD in Coolify runtime environment.`**：在生产环境变量中设置非空密码，确认 Runtime 可用、保存后重新部署。
- **502 / 网关不可达**：检查容器是否运行、域名是否选择 `cli-proxy-api` 服务、代理目标端口是否为 `8317`。
- **401**：API 调用要使用客户端 API key；管理页面使用 `MANAGEMENT_PASSWORD`。
- **管理界面 403**：核对运行时环境变量是否存在并重新部署。多次输错管理密码还会触发临时 IP 限制。
- **模型为空或调用失败**：先检查上游账号是否完成认证，以及服务器是否能访问对应服务。
- **启动提示配置无效**：查看 Runtime Logs；不要通过删除卷来尝试修复，先备份配置。

## 6. 持久化、日志和备份

| 命名卷 | 容器路径 | 内容 |
| --- | --- | --- |
| `cpa-config` | `/CLIProxyAPI/runtime` | 运行配置和客户端 API key |
| `cpa-auths` | `/root/.cli-proxy-api` | OAuth 凭据及本地状态 |
| `cpa-logs` | `/CLIProxyAPI/logs` | 应用可能生成的错误日志 |
| `cpa-plugins` | `/CLIProxyAPI/plugins` | 插件数据；插件默认关闭 |

Coolify 可能为卷名添加应用标识前缀。使用 Coolify 实际显示的卷名安排备份和恢复，并保持应用、Compose 服务及卷名稳定。重新部署容器会保留这些卷；删除应用或卷可能删除持久化数据。

运行配置保存在卷中，不保存在 Git。后续通过管理界面修改配置；源码或 Compose 更新不会自动覆盖已存在的配置。初始化模板的改动只影响新的空配置卷，需要对已有部署单独迁移。

普通运行日志输出到 stdout，方便在 Coolify 查看；代理请求/响应日志默认关闭。应用 `logs` 目录设置 100 MB 总量限制，并保留最多 10 个错误日志文件；Docker stdout 日志另设置为每文件 10 MB、最多 3 个文件。这两套限制分别生效。

配置、OAuth 凭据和上游 API key 均属于敏感数据，备份应保存在自己的受控位置，不要提交到 Git。更新前备份配置和认证卷；恢复后检查目录权限、上游认证和 API 调用。

## 7. 上游同步和二次开发

- `main` 跟踪 `router-for-me/CLIProxyAPI` 的上游 `main`，不直接加入本仓库的部署或业务改动。
- `coolify` 是本仓库默认分支，也是 Coolify 部署分支。部署配置和二次开发在这个分支维护。
- 完成 [UPSTREAM_SYNC.md](UPSTREAM_SYNC.md) 中的同步密钥和权限设置后，GitHub Actions 每 6 小时检查上游，先同步本仓库 `main`，再创建或更新 **`main` → `coolify`** 的 Pull Request。也可以在 Actions 中手动运行。尚未配置同步密钥时，工作流会报告缺少配置，不会修改分支。
- 在 PR 中检查上游变化、构建结果和冲突，再通过 **Create a merge commit / Merge pull request** 合并。请勿使用 Squash 或 Rebase 合并此同步 PR，以保留两条分支的共同祖先，便于后续增量同步。
- 不要在 `main` → `coolify` PR 上点击 **Resolve conflicts** 或 **Update branch**，这些操作会修改作为 PR 来源的 `main`。遇到冲突时，从最新 `coolify` 新建 `integration/upstream-<提交号>` 分支，将 `origin/main` 合入这个临时分支并解决冲突，再提交该分支 → `coolify` 的 PR。通过 Merge commit 合入后关闭原同步 PR，保持 `main` 原样。
- Coolify 开启自动部署时，PR 合并到 `coolify` 后会触发部署；否则手动点击 Deploy。GitHub Actions 计划任务可能延迟，6 小时为检查计划，不是部署时限。

## 官方参考

- [CLIProxyAPI 上游](https://github.com/router-for-me/CLIProxyAPI)
- [CLIProxyAPI 配置示例](https://github.com/router-for-me/CLIProxyAPI/blob/main/config.example.yaml)
- [Coolify Docker Compose 构建与配置](https://coolify.io/docs/applications/builds/docker-compose)
- [Coolify 域名与容器端口](https://coolify.io/docs/core/networking/domains)
- [Coolify 持久化卷](https://coolify.io/docs/core/persistent-storage/storage-mounts/volume-mounts)
