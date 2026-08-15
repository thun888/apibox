package starhistory

// 星标数据抓取：直接调用 GitHub stargazers API，并支持基于过期缓存的
// 增量复用。
//
// 2026-06-30 起 GitHub 将该 API 限制为仓库管理员/协作者可见（无权访问的
// 仓库返回 404），因此需要 secrets.github_token，且只能抓取本人拥有/协作
// 的仓库。
//
// 全量抓取按 starred_at 日期聚合为累计星标记录（与原项目流水线一致），
// 记录数少于 minDataPoints 时视为“不在数据集”。
//
// REST 端点不支持按时间过滤，增量复用靠“升序稳定 + 元数据总数校验”：
//   - 元数据 stargazers_count 与上次一致：只翻最后一页确认无新日期即沿用
//     旧记录（2 次 API 调用）；
//   - 总数增加：只翻从上次总数位置起的尾部页，把晚于最后记录日期的新条目
//     并入旧记录（同天新增、unstar 位移等使合并结果与总数不符时回退全量）；
//   - 总数减少：unstar 导致曲线需重算，直接全量。
//
// 复用模式下曲线历史不重算（unstar 造成的形状修正只在全量时体现），
// 但最终 count 始终以 stargazers_count 校验为准。
//
// 头像抓取仓库 owner 的 avatar_url 并转 base64 data URL 内联。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

const (
	stargazersPerPage = 100
	maxStarPages      = 1000 // 单仓库最多翻 1000 页（10 万条），防失控
	minDataPoints     = 5    // 与原项目 MIN_DATAPOINTS 一致
	githubAPIBase     = "https://api.github.com"
)

// githubAPIError GitHub API 非 200 响应
type githubAPIError struct{ Status int }

func (e *githubAPIError) Error() string { return fmt.Sprintf("github api status %d", e.Status) }

