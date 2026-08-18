# AGENTS.md

面向 AI 编码代理的本仓库开发指引：架构约定、模块开发流程与踩坑记录。修改代码前请先读一遍。

## 项目概览

- Go + Gin 的模块化 API 服务（module `github.com/thun888/apibox`，Go 1.25+）。
- 每个功能是 `internal/api/modules/<name>/` 下的独立包，通过 `init()` 自我注册，
  统一挂载到 `/api/<module_name>` 路由组；共享配置、数据库（GORM）、Redis 与 CookieCloud。
- 入口 `cmd/server/main.go`；构建见 `build.bat`（注入版本号，交叉编译 Windows/Linux）。

## 目录结构与职责

| 路径 | 职责 |
|------|------|
| `cmd/server/main.go` | 启动：配置 → 数据库 → Redis → 路由 → 优雅退出 |
| `internal/api/registry.go` | `Controller` 接口、`RegisterController`、`SetupRouter`、`api.Shutdown()` |
| `internal/api/modules/` | 各功能模块，`modules.go` 匿名导入汇总 |
| `internal/config/config.go` | 全局 `config.Cfg`，含 `ModulesConfig` |
| `internal/database/database.go` | 全局 `database.DB`（GORM），`RegisterModel`/`BuildTableName` |
| `internal/cache/redis.go` | 全局 `cache.Client`，`addr` 为空时跳过（Redis 可选） |
| `internal/utils/` | `NewModuleLogger`、`CheckReferer` 等共享工具 |
| `useless/` | 遗留旧代码（user/order 模块），**未接入** `modules.go`，构建时需排除（见下文） |
| `dist/` | 构建产物 |

## 启动与生命周期

1. `config.Load()` 读工作目录 `config.yaml` → 写入全局 `config.Cfg`。
2. `database.Init`（GORM，支持 mysql/postgres/sqlite；`auto_migrate` 时迁移所有
   `RegisterModel` 注册的模型）。
3. `cache.Init`（Redis，未配置 `addr` 则跳过）。
4. `api.SetupRouter` 遍历所有已注册 Controller，`Enabled()` 为真的才挂载路由
   （因此模块默认禁用，配置加载完成后路由才可见）。
5. 优雅退出（SIGINT/SIGTERM）：`api.Shutdown()` → `cache.Close()` → `database.Close()` → exit。
   模块如需退出前收尾（如把内存缓冲刷库），实现可选方法 `Shutdown()` 即可被调用。

## 命名与配置约定

- **包目录**：小写复合词 `hitcount`、`qqmailhead`。
- **`moduleName` 常量（= 路由前缀）**：蛇形 `hit_count`、`qq_mail_head`。
- **配置 yaml 键**：小写复合词 `modules.hitcount:`；结构体名 `HitCountConfig`，
  字段 `Enable *bool` + `Enabled()`（未配置默认禁用）。
- **表名**：`database.BuildTableName(&Hit{}, "hitcount_")` → `hitcount_hits`
  （前缀 + GORM 默认复数蛇形）。模块表前缀用小写复合词（`starvote_`、`hitcount_`）。
- **时长配置**：yaml.v3 原生支持 `time.Duration`（`5s`、`5m`），直接声明
  `time.Duration` 字段即可；≤0 时在代码里回退默认值。
- **日志**：`var log = utils.NewModuleLogger(moduleName)`，自动带 `module=` 属性。

## 新增模块清单

1. 新建 `internal/api/modules/<name>/`，实现并注册 Controller：

```go
const moduleName = "hit_count" // 蛇形，即路由前缀

type Controller struct{}

func init() { api.RegisterController(&Controller{}) }

func (c *Controller) Register(r *gin.RouterGroup) { r.GET("/*path", handle) }
func (c *Controller) ModuleName() string          { return moduleName }
func (c *Controller) Enabled() bool               { return config.Cfg.Modules.HitCount.Enabled() }
```

2. `internal/config/config.go` 的 `ModulesConfig` 增加字段，并写 `Enabled()` 方法。
3. `config.example.yaml`（必改）与 `config.yaml`（按需）补充模块配置段。
4. 需要表时定义 GORM 模型 + `init()` 里 `database.RegisterModel`（见下）。
5. `internal/api/modules/modules.go` 追加一行匿名导入（忘记会静默 404）。
6. README：模块表加一行 + 补 API 说明小节。
7. 需要退出前收尾时给 Controller 加 `Shutdown()` 方法（如 hitcount 的 flush）。

## 常用代码模式

**数据库模型 + 表名**：

