package starhistory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	historyWeeksPerPage = 30
	maxHistoryPages     = 100 // history API 翻页上限（30 周/页，约 57 年）
	minDataPoints       = 5
	githubAPIBase       = "https://api.github.com"
)

type githubAPIError struct{ Status int }

func (e *githubAPIError) Error() string { return fmt.Sprintf("github api status %d", e.Status) }

// isGithubNotFound 404：仓库不存在或无权访问（私有仓库）
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

// starWeek stargazers/history 周记录，days 从周日开始、空天补 0
type starWeek struct {
	Week  int64 `json:"week"`
	Total int   `json:"total"`
	Days  []int `json:"days"`
}

type repoMeta struct {
	AvatarURL string
}

func fetchRepoStarData(ctx context.Context, token, repo string) (data *repoStarData, miss bool, err error) {
	meta, err := fetchRepoMeta(ctx, token, repo)
	if err != nil {
		if isGithubNotFound(err) {
			return nil, true, nil
		}
		return nil, false, err
	}

	count, err := fetchStarCount(ctx, token, repo)
	if err != nil {
		if isGithubNotFound(err) {
			return nil, true, nil
		}
		log.Warn("fetch star count failed", "repo", repo, "error", err)
		count = 0
	} else if count < minDataPoints {
		return nil, true, nil
	}

	records, err := fetchHistoryRecords(ctx, token, repo)
	if err != nil {
		if isGithubNotFound(err) {
			return nil, true, nil
		}
		return nil, false, err
	}
	if len(records) < minDataPoints {
		return nil, true, nil
	}

	if count > 0 {
		if last := records[len(records)-1]; last.Count != count {
			// unstar 等场景两端口径可能不一致，仅记录不修正
			log.Warn("star history total mismatch", "repo", repo, "history", last.Count, "count", count)
		}
	}

	logo := ""
	if meta.AvatarURL != "" {
		logo = fetchBase64Image(ctx, meta.AvatarURL+"&s=22")
	}
	return &repoStarData{Repo: repo, StarRecords: records, LogoB64: logo}, false, nil
}

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
		Owner struct {
			AvatarURL string `json:"avatar_url"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}
	return &repoMeta{AvatarURL: meta.Owner.AvatarURL}, nil
}

func fetchStarCount(ctx context.Context, token, repo string) (int, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBase+"/repos/"+repo+"/stargazers/count", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "apibox")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, &githubAPIError{Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if err != nil {
		return 0, err
	}
	var c struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(body, &c); err != nil {
		return 0, err
	}
	return c.Count, nil
}

func fetchHistoryRecords(ctx context.Context, token, repo string) ([]starRecord, error) {
	weeks, err := fetchAllWeeks(ctx, token, repo)
	if err != nil {
		return nil, err
	}
	return expandWeeks(weeks), nil
}

func fetchAllWeeks(ctx context.Context, token, repo string) ([]starWeek, error) {
	var weeks []starWeek
	client := &http.Client{Timeout: 30 * time.Second}
	for page := 1; ; page++ {
		if page > maxHistoryPages {
			return nil, errors.New("too many history pages")
		}
		endpoint := fmt.Sprintf("%s/repos/%s/stargazers/history?per_page=%d&page=%d",
			githubAPIBase, repo, historyWeeksPerPage, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "apibox")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github history request: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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
		var items []starWeek
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("decode history: %w", err)
		}
		weeks = append(weeks, items...)
		if len(items) < historyWeeksPerPage {
			break
		}
	}
	// API 按最新周在前返回，反转为时间升序
	for i, j := 0, len(weeks)-1; i < j; i, j = i+1, j-1 {
		weeks[i], weeks[j] = weeks[j], weeks[i]
	}
	return weeks, nil
}

// expandWeeks 周记录（升序）展开为按日累计星标记录，零星标天跳过
func expandWeeks(weeks []starWeek) []starRecord {
	var records []starRecord
	cumulative := 0
	for _, w := range weeks {
		start := time.Unix(w.Week, 0).UTC()
		for i, n := range w.Days {
			if n <= 0 {
				continue
			}
			cumulative += n
			records = append(records, starRecord{
				Date:  start.AddDate(0, 0, i).Format("2006-01-02"),
				Count: cumulative,
			})
		}
	}
	return records
}

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
