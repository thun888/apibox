package utils

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thun888/apibox/internal/cache"
	"github.com/thun888/apibox/internal/config"
)

// CookieItem 单个 Cookie 条目
type CookieItem struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Expires  int64  `json:"expires,omitempty"`
	HTTPOnly bool   `json:"httpOnly"`
	Secure   bool   `json:"secure"`
	SameSite string `json:"sameSite,omitempty"`
}

// DecryptResult cookie_decrypt 的返回值结构
type DecryptResult struct {
	CookieData map[string][]CookieItem `json:"cookie_data"`
}

// CookieCloud 从远端 CookieCloud 服务获取并解密 Cookie
// host:      服务地址，如 "https://cookie.example.com"
// uuid:      用户标识
// password:  解密密码
// cryptoType: 加密算法，空字符串或 "legacy" 为旧算法，"aes-128-cbc-fixed" 为新算法
func CookieCloud(ctx context.Context, host, uuid, password, cryptoType string) ([]CookieItem, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("parse host: %w", err)
	}
	u = u.JoinPath("get", uuid)

	if cryptoType != "" && cryptoType != "legacy" {
		q := u.Query()
		q.Set("crypto_type", cryptoType)
		u.RawQuery = q.Encode()
	}

	reqURL := u.String()

	// 尝试从缓存读取
	if cache.Client != nil {
		cached, err := cache.Client.Get(ctx, reqURL).Result()
		if err == nil && cached != "" {
			var cookies []CookieItem
			if err := json.Unmarshal([]byte(cached), &cookies); err == nil {
				return cookies, nil
			}
		}
	}

	// 请求远端服务
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch cookies: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var envelope struct {
		Encrypted  string `json:"encrypted"`
		CryptoType string `json:"crypto_type"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if envelope.Encrypted == "" {
		return nil, fmt.Errorf("no encrypted data in response")
	}

	// 解密
	result, err := cookieDecrypt(uuid, envelope.Encrypted, password,
		orDefault(cryptoType, envelope.CryptoType, "legacy"))
	if err != nil {
		return nil, fmt.Errorf("decrypt cookies: %w", err)
	}

	// 展平 cookie_data 中所有站点的 cookie
	var cookies []CookieItem
	for _, items := range result.CookieData {
		for _, item := range items {
			if item.SameSite == "unspecified" {
				item.SameSite = "Lax"
			}
			cookies = append(cookies, item)
		}
	}

	// 写入缓存（1 小时）
	if cache.Client != nil {
		data, _ := json.Marshal(cookies)
		cache.Client.Set(ctx, reqURL, string(data), 1*time.Hour)
	}

	return cookies, nil
}

// GetCookies 从配置读取参数，一键获取 Cookie（零参数便捷方法）
func GetCookies(ctx context.Context) ([]CookieItem, error) {
	cfg := config.Cfg.Modules.CookieCloud
	return CookieCloud(ctx, cfg.Host, cfg.UUID, cfg.Password, cfg.CryptoType)
}

// FilterCookiesByDomain 筛选指定域名的 Cookie
func FilterCookiesByDomain(cookies []CookieItem, domain string) []CookieItem {
	var result []CookieItem
	for _, c := range cookies {
		if c.Domain == domain {
			result = append(result, c)
		}
	}
	return result
}

// BuildCookieHeader 将 Cookie 列表拼接为 Cookie 请求头值
func BuildCookieHeader(cookies []CookieItem) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// cookieDecrypt 解密 Cookie 数据
func cookieDecrypt(uuid, encrypted, password, cryptoType string) (*DecryptResult, error) {
	hash := md5.Sum([]byte(uuid + "-" + password))
	theKey := hex.EncodeToString(hash[:])[:16] // 取前 16 个字符

	var decrypted []byte
	var err error

	switch cryptoType {
	case "aes-128-cbc-fixed":
		decrypted, err = decryptAES128CBCFixed(encrypted, []byte(theKey))
	default: // legacy
		decrypted, err = decryptLegacy(encrypted, []byte(theKey))
	}
	if err != nil {
		return nil, err
	}

	var result DecryptResult
	if err := json.Unmarshal(decrypted, &result); err != nil {
		return nil, fmt.Errorf("parse decrypted data: %w", err)
	}
	return &result, nil
}

// decryptAES128CBCFixed AES-128-CBC 解密，使用固定零 IV
func decryptAES128CBCFixed(encrypted string, key []byte) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid ciphertext length: %d", len(ciphertext))
	}

	iv := make([]byte, aes.BlockSize) // 全零 IV
	mode := cipher.NewCBCDecrypter(block, iv)

	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	return pkcs7Unpad(plaintext)
}

// decryptLegacy 解密 CryptoJS legacy 格式（OpenSSL 兼容）
func decryptLegacy(encrypted string, password []byte) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	// OpenSSL 格式: "Salted__"(8) + salt(8) + ciphertext
	const saltPrefix = "Salted__"
	if len(data) < 16 || string(data[:8]) != saltPrefix {
		return nil, fmt.Errorf("invalid legacy format: missing Salted__ prefix")
	}

	salt := data[8:16]
	ciphertext := data[16:]

	// EvpKDF 派生 key 和 IV（AES-256-CBC）
	key, iv := evpKDF(password, salt, 32, 16)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	return pkcs7Unpad(plaintext)
}

// evpKDF OpenSSL EVP_BytesToKey 兼容实现（MD5, 1 次迭代）
func evpKDF(password, salt []byte, keyLen, ivLen int) ([]byte, []byte) {
	derived := make([]byte, 0, keyLen+ivLen)
	var block []byte

	for len(derived) < keyLen+ivLen {
		h := md5.New()
		if block != nil {
			h.Write(block)
		}
		h.Write(password)
		h.Write(salt)
		block = h.Sum(nil)
		derived = append(derived, block...)
	}

	return derived[:keyLen], derived[keyLen : keyLen+ivLen]
}

// pkcs7Unpad 去除 PKCS7 填充
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	paddingLen := int(data[len(data)-1])
	if paddingLen == 0 || paddingLen > len(data) || paddingLen > aes.BlockSize {
		return nil, fmt.Errorf("invalid padding length: %d", paddingLen)
	}
	return data[:len(data)-paddingLen], nil
}

// orDefault 返回第一个非空值，全空则返回 "legacy"
func orDefault(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "legacy"
}
