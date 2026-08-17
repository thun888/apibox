package genlineanimation

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"

	"github.com/thun888/apibox/internal/api"
	"github.com/thun888/apibox/internal/config"
	"github.com/thun888/apibox/internal/utils"
)

var (
	moduleName = "gen_line_animation"
	// logger     = utils.NewModuleLogger(moduleName)
)

// Controller GenLineAnimation 手写签名动画模块控制器
type Controller struct{}

func init() {
	api.RegisterController(&Controller{})
}

// Register 注册路由
func (c *Controller) Register(r *gin.RouterGroup) {
	r.GET("/signature", c.signature)
}

func (c *Controller) ModuleName() string { return moduleName }

func (c *Controller) Enabled() bool { return config.Cfg.Modules.GenLineAnimation.Enabled() }

// ---------------------------------------------------------------------------
// GET  /api/genlineanimation/signature
//
//	参数: name (string), animate (bool), speed (float), color (string)
//
// ---------------------------------------------------------------------------
func (c *Controller) signature(ctx *gin.Context) {
	if !utils.CheckReferer(config.Cfg.Modules.GenLineAnimation.AllowedReferers, ctx) {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	name := ctx.DefaultQuery("name", "Signature")
	animate := ctx.Query("animate") == "true"

	speed := 1.0
	if s, err := strconv.ParseFloat(ctx.Query("speed"), 64); err == nil && s > 0 {
		speed = s
	}

	strokeColor := ctx.DefaultQuery("color", "#000000")

	svg := generateSignatureSVG(name, animate, speed, strokeColor)

	ctx.Header("Cache-Control", "public, max-age=31536000")
	ctx.Data(http.StatusOK, "image/svg+xml", []byte(svg))
}

// generateSignatureSVG 生成签名 SVG
func generateSignatureSVG(name string, animate bool, animationSpeed float64, strokeColor string) string {
	const (
		letterHeight = 51
		fillColor    = "none"
		lineCap      = "round"
		lineJoin     = "round"
	)

	var sb strings.Builder
	totalWidth := 0

	// 写入 SVG 头部（viewBox 宽度会在最后替换）
	sb.WriteString(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="100%%" height="100%%" viewBox="0 0 0 %d">`, letterHeight,
	))
	sb.WriteString(`
		<style>
			@keyframes draw {
				to { stroke-dashoffset: 0; }
			}
		</style>
		<foreignObject width="100%" height="100%">
		<div xmlns="http://www.w3.org/1999/xhtml" style="display: flex; align-items: center; height: 100%; justify-content: center;">`)

	var totalAnimationDuration float64
	animationSpeedPerUnit := 0.01 / animationSpeed

	for _, ch := range name {
		if ch == ' ' {
			totalWidth += 7
			sb.WriteString(`<div style="width: 7px;"></div>`)
			continue
		}

		lower := string(unicode.ToLower(ch))
		isUpper := unicode.IsUpper(ch)

		caseKey := "lowercase"
		if isUpper {
			caseKey = "uppercase"
		}

		letterData := svgData[caseKey][lower]
		if letterData == "" {
			continue
		}

		letterWidth := letterWidths[caseKey][lower]
		letterMargin := letterMargins[caseKey][lower]
		pathLength := lineWidths[caseKey][lower]

		totalWidth += letterWidth

		// 替换 <svg 标签，设置内联样式
		letterSvg := strings.Replace(letterData, "<svg",
			fmt.Sprintf(`<svg style="display: inline-block; width: %dpx; height: 100%%;" `, letterWidth), 1)

		// 替换 <path 标签，添加描边属性
		letterSvg = strings.Replace(letterSvg, "<path",
			fmt.Sprintf(`<path stroke="%s" fill="%s" stroke-linecap="%s" stroke-linejoin="%s"`,
				strokeColor, fillColor, lineCap, lineJoin), 1)

		if animate {
			animationDuration := float64(pathLength) * animationSpeedPerUnit
			animationDelay := totalAnimationDuration
			totalAnimationDuration += animationDuration

			// 在 <path 标签前插入动画属性（保留上一步的描边属性）
			letterSvg = strings.Replace(letterSvg, "<path",
				fmt.Sprintf(`<path stroke-dasharray="%d" stroke-dashoffset="%d" style="animation: draw %fs %fs forwards;"`,
					pathLength, pathLength, animationDuration, animationDelay), 1)
		}

		sb.WriteString(fmt.Sprintf(
			`<div style="display: inline-flex; align-items: center; margin: %s;">%s</div>`,
			letterMargin, letterSvg,
		))
	}

	sb.WriteString(`</div></foreignObject></svg>`)

	result := sb.String()
	result = strings.Replace(result, `viewBox="0 0 0`, fmt.Sprintf(`viewBox="0 0 %d`, totalWidth), 1)

	return result
}
