package hitcount

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	defaultLabel = "hits"
	defaultColor = "4c1"
	maxLabelLen  = 40
)

var colorPattern = regexp.MustCompile(`^#?([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// svgTemplate SVG 徽章模板（shields.io flat 结构，宽度按文本动态计算）：
//   - %s ①gradient 定义（flat 样式有，flat-square 为空）
//   - %s ②高光 rect（flat 样式有，flat-square 为空）
var svgTemplate = `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s">%s` +
	`<clipPath id="r"><rect width="%d" height="20" rx="%d" fill="#fff"/></clipPath>` +
	`<g clip-path="url(#r)">` +
	`<rect width="%d" height="20" fill="#555"/><rect x="%d" width="%d" height="20" fill="#%s"/>%s` +
	`</g>` +
	`<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">` +
	`<text x="%d" y="14">%s</text><text x="%d" y="14">%s</text>` +
	`</g></svg>`

const gradientDef = `<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>`

// renderSVG 渲染 SVG 徽章响应。查询参数：
//
//	style=flat|flat-square  徽章样式，默认 flat
//	color=<hex>             右侧计数区颜色（3/6 位十六进制），默认 4c1
//	label=<text>            左侧文字，默认 hits（最长 40 字符）
func renderSVG(ctx *gin.Context, count int64) {
	label := normLabel(ctx.Query("label"))
	color := normColor(ctx.Query("color"))
	style := normStyle(ctx.Query("style"))
	svg := renderBadge(label, strconv.FormatInt(count, 10), color, style)
	ctx.Data(http.StatusOK, "image/svg+xml; charset=utf-8", []byte(svg))
}

// renderBadge 渲染徽章 SVG：左侧 label（深灰底），右侧 message（彩色底）
func renderBadge(label, message, color, style string) string {
	lw := textWidth(label)
	mw := textWidth(message)
	total := lw + mw

	gradient, shine := "", ""
	rx := 0
	if style == "flat" {
		rx = 3
		gradient = gradientDef
		shine = fmt.Sprintf(`<rect width="%d" height="20" fill="url(#s)"/>`, total)
	}

	aria := escapeXML(label) + ": " + escapeXML(message)
	return fmt.Sprintf(svgTemplate,
		total, aria,
		gradient,
		total, rx,
		lw, lw, mw, color,
		shine,
		lw/2, escapeXML(label),
		lw+mw/2, escapeXML(message),
	)
}

// textWidth 估算 Verdana 11px 下文本的渲染宽度（与 shields.io 近似）
func textWidth(s string) int {
	w := int(math.Ceil(float64(len(s)) * 6.5)) + 10
	if w < 20 {
		w = 20
	}
	return w
}

// normColor 规范化颜色：接受 3/6 位十六进制（可带 #），非法回退默认色
func normColor(raw string) string {
	raw = strings.TrimSpace(raw)
	if m := colorPattern.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return defaultColor
}

// normLabel 规范化左侧文字：空则用默认值，超长截断
func normLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLabel
	}
	if len(raw) > maxLabelLen {
		raw = raw[:maxLabelLen]
	}
	return raw
}

// normStyle 规范化样式：仅支持 flat 与 flat-square，其余回退 flat
func normStyle(raw string) string {
	if raw == "flat" || raw == "flat-square" {
		return raw
	}
	return "flat"
}

// escapeXML 转义 XML 特殊字符，防止注入
func escapeXML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