```go
type Hit struct {
    Path      string    `gorm:"column:path;primaryKey;size:255" json:"path"`
    Count     int64     `gorm:"column:count;not null;default:0" json:"count"`
    CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"createdAt"`
    UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updatedAt"`
}

func (Hit) TableName() string { return database.BuildTableName(&Hit{}, "hitcount_") }

func init() { database.RegisterModel(&Hit{}) }
```

**计数类 Upsert**（累加已有列，参考 starvote/hitcount）：

```go
database.DB.Clauses(clause.OnConflict{
    Columns: []clause.Column{{Name: "path"}},
    DoUpdates: clause.Assignments(map[string]interface{}{
        "count":      gorm.Expr("? + ?", clause.Column{Name: "count"}, delta),
        "updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
    }),
}).Create(&row)
```

`gorm.Expr` 的 `?` 参数传 `clause.Column` 会按方言正确加引号（gorm v2 `AddVar` 行为）。

**Referer 白名单**（需要时）：`utils.CheckReferer(config.Cfg.Modules.X.AllowedReferers, ctx)`，
失败返回 403。SVG 徽章/头像类接口（star_history、hit_count、qqmail_head）不做校验，
因为通常内嵌于 README/第三方页面，没有 Referer。

**后台定时任务**：惰性启动（`sync.Once` + 首次使用时启动 goroutine），
`Shutdown()` 里停表并同步一次。缓冲先整体换出再写库，失败的数据合并回缓冲下轮重试，
避免丢数据（见 `hitcount/counter.go`）。

**gin 路由细节**：
- 通配参数 `/*path` 的值**带前导斜杠**（`/a/b.svg`），处理前需 `strings.TrimPrefix`。
- 不支持部分段参数（`:repo.svg` 这种匹配不到后缀），需要按后缀分发时用通配 + handler 内判断。
- 计数/动态内容响应必须加 `Cache-Control: no-store`，防止 CDN 缓存。

## 现有模块速查

| 模块 | 路由前缀 | 数据依赖 |
|------|----------|----------|
| biliinfo | `/api/bili_info` | CookieCloud（可选）、Redis 1h 缓存 |
| qqmailhead | `/api/qqmail_head` | CookieCloud（必需） |
| starvote | `/api/star_vote` | 数据库 |
| genlineanimation | `/api/gen_line_animation` | 无 |
| hitcount | `/api/hit_count` | 数据库（内存缓冲 5 分钟同步） |
| starhistory | `/api/star_history` | GitHub Token + Redis/数据库两级缓存 |
| siteinfo | `/api/site_info` | Redis（可选）、上游代理（可选） |
| pathproxy | `/api/path_proxy` | 无（每条规则单独 Referer 白名单） |

## 开发经验与注意事项（踩坑记录）

- **不要跑 `go build ./...`**：`useless/` 里的遗留 .go 文件会触发 go 向 proxy 抓取
  `github.com/thun888/apibox@v1.10.2` 的版本信息（写入用户目录的模块下载缓存），
  在受限环境下直接被拒。构建只针对 `./cmd/... ./internal/...`；必要时把
  `GOCACHE` 重定向到仓库内目录。
- **Go 函数与类型共用命名空间**：模块内函数名不要与模型类型名撞车
  （例如 `Hit` 模型与 `func Hit()` 会 `redeclared`，改名为 `Incr` 之类）。
- **不要用 PowerShell 重写含中文的 UTF-8 文件**：`Set-Content` 默认按系统 ANSI
  编码写盘，会把中文注释写坏成乱码。一律用文件编辑工具（write/edit）。
- 模块默认禁用是刻意的：改代码后如果接口 404，先查配置 `enable: true` 与
  `modules.go` 导入。
- `database.DB` / `cache.Client` 在 `main` 初始化后才可用；模块 `init()` 只能做
  注册（RegisterController/RegisterModel），不能碰连接。
- 写库失败要有重试路径（hitcount 的 merge-back），不要静默丢计数类数据。
- 配置里 `enable` 用 `*bool`：yaml 缺省时为 nil，`Enabled()` 返回 false，
  避免「注释掉配置反而启用」的语义反转。

## 工作约定

- 不新增单元测试文件；不运行 `go build`/`go test`/`go vet` 做验证，编译与验证由维护者完成。
- 修改 README 与 `config.example.yaml` 时保持中文注释风格，与现有段落格式一致。
- 参考外部项目移植功能时（如 dwyl/hits）：沿用其 API 语义（URL 形态、响应格式），
  但按本仓库约定重新设计存储与配置。
