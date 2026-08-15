// Package starhistory 移植 star-history（github.com/Mubelotix/star-history）
// 的 SVG 星标历史图表生成能力，仅保留 /svg 图像接口。
//
// 与原项目架构一致：渲染前的数据来自 GitHub stargazers API（原项目为本地
// repos.sqlite，由外部流水线填充）。2026-06-30 起 GitHub 将该 API 限制为
// 仓库管理员/协作者可见，因此需配置 secrets.github_token，且只能生成
// 本人拥有/协作仓库的图表。
//
// 星标数据按 Redis → 数据库 → GitHub API 三级读取：Redis 未命中时查库
// （表 starhistory_star_data_caches，TTL 与 Redis 一致 24h，未配置数据库
// 时自动跳过），仍未命中才回源 GitHub。回源时把过期的库缓存行作为基准
// 增量复用（总数持平只翻最后一页确认、总数增加只翻新增尾页，结果与
// stargazers_count 校验不符则回退全量）；回源结果写回两级缓存。
//
// 渲染部分（坐标轴刻度、折线、图例、水印、ToolTip 占位）在 Go 中逐元素
// 复刻 JSDOM + d3 v2/v3 的输出，详见 chart.go / d3ticks.go。
package starhistory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/cache"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/utils"
)

const moduleName = "starhistory"

const (
	maxReposPerRequest = 20
	cacheTTL           = 24 * time.Hour // 与原项目保持一致
)

var log = utils.NewModuleLogger(moduleName)

var chartWidths = map[string]int{
	"mobile":  600,
	"laptop":  800,
	"desktop": 1000,
}

// Controller 星标历史图表模块
type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
}

func (c *Controller) ModuleName() string { return moduleName }

func (c *Controller) Enabled() bool {
	return config.Cfg.Modules.StarHistory.Enabled()
}

func (c *Controller) Register(r *gin.RouterGroup) {
	r.GET("/svg", handleSVG)
}

