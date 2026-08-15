package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Cfg 全局配置实例
var Cfg *Config

// Config 应用顶层配置
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Database    DatabaseConfig    `yaml:"database"`
	Redis       RedisConfig       `yaml:"redis"`
	CookieCloud CookieCloudConfig `yaml:"cookiecloud"`
	Secrets     SecretsConfig     `yaml:"secrets"`
	Modules     ModulesConfig     `yaml:"modules"`
}

// ModulesConfig 各模块独立配置
type ModulesConfig struct {
	BiliInfo         BiliInfoConfig         `yaml:"biliinfo"`
	QQMailHead       QQMailHeadConfig       `yaml:"qqmailhead"`
	StarVote         StarVoteConfig         `yaml:"starvote"`
	GenLineAnimation GenLineAnimationConfig `yaml:"genlineanimation"`
	StarHistory      StarHistoryConfig      `yaml:"starhistory"`
	SiteInfo         SiteInfoConfig         `yaml:"siteinfo"`
}

// ServerConfig 服务器相关配置
type ServerConfig struct {
	Port           int      `yaml:"port"`
	Mode           string   `yaml:"mode"`
	TrustedProxies []string `yaml:"trusted_proxies"`
	AllowedOrigins []string `yaml:"allowed_origins"` // CORS 允许的来源域名
}

// DatabaseConfig 数据库相关配置
type DatabaseConfig struct {
	Driver          string        `yaml:"driver"`
	DSN             string        `yaml:"dsn"`
	MaxOpenConns    int           `yaml:"max_open_conns"`
	MaxIdleConns    int           `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	LogLevel        string        `yaml:"log_level"`
	AutoMigrate     bool          `yaml:"auto_migrate"`
}

// RedisConfig Redis 相关配置
type RedisConfig struct {
	Addr         string        `yaml:"addr"`
	Password     string        `yaml:"password"`
	DB           int           `yaml:"db"`
	PoolSize     int           `yaml:"pool_size"`
	MinIdleConns int           `yaml:"min_idle_conns"`
	DialTimeout  time.Duration `yaml:"dial_timeout"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

// SecretsConfig 跨模块共享凭据
type SecretsConfig struct {
	GitHubToken string `yaml:"github_token"`
}

// CookieCloudConfig CookieCloud 服务配置（共享基础设施，非 API 模块）
type CookieCloudConfig struct {
	Enable     *bool  `yaml:"enable"` // 未配置时默认禁用
	Host       string `yaml:"host"`
	UUID       string `yaml:"uuid"`
	Password   string `yaml:"password"`
	CryptoType string `yaml:"crypto_type"` // 留空默认 legacy
}

// Enabled 是否启用
func (c *CookieCloudConfig) Enabled() bool { return c.Enable != nil && *c.Enable }

// BiliInfoConfig Bilibili 信息模块配置
type BiliInfoConfig struct {
	Enable          *bool    `yaml:"enable"` // 未配置时默认禁用
	AllowedReferers []string `yaml:"allowed_referers"`
}

// Enabled 模块是否启用
func (c *BiliInfoConfig) Enabled() bool { return c.Enable != nil && *c.Enable }

// QQMailHeadConfig QQ 邮箱头像模块配置
type QQMailHeadConfig struct {
	Enable *bool `yaml:"enable"` // 未配置时默认禁用
}

// Enabled 模块是否启用
func (c *QQMailHeadConfig) Enabled() bool { return c.Enable != nil && *c.Enable }

// StarVoteConfig 投票/评分模块配置
type StarVoteConfig struct {
	Enable          *bool    `yaml:"enable"` // 未配置时默认禁用
	AllowedReferers []string `yaml:"allowed_referers"`
}

// Enabled 模块是否启用
func (c *StarVoteConfig) Enabled() bool { return c.Enable != nil && *c.Enable }

// GenLineAnimationConfig 手写签名动画模块配置
type GenLineAnimationConfig struct {
	Enable          *bool    `yaml:"enable"` // 未配置时默认禁用
	AllowedReferers []string `yaml:"allowed_referers"`
}

// Enabled 模块是否启用
func (c *GenLineAnimationConfig) Enabled() bool { return c.Enable != nil && *c.Enable }

// StarHistoryConfig 星标历史图表模块配置
// 说明：SVG 图表通常内嵌于 README 等场景（无 Referer），故不做来源校验；
// 数据来自 GitHub stargazers API（2026-06-30 起仅仓库管理员/协作者可访问，
// 需 secrets.github_token），经 Redis 缓存（24h）。
type StarHistoryConfig struct {
	Enable *bool `yaml:"enable"` // 未配置时默认禁用
}

// Enabled 模块是否启用
func (c *StarHistoryConfig) Enabled() bool { return c.Enable != nil && *c.Enable }

// SiteInfoConfig 站点信息模块配置
type SiteInfoConfig struct {
	Enable          *bool    `yaml:"enable"` // 未配置时默认禁用
	AllowedReferers []string `yaml:"allowed_referers"`
}

// Enabled 模块是否启用
func (c *SiteInfoConfig) Enabled() bool { return c.Enable != nil && *c.Enable }

// Load 加载配置文件，默认查找执行目录下的 config.yaml
func Load(path ...string) (*Config, error) {
	configPath := "config.yaml"
	if len(path) > 0 && path[0] != "" {
		configPath = path[0]
	}

	// 如果传入的是相对路径，尝试基于执行目录解析
	if !filepath.IsAbs(configPath) {
		execDir, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
		configPath = filepath.Join(execDir, configPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", configPath, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	// 默认值
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "release"
	}

	Cfg = &cfg
	return &cfg, nil
}