// isGithubNotFound 是否为 404（仓库不存在或无权访问）
func isGithubNotFound(err error) bool {
	var apiErr *githubAPIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

type starRecord struct {
	Date  string `json:"date"`  // YYYY-MM-DD（UTC）
	Count int    `json:"count"` // 截止当天的累计星标数
}

type repoStarData struct {
	Repo        string       `json:"repo"`
	StarRecords []starRecord `json:"star_records"`
	LogoB64     string       `json:"logo_b64"`
}

// repoMeta 仓库元数据（增量判断用）
type repoMeta struct {
	AvatarURL string
	StarCount int
}

// fetchRepoStarData 抓取单个仓库的星标数据与头像。
// prev 为上次抓取的记录（可来自过期数据库缓存），用于增量复用。
// miss=true 表示仓库“不在数据集”（不存在、无权访问或星标记录过少）。
func fetchRepoStarData(ctx context.Context, token, repo string, prev *repoStarData) (data *repoStarData, miss bool, err error) {
	meta, err := fetchRepoMeta(ctx, token, repo)
	if err != nil {
		if isGithubNotFound(err) {
			return nil, true, nil
		}
		return nil, false, err
	}

	records, err := fetchRecords(ctx, token, repo, prev, meta)
	if err != nil {
		// stargazers 端点 404：无权访问（2026-06-30 起仅管理员/协作者可见）
		if isGithubNotFound(err) {
			return nil, true, nil
		}
		return nil, false, err
	}
	if len(records) < minDataPoints {
		return nil, true, nil
	}

	logo := ""
	if meta.AvatarURL != "" {
		logo = fetchBase64Image(ctx, meta.AvatarURL+"&s=22")
	}
	return &repoStarData{Repo: repo, StarRecords: records, LogoB64: logo}, false, nil
}

// fetchRepoMeta GET /repos/{o}/{r}：owner 头像与当前星标总数（公共端点，
// 不受 stargazers 限制影响）。
func fetchRepoMeta(ctx context.Context, token, repo string) (*repoMeta, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBase+"/repos/"+repo, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "apibox")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &githubAPIError{Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var meta struct {
		StargazersCount int `json:"stargazers_count"`
		Owner           struct {
			AvatarURL string `json:"avatar_url"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}
	return &repoMeta{AvatarURL: meta.Owner.AvatarURL, StarCount: meta.StargazersCount}, nil
}

// fetchRecords 按 prev 与元数据决定全量或增量抓取。
func fetchRecords(ctx context.Context, token, repo string, prev *repoStarData, meta *repoMeta) ([]starRecord, error) {
	// 星标总数不足 minDataPoints：结果必为 missing，无需抓取
	if meta.StarCount < minDataPoints {
		return nil, nil
	}
	if prev == nil || len(prev.StarRecords) == 0 {
		return fetchFullRecords(ctx, token, repo)
	}
	return fetchIncrementalRecords(ctx, token, repo, prev, meta)
}

// fetchFullRecords 全量翻页抓取并聚合。
func fetchFullRecords(ctx context.Context, token, repo string) ([]starRecord, error) {
	dates, err := fetchStargazerDates(ctx, token, repo, 1, 0)
	if err != nil {
		return nil, err
	}
	return aggregateDates(dates), nil
}

// fetchIncrementalRecords 尝试基于 prev 增量复用：
//   - 总数持平：翻最后一页确认无新日期 → 沿用旧记录；发现新日期（说明
//     同时存在 unstar）→ 全量；
//   - 总数减少：全量（曲线需按当前 stargazers 重算）；
//   - 总数增加：只翻新增尾页合并；合并结果与总数不符 → 全量。
func fetchIncrementalRecords(ctx context.Context, token, repo string, prev *repoStarData, meta *repoMeta) ([]starRecord, error) {
	old := prev.StarRecords
	last := old[len(old)-1]
	lastDate, prevCount := last.Date, last.Count
	starCount := meta.StarCount

	if starCount == prevCount {
		lastPage := (starCount + stargazersPerPage - 1) / stargazersPerPage
		tail, err := fetchStargazerDates(ctx, token, repo, lastPage, lastPage)
		if err != nil {
			return nil, err
		}
		for _, d := range tail {
			if d > lastDate {
				return fetchFullRecords(ctx, token, repo)
			}
		}
		return old, nil // 无新星标：复用旧记录
	}

	if starCount < prevCount {
		return fetchFullRecords(ctx, token, repo)
	}

	startPage := (prevCount + stargazersPerPage - 1) / stargazersPerPage
	endPage := (starCount + stargazersPerPage - 1) / stargazersPerPage
	tail, err := fetchStargazerDates(ctx, token, repo, startPage, endPage)
	if err != nil {
		return nil, err
	}
	merged := mergeTailRecords(old, tail, prevCount)
	if len(merged) == 0 || merged[len(merged)-1].Count != starCount {
		// 合并结果与总数不符（同天新增、unstar 位移等）→ 全量兜底
		return fetchFullRecords(ctx, token, repo)
	}
	return merged, nil
}

// mergeTailRecords 将尾部页的日期并入旧记录：仅接受严格晚于最后记录日期
// 的条目，在其后按 prevCount 继续累计。无新条目时返回 nil。
func mergeTailRecords(old []starRecord, tail []string, prevCount int) []starRecord {
	if len(old) == 0 {
		return nil
	}
	lastDate := old[len(old)-1].Date
	newDates := make([]string, 0, len(tail))
	for _, d := range tail {
		if d > lastDate {
			newDates = append(newDates, d)
		}
	}
	if len(newDates) == 0 {
		return nil
	}
	merged := make([]starRecord, 0, len(old)+len(newDates))
	merged = append(merged, old...)
	for _, r := range aggregateDates(newDates) {
		merged = append(merged, starRecord{Date: r.Date, Count: r.Count + prevCount})
	}
	return merged
}

// fetchStargazerDates 抓取 [startPage, endPage] 页的 starred_at 日期列表
// （升序）；endPage 传 0 表示翻到最后（全量）。
func fetchStargazerDates(ctx context.Context, token, repo string, startPage, endPage int) ([]string, error) {
	if startPage < 1 {
		startPage = 1
	}
	dates := make([]string, 0, stargazersPerPage)
	client := &http.Client{Timeout: 30 * time.Second}

	for page := startPage; ; page++ {
		if endPage > 0 && page > endPage {
			break
		}
		if page > maxStarPages {
			return nil, errors.New("too many stargazers")
		}
		endpoint := fmt.Sprintf("%s/repos/%s/stargazers?per_page=%d&page=%d",
			githubAPIBase, repo, stargazersPerPage, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github.star+json")
		req.Header.Set("User-Agent", "apibox")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github stargazers request: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, &githubAPIError{Status: resp.StatusCode}
		}
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, errors.New("github api rate limit exceeded")
		}

		var items []struct {
			StarredAt string `json:"starred_at"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("decode stargazers: %w", err)
		}
		for _, it := range items {
			if len(it.StarredAt) >= 10 {
				dates = append(dates, it.StarredAt[:10])
			}
		}
		if len(items) < stargazersPerPage {
			break // 最后一页
		}
	}

	return dates, nil
}

// aggregateDates 按日期聚合并累计为记录。
func aggregateDates(dates []string) []starRecord {
	if len(dates) == 0 {
		return nil
	}
	byDate := make(map[string]int, len(dates))
	for _, d := range dates {
		byDate[d]++
	}
	keys := make([]string, 0, len(byDate))
	for d := range byDate {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	records := make([]starRecord, 0, len(keys))
	cumulative := 0
	for _, d := range keys {
		cumulative += byDate[d]
		records = append(records, starRecord{Date: d, Count: cumulative})
	}
	return records
}

// fetchBase64Image 抓取图片并转 base64 data URL（与原项目 getBase64Image
// 一致：10s 超时、失败返回空串、无 content-type 时回退 image/png）。
func fetchBase64Image(ctx context.Context, imageURL string) string {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "apibox")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/png"
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(body)
}