// handleSVG GET /api/starhistory/svg?repos=a/b,c/d&type=timeline|date&size=mobile|laptop|desktop&theme=dark|light&transparent=true&legend=bottom-right&logscale=true
//
// 参数语义与原项目 /svg 完全一致（大小写规范化 301、logscale 只要出现且
// 非 "false" 即启用、style=landscape1 返回 503）。
func handleSVG(ctx *gin.Context) {
	reposParam := ctx.Query("repos")
	if reposParam == "" {
		ctx.String(http.StatusBadRequest, "Repo name required")
		return
	}

	// 小写规范化（GitHub 仓库名不区分大小写），非规范请求 301 到规范 URL，
	// 与原项目一致，便于 CDN 缓存合并。
	repos := strings.Split(reposParam, ",")
	normalized := make([]string, 0, len(repos))
	for _, r := range repos {
		trimmed := strings.TrimSpace(strings.ToLower(r))
		if trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	canonical := strings.Join(normalized, ",")
	if canonical != reposParam {
		u := *ctx.Request.URL
		q := u.Query()
		q.Set("repos", canonical)
		u.RawQuery = q.Encode()
		ctx.Redirect(http.StatusMovedPermanently, u.RequestURI())
		return
	}
	if len(normalized) == 0 {
		ctx.String(http.StatusBadRequest, "Repo name required")
		return
	}
	if len(normalized) > maxReposPerRequest {
		ctx.String(http.StatusBadRequest, fmt.Sprintf("Too many repos: max %d per request", maxReposPerRequest))
		return
	}

	// OG 卡片样式在原项目已被禁用
	if ctx.Query("style") == "landscape1" {
		ctx.String(http.StatusServiceUnavailable, "OG cards are unavailable: GitHub API access has been disabled")
		return
	}

	// type: timeline|date；未指定时兼容旧版 timeline 参数
	xTickLabelType := "Date"
	if typeParam := ctx.Query("type"); typeParam != "" {
		if strings.ToLower(typeParam) == "timeline" {
			xTickLabelType = "Number"
		}
	} else if _, ok := ctx.GetQuery("timeline"); ok {
		xTickLabelType = "Number"
	}
	xLabel := "Date"
	if xTickLabelType == "Number" {
		xLabel = "Timeline"
	}

	// logscale：只要出现且值不是 "false" 即启用（原项目按 presence 判断）
	logscaleParam, hasLogscale := ctx.GetQuery("logscale")
	useLogScale := hasLogscale && logscaleParam != "false"

	theme := "light"
	if ctx.Query("theme") == "dark" {
		theme = "dark"
	}
	transparent := strings.ToLower(ctx.Query("transparent")) == "true"

	legendPosition := "top-left"
	if ctx.Query("legend") == "bottom-right" {
		legendPosition = "bottom-right"
	}

	size := ctx.Query("size")
	if _, ok := chartWidths[size]; !ok {
		size = "laptop"
	}
	chartWidth := chartWidths[size]

	svgCacheKey := fmt.Sprintf("%s|%s|%s|%s|%t|%s|%t",
		canonical, xTickLabelType, size, theme, transparent, legendPosition, useLogScale)

	// ---------- SVG 缓存 ----------
	if cache.Client != nil {
		if val, err := cache.Client.Get(ctx, "starhistory:svg:"+svgCacheKey).Result(); err == nil && val != "" {
			ctx.Data(http.StatusOK, "image/svg+xml;charset=utf-8", []byte(val))
			ctx.Header("Cache-Control", "public, s-maxage=86400, max-age=86400")
			return
		}
	}

	// ---------- 星标数据 ----------
	repoData, missing, err := loadReposData(ctx.Request.Context(), normalized)
	if err != nil {
		log.Error("fetch star data failed", "error", err)
		ctx.String(http.StatusBadGateway, "upstream error")
		return
	}
	if len(missing) > 0 {
		// 与原项目 /svg 一致：任一仓库不在数据集时返回 404
		ctx.String(http.StatusNotFound, "Repo not found in dataset: "+missing[0])
		return
	}
	datasets := make([]chartDataset, 0, len(repoData))
	for _, data := range repoData {
		datasets = append(datasets, dataToChartDataset(data, xTickLabelType))
	}

	svg := renderChart(renderOptions{
		title:          "Star History",
		xLabel:         xLabel,
		yLabel:         "GitHub Stars",
		datasets:       datasets,
		theme:          theme,
		transparent:    transparent,
		xTickLabelType: xTickLabelType,
		chartWidth:     chartWidth,
		useLogScale:    useLogScale,
		legendPosition: legendPosition,
	})

	if cache.Client != nil {
		if err := cache.Client.Set(ctx, "starhistory:svg:"+svgCacheKey, svg, cacheTTL).Err(); err != nil {
			log.Warn("set svg cache failed", "error", err)
		}
	}

	ctx.Header("Cache-Control", "public, s-maxage=86400, max-age=86400")
	ctx.Data(http.StatusOK, "image/svg+xml;charset=utf-8", []byte(svg))
}

// loadReposData 批量加载仓库星标数据：逐仓库优先读 Redis 缓存，未命中查
// 数据库缓存（24h，与 Redis 一致），仍未命中的并发抓取 GitHub stargazers
// API——抓取时把过期的库缓存行作为基准增量复用（见 github.go）。
// 返回按请求顺序排列的数据与缺失仓库列表（仓库不存在、无权访问或星标
// 记录过少）。
func loadReposData(ctx context.Context, repos []string) ([]*repoStarData, []string, error) {
	ordered := make([]*repoStarData, len(repos))

	for i, repo := range repos {
		if cache.Client != nil {
			if val, err := cache.Client.Get(ctx, "starhistory:data:"+repo).Result(); err == nil && val != "" {
				var c repoStarData
				if json.Unmarshal([]byte(val), &c) == nil {
					ordered[i] = &c
				}
			}
		}
	}

	// 数据库缓存兜底（Redis 未命中的仓库）：新鲜行直接命中，过期行作为
	// 增量抓取的复用基准（prev）
	dbMiss := make([]string, 0, len(repos))
	for i, repo := range repos {
		if ordered[i] == nil {
			dbMiss = append(dbMiss, repo)
		}
	}
	prev := make(map[string]*repoStarData, len(dbMiss))
	if len(dbMiss) > 0 {
		fromDB, fetched := dbLoadStarData(ctx, dbMiss)
		for i, repo := range repos {
			d, ok := fromDB[repo]
			if !ok || ordered[i] != nil {
				continue
			}
			if time.Since(fetched[repo]) < cacheTTL {
				ordered[i] = d
				// 回灌 Redis，下次请求走内存缓存
				if cache.Client != nil {
					if b, err := json.Marshal(d); err == nil {
						if err := cache.Client.Set(ctx, "starhistory:data:"+repo, string(b), cacheTTL).Err(); err != nil {
							log.Warn("rehydrate redis cache failed", "repo", repo, "error", err)
						}
					}
				}
			} else {
				prev[repo] = d
			}
		}
	}

	uncached := make([]string, 0, len(repos))
	for i, repo := range repos {
		if ordered[i] == nil {
			uncached = append(uncached, repo)
		}
	}

	missing := []string{}
	if len(uncached) > 0 {
		token := config.Cfg.Secrets.GitHubToken
		repoIndex := make(map[string]int, len(repos))
		for i, r := range repos {
			repoIndex[r] = i
		}

		var (
			mu       sync.Mutex
			wg       sync.WaitGroup
			fresh    []*repoStarData // 本次回源成功的仓库
			fetchErr error
		)
		for _, repo := range uncached {
			wg.Add(1)
			go func(repo string) {
				defer wg.Done()
				data, miss, err := fetchRepoStarData(ctx, token, repo, prev[repo])
				if err != nil {
					mu.Lock()
					if fetchErr == nil {
						fetchErr = err
					}
					mu.Unlock()
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if miss {
					missing = append(missing, repo)
					return
				}
				ordered[repoIndex[repo]] = data
				fresh = append(fresh, data)
			}(repo)
		}
		wg.Wait()
		if fetchErr != nil {
			return nil, nil, fetchErr
		}
		sort.Strings(missing) // 稳定输出

		// 写缓存（仅本次回源成功的仓库；命中缓存的条目不刷新 TTL，
		// 保证满 24h 后强制回源拿到新数据）
		for _, d := range fresh {
			if cache.Client != nil {
				if b, err := json.Marshal(d); err == nil {
					if err := cache.Client.Set(ctx, "starhistory:data:"+d.Repo, string(b), cacheTTL).Err(); err != nil {
						log.Warn("set star data cache failed", "repo", d.Repo, "error", err)
					}
				}
			}
			dbSaveStarData(ctx, d)
		}
	}

	// 概率式清理过期行（见 store.go；本请求保存/复用的行已刷新 fetched_at，不会误删）
	dbPurgeExpired(ctx)

	out := make([]*repoStarData, 0, len(repos))
	for _, d := range ordered {
		if d != nil {
			out = append(out, d)
		}
	}
	return out, missing, nil
}

// dataToChartDataset 将星标记录转为图表数据：
// Timeline 模式 x 为相对首日的毫秒数，Date 模式 x 为 UTC 零点毫秒值
// （与 JS new Date("YYYY-MM-DD") 的语义一致）。
func dataToChartDataset(data *repoStarData, xTickLabelType string) chartDataset {
	var firstDate float64
	if xTickLabelType == "Number" && len(data.StarRecords) > 0 {
		firstDate = parseDateMs(data.StarRecords[0].Date)
	}
	points := make([]xyPoint, 0, len(data.StarRecords))
	for _, r := range data.StarRecords {
		x := parseDateMs(r.Date)
		if xTickLabelType == "Number" {
			x -= firstDate
		}
		points = append(points, xyPoint{x: x, y: float64(r.Count)})
	}
	return chartDataset{label: data.Repo, logo: data.LogoB64, data: points}
}
