# ApiBox

基于 Go + Gin 的模块化 API 服务

各功能以独立模块形式挂载到 `/api/<模块名>` 路由组下，共享数据库、Redis 缓存与 [CookieCloud](https://github.com/easychen/CookieCloud) Cookie 管理

## 内置模块

作为个人使用项目，重构实现了部分自己有需求的api接口，感谢原作者的付出❤️

| 模块 | 路由前缀 | 功能 | 外部依赖 |
|------|----------|------|----------|
| [biliinfo](https://github.com/thun888/biliinfo) | `/api/biliinfo` | 代理 Bilibili 视频信息接口 | CookieCloud（可选） |
| [qqmail_head](https://github.com/thun888/qq-mail-head) | `/api/qqmail_head` | 获取 QQ 邮箱头像 | CookieCloud（必需） |
| [starvote](https://github.com/xaoxuu/star-vote) | `/api/starvote` | 投票与评分 | 数据库 |
| [genlineanimation](https://github.com/jrenc2002/GenLineAnimation-Server) | `/api/genlineanimation` | 生成手写签名动画 SVG | 无 |
| [starhistory](https://github.com/Mubelotix/star-history)（移植） | `/api/starhistory` | 生成 GitHub 星标历史图表 SVG | GitHub Token（需仓库管理员/协作者） |

模块默认启用，可通过 `modules.<name>.enable: false` 关闭（见[配置](#配置)）。

## 快速开始

### 1.下载

从[Releases](https://github.com/thun888/apibox/releases)下载对应架构的发行版及仓库内的`config.example.yaml`

### 2. 配置

重命名`config.example.yaml`为`config.yaml`，按需调整相关配置

完整配置项见 [config.example.yaml](config.example.yaml)

## 开发

### 1. 环境要求

- Go 1.25+（见 [go.mod](go.mod)）

### 3. 构建与运行

```bash
go run ./cmd/server

# 或构建二进制（未注入版本号时 /ping 返回 "dev"）
go build -ldflags="-s -w -X github.com/thun888/apibox/internal/api.Version=v1.2.3" -o apibox ./cmd/server
```

### 4. 验证

```bash
curl http://localhost:8080/ping
# {"message":"pong","version":"dev"}

curl "http://localhost:8080/api/biliinfo/get_video_info?bvid=BV1xx411U7xx" \
  -H "Referer: http://localhost:4000/"
```

## API

<details>
<summary>点击展开</summary>

除特别说明外，模块接口均要求 Referer 头命中该模块的 `allowed_referers` 白名单，否则返回 403。

### 通用

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/ping` | 健康检查，返回 `{"message":"pong","version":"<版本>"}` |
| GET | `/` | 302 重定向到本仓库 |

### biliinfo — Bilibili 视频信息

`GET /api/biliinfo/get_video_info`

| 参数 | 必填 | 说明 |
|------|------|------|
| bvid | 是 | 视频 BV 号 |

- 请求 `https://api.bilibili.com/x/web-interface/view`，响应体原样透传。
- 配置了 Redis 时缓存 1 小时（key：`video:bvid:<bvid>`）。
- 配置了 CookieCloud 时附带 `.bilibili.com` 域 Cookie；获取失败仅记录警告，继续请求。
- 错误响应：400 `{"error":"missing bvid"}`（缺少 bvid）；502 `{"error":"upstream error"}`（上游请求失败）。

### qqmail_head — QQ 邮箱头像

`GET /api/qqmail_head/:email`，返回头像图片（Content-Type 来自上游，缺省 `image/png`）。

- 响应头 `Cache-Control: public, max-age=2592000`（30 天）。
- 依赖 CookieCloud 提供 `.mail.qq.com` 域 Cookie，获取失败返回 500。
- 错误响应：400 `{"error":"Invalid email format"}`；500 `{"error":"Failed to get cookies"}`；502 上游失败（消息中包含上游状态码）。

示例：

```bash
curl -o avatar.png "http://localhost:8080/api/qqmail_head/123456@qq.com"
```

### starvote — 投票与评分

数据持久化到数据库（表 `starvote_votes`、`starvote_ratings`），使用 UPSERT（`ON CONFLICT DO UPDATE`）累加计数。

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/starvote/vote/update` | 投票 +1：`value` 为 `up`/`down` |
| POST | `/api/starvote/rating/update` | 评分 +1：`value` 为 1–5 的数字（小数向上取整） |
| GET | `/api/starvote/vote/info` | 查询投票数 |
| GET | `/api/starvote/rating/info` | 查询评分分布 |

- 参数 `id` 可放在表单（`application/x-www-form-urlencoded`）或查询字符串中，表单优先；GET 接口的 `id` 缺省为 `default`。
- 更新成功返回 `{"success":"true"}`；参数缺失或非法返回 400（`{"code":400,"message":"Bad Request"}`）；数据库错误返回 500。
- 查询不存在的 id 时返回全 0 的结果。

示例：

```bash
curl -X POST "http://localhost:8080/api/starvote/vote/update" \
  -H "Referer: http://localhost:4000/" \
  -d "id=test&value=up"
```

### genlineanimation — 手写签名动画

`GET /api/genlineanimation/signature`

| 参数 | 缺省 | 说明 |
|------|------|------|
| name | `Signature` | 要绘制的文本 |
| animate | `false` | 为 `true` 时按笔画顺序播放动画 |
| speed | `1` | 动画速度倍率（须 > 0） |
| color | `#000000` | 笔画颜色 |

返回 SVG（`image/svg+xml`），响应头 `Cache-Control: public, max-age=31536000`。

### starhistory — GitHub 星标历史图表

[star-history](https://github.com/Mubelotix/star-history) 的 SVG 生成能力移植，`GET /api/starhistory/svg`。

| 参数 | 缺省 | 说明 |
|------|------|------|
| repos | 必填 | 仓库列表，逗号分隔（最多 20 个），如 `thun888/apibox,microsoft/vscode` |
| type | `date` | X 轴模式：`date`（绝对日期）/ `timeline`（相对首日的时长） |
| size | `laptop` | 图表宽度：`mobile`(600) / `laptop`(800) / `desktop`(1000) |
| theme | `light` | `dark` / `light` |
| transparent | 无 | 为 `true` 时背景透明 |
| legend | `top-left` | 图例位置：`top-left` / `bottom-right` |
| logscale | 无 | 只要出现且值不为 `false` 即启用 Y 轴对数刻度 |

- 返回 SVG（`image/svg+xml;charset=utf-8`），响应头 `Cache-Control: public, s-maxage=86400, max-age=86400`。
- 仓库名大小写不敏感：非小写请求会 301 到规范 URL。
- 数据源：GitHub stargazers API（`GET api.github.com/repos/{repo}/stargazers`，`Accept: application/vnd.github.star+json` 返回带 `starred_at` 的列表，按日期聚合为累计星标数），与原项目流水线的数据语义一致。2026-06-30 起 GitHub 将该 API 限制为仓库管理员/协作者可见，因此需在 `secrets.github_token` 配置令牌，且只能生成本人拥有/协作仓库的图表。
- 一次请求最多 20 个仓库，未命中的仓库并发抓取（翻页 `per_page=100`）；头像取仓库 owner 的 `avatar_url`（`&s=22`）并内联为 base64 data URL。
- 星标数据缓存 24 小时：优先 Redis，未命中读数据库表 `starhistory_star_data_caches`（配置了数据库时自动启用），仍未命中才请求 GitHub API。回源时复用过期的库缓存行做增量抓取——总数持平只翻最后一页确认（2 次 API 调用即完成刷新），总数增加只翻新增尾页并合并，合并结果与 `stargazers_count` 校验不符或总数减少时回退全量；回源结果写回两级缓存。渲染结果（SVG）仅 Redis 缓存。
- 不校验 Referer（SVG 通常内嵌于 README / 卡片场景）。
- 错误响应：400（缺少 `repos` / 超过 20 个仓库）；404（仓库不存在、无权访问或星标记录少于 5 条，如 `Repo not found in dataset: xxx/yyy`）；502（GitHub API 失败、令牌无效或配额耗尽）；503（`style=landscape1`，OG 卡片已禁用）。

示例：

```bash
curl -o stars.svg "http://localhost:8080/api/starhistory/svg?repos=thun888/apibox&theme=dark"
```

</details>

## 架构

### 模块注册

每个模块实现 `Controller` 接口，通过 `init()` 自动注册；main 中只需匿名导入 `internal/api/modules` 汇总包：

```go
type Controller interface {
    Register(r *gin.RouterGroup) // 注册子路由；Group 已带 /api/<模块名> 前缀
    ModuleName() string          // 模块名，即路由前缀
    Enabled() bool               // 返回 false 时跳过路由注册
}

func RegisterController(c Controller)
```

启动流程（[cmd/server/main.go](cmd/server/main.go)）：

1. 加载 `config.yaml`
2. 初始化数据库（`auto_migrate: true` 时自动迁移所有已注册模型）
3. 初始化 Redis（`addr` 为空则跳过）
4. 注册全局中间件（CORS、可信代理）与 `/`、`/ping` 路由
5. 遍历已注册模块，按 `Enabled()` 决定是否挂载

### 基础设施

模块通过包级变量共享基础设施：

| 组件 | 位置 | 说明 |
|------|------|------|
| 数据库 | `database.DB`（GORM） | 支持 MySQL/PostgreSQL/SQLite；模型通过 `database.RegisterModel` 注册 |
| 缓存 | `cache.Client`（go-redis） | 未配置时为 nil，使用前需判空 |
| 日志 | `utils.NewModuleLogger` | slog 文本输出到 stdout，自动带 `module` 属性 |
| 配置 | `config.Cfg` | 模块配置在 `ModulesConfig`，共享凭据在顶层 `secrets` 段 |

### 请求流程（以 biliinfo 为例）

```
请求进入 gin 路由
  → CORS / 可信代理中间件
  → 模块 handler：Referer 白名单校验（失败 403）
  → 查询 Redis 缓存（命中则直接返回）
  → 带 UA/Referer/Cookie 请求上游 API（超时 10s）
  → 写入缓存（1h）→ 返回
```

## 安全

- **Referer 白名单**：`modules.<name>.allowed_referers`，对 Referer 头的主机名做后缀匹配；Referer 缺失或不在白名单返回 403。biliinfo、starvote、genlineanimation 启用了此校验，qqmail_head、starhistory 未启用（前者头像图片、后者 SVG 图表通常内嵌于第三方页面）。
- **CORS**：`server.allowed_origins`。包含 `"*"` 时允许任意 Origin（回显请求 Origin，配合 Credentials 不能直接返回 `*`）；否则按列表精确匹配。
- **可信代理**：`server.trusted_proxies` 传给 gin 的 `SetTrustedProxies`，影响 `c.ClientIP()`。

## CookieCloud

[internal/utils/cookie.go](internal/utils/cookie.go) 实现了从 CookieCloud 服务获取并解密 Cookie 的客户端：

- 接口：`GET {host}/get/{uuid}`，`crypto_type` 非空且非 `legacy` 时作为查询参数。
- 解密算法：`legacy`（CryptoJS/OpenSSL 格式，缺省）或 `aes-128-cbc-fixed`。
- 结果按域名过滤（`.bilibili.com`、`.mail.qq.com`）后注入上游请求头，不返回给调用方。
- 配置了 Redis 时，解密结果缓存 1 小时。

## 开发：添加新模块

1. 新建 `internal/api/modules/<name>/`，实现 `Controller` 并在 `init()` 中注册：

```go
package newmodule

import (
    "github.com/gin-gonic/gin"
    "github.com/thun888/apibox/internal/api"
    "github.com/thun888/apibox/internal/config"
)

const moduleName = "newmodule"

type Controller struct{}

func init() { api.RegisterController(&Controller{}) }

func (c *Controller) Register(r *gin.RouterGroup) {
    r.GET("/hello", func(ctx *gin.Context) { ctx.String(200, "hello") })
}
func (c *Controller) ModuleName() string { return moduleName }
func (c *Controller) Enabled() bool      { return config.Cfg.Modules.NewModule.Enabled() }
```

2. 在 [internal/api/modules/modules.go](internal/api/modules/modules.go) 中追加匿名导入。
3. 需要配置项时，在 [internal/config/config.go](internal/config/config.go) 的 `ModulesConfig` 中增加字段，并在 `config.example.yaml` 中补充示例。
4. 需要数据库表时，定义 GORM 模型并在 `init()` 中调用 `database.RegisterModel`（`auto_migrate: true` 时启动自动建表）。表名通过 `database.BuildTableName` 加模块前缀：

```go
func (Vote) TableName() string { return database.BuildTableName(&Vote{}, "starvote_") } // → starvote_votes
```

## FAQ

**接口返回 403？**
Referer 头缺失，或其主机名不在对应模块的 `allowed_referers` 白名单中（后缀匹配）。qqmail_head 不校验 Referer。

**starhistory 返回 404？**
2026-06-30 起 GitHub 将 stargazers API 限制为仓库管理员/协作者可见，因此只能生成本人拥有/协作仓库的图表（其他仓库返回 `Repo not found in dataset`）；星标记录少于 5 条的仓库同样返回 404。

**如何禁用某个模块？**
在 `config.yaml` 中设置 `modules.<name>.enable: false`，该模块的路由将不会注册。

**qqmail_head 返回 500？**

该模块依赖 CookieCloud。确认 `cookiecloud` 配置里的 `host`/`uuid`/`password` 正确、`GET {host}/get/{uuid}` 可访问、`crypto_type` 与服务端一致（`legacy` 或 `aes-128-cbc-fixed`）。

**配置了 Redis 但启动失败？**
启动时会先 ping 一次 Redis，连不上则直接退出。不使用 Redis 时将 `redis.addr` 留空。

## License

[MIT](./LICENSE)
