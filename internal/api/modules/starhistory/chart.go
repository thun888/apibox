package starhistory

// SVG 渲染器：在 Go 中复刻 star-history 前端 XYChart + drawAxis + drawLegend
// + drawWatermark 的输出（坐标、属性与样式已按 svgo 优化后的特征直接内联：
// 无缩进、无冗余属性）。JSDOM 初始 DOM 里的残留（隐藏 ToolTip、图例周围的
// 空嵌套 <svg>、pointer-events 包装）不产生可见输出，已省略；d3-shape 中
// 依赖重复 x 坐标的 JS 边界处理同样省略（星标日期唯一且升序，x 严格递增）。

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"
)

const (
	marginTop    = 50
	marginRight  = 30
	marginBottom = 50
	marginLeft   = 50

	xkcdCharWidth  = 7.0
	xkcdCharHeight = 20.0
)

var lightColors = [20]string{
	"#dd4528", "#28a3dd", "#f3db52", "#ed84b5", "#4ab74e",
	"#9179c0", "#8e6d5a", "#f19839", "#949494", "#1a9988",
	"#c75dab", "#6a8e2f", "#d4583b", "#3767b0", "#e8a735",
	"#7c4dff", "#00897b", "#c2185b", "#5c6bc0", "#e67e22",
}

var darkColors = [20]string{
	"#ff6b6b", "#48dbfb", "#feca57", "#ff9ff3", "#1dd1a1",
	"#f368e0", "#ff9f43", "#a4b0be", "#576574", "#00d2d3",
	"#f78fb3", "#badc58", "#ff7979", "#7ed6df", "#f9ca24",
	"#b388ff", "#4dd0e1", "#ff80ab", "#9fa8da", "#f5b041",
}

type xyPoint struct {
	x float64
	y float64
}

type chartDataset struct {
	label string
	logo  string // base64 data URL，可为空
	data  []xyPoint
}

type renderOptions struct {
	title          string
	xLabel         string // "Date" | "Timeline"
	yLabel         string
	datasets       []chartDataset
	theme          string // "light" | "dark"
	transparent    bool
	xTickLabelType string // "Date" | "Number"
	chartWidth     int
	useLogScale    bool
	legendPosition string // "top-left" | "bottom-right"
}

// parseDateMs 与 JS new Date("YYYY-MM-DD") 一致：UTC 零点
func parseDateMs(date string) float64 {
	t, err := time.ParseInLocation("2006-01-02", date, time.UTC)
	if err != nil {
		return 0
	}
	return float64(t.UnixMilli())
}

type linearScale struct {
	d0, d1, r0, r1 float64
}

// apply 复刻 d3-scale v3 continuous 的运算顺序，保证浮点结果与 JS 完全一致：
//  1. normalize: b = d1-d0（rescale 时算一次）；t = (x-d0)/b
//  2. interpolate（d3-interpolate@2.x）: r = r0*(1-t) + r1*t
//     （d3-scale@3.3.0 的依赖范围 "1.2.0 - 2" 默认解析到 v2 的公式）
func (s linearScale) apply(v float64) float64 {
	t := (v - s.d0) / (s.d1 - s.d0)
	return s.r0*(1-t) + s.r1*t
}

func renderChart(o renderOptions) string {
	stroke := "black"
	bg := "white"
	palette := lightColors
	if o.theme == "dark" {
		stroke = "white"
		// jsdom CSSOM 会把 #0d1117 序列化为 rgb(13, 17, 23)；svgo 再转回 #0d1117，
		// 这里直接输出 svgo 优化后的等价形式
		bg = "#0d1117"
		palette = darkColors
	}
	if o.transparent {
		bg = "transparent"
	}

	mt, mb, ml := float64(marginTop), float64(marginBottom), float64(marginLeft)
	if o.title != "" {
		mt = 60
	}
	if o.yLabel != "" {
		ml = 70
	}

	clientWidth := float64(o.chartWidth)
	clientHeight := (clientWidth * 2) / 3
	chartWidth := clientWidth - ml - marginRight
	chartHeight := clientHeight - mt - mb

	// 汇总所有数据点
	var minX, maxX, maxY float64
	first := true
	for _, ds := range o.datasets {
		for _, p := range ds.data {
			if first {
				minX, maxX = p.x, p.x
				maxY = p.y
				first = false
			} else {
				if p.x < minX {
					minX = p.x
				}
				if p.x > maxX {
					maxX = p.x
				}
				if p.y > maxY {
					maxY = p.y
				}
			}
		}
	}

	var xScale linearScale
	if o.xTickLabelType == "Date" {
		xScale = linearScale{minX, maxX, 0, chartWidth}
	} else {
		xScale = linearScale{0, maxX, 0, chartWidth}
	}

	var yScale func(float64) float64
	if o.useLogScale {
		// d3 scaleSymlog: symlog(v) = sign(v) * log1p(|v|/c)，c=10；
		// 再经 continuous 的 normalize + interpolate（同 linearScale.apply）
		c := 10.0
		tMax := math.Log1p(maxY / c) // t1 - t0，t0 = symlog(0) = 0
		yScale = func(v float64) float64 {
			t := math.Log1p(v/c) / tMax
			return chartHeight*(1-t) + 0*t
		}
	} else {
		ls := linearScale{0, maxY, chartHeight, 0}
		yScale = ls.apply
	}

	var b strings.Builder

	// 根节点：width/xmlns 由后端 setAttribute 先设置，随后 XYChart 再补
	// style（CSSOM 按 d3 .style() 调用顺序序列化；stroke-width 传入数字，
	// 序列化为无单位的 "3"）、height、preserveAspectRatio
	fmt.Fprintf(&b, `<svg width="%s" xmlns="http://www.w3.org/2000/svg" style="stroke-width: 3; font-family: xkcd; background: %s;" height="%s" preserveAspectRatio="xMidYMid meet">`,
		jsNum(clientWidth), bg, jsNum(clientHeight))

	// @font-face（addFont 先于 addFilter 调用）
	fmt.Fprintf(&b, `<defs><style type="text/css">@font-face {
      font-family: "xkcd";
      src: url(%s) format('woff');
    }</style></defs>`, xkcdFontDataURL)

	// xkcdify 滤镜
	b.WriteString(`<filter id="xkcdify" filterUnits="userSpaceOnUse" x="-5" y="-5" width="100%" height="100%"><feTurbulence type="fractalNoise" baseFrequency="0.05" result="noise"></feTurbulence><feDisplacementMap scale="5" xChannelSelector="R" yChannelSelector="G" in="SourceGraphic" in2="noise"></feDisplacementMap></filter>`)

	// 图表主 group
	fmt.Fprintf(&b, `<g transform="translate(%s,%s)">`, jsNum(ml), jsNum(mt))
	b.WriteString(`<g>`)

	// ---------- 水印 ----------
	fmt.Fprintf(&b, `<text style="font-size: 16px; fill: #666666;" transform="translate(%s,%s)" text-anchor="middle">star-history.dera.page</text>`,
		jsNum(chartWidth-50), jsNum(chartHeight+40))
	starScale := 28.0 / 1095.0
	fmt.Fprintf(&b, `<g transform="translate(%s,%s) scale(%s)"><path d="%s" fill="#eac54f" fill-rule="evenodd" stroke="#eac54f" stroke-width="0.25" stroke-linejoin="round"></path></g>`,
		jsNum(chartWidth-162), jsNum(chartHeight+19), jsNum(starScale), watermarkStarPath)

	// ---------- X 轴 ----------
	var xTicks []float64
	var xTickLabels []string
	if o.xTickLabelType == "Date" {
		// 刻度固定按 UTC 对齐（见 d3ticks.go）
		start := time.UnixMilli(int64(math.Round(minX))).UTC()
		stop := time.UnixMilli(int64(math.Round(maxX))).UTC()
		for _, t := range timeTicks(start, stop, 5) {
			xTicks = append(xTicks, float64(t.UnixMilli()))
			xTickLabels = append(xTickLabels, d3TickFormat(t))
		}
	} else {
		xs := d3Ticks(0, maxX, 5)
		index := 1
		var unit string
		for _, v := range xs {
			index++
			if v == 0 || (len(xs) >= 7 && index%2 == 0) {
				xTickLabels = append(xTickLabels, " ")
			} else {
				if unit == "" {
					unit = getTimestampFormatUnit(v)
				}
				xTickLabels = append(xTickLabels, formatTimeline(v, unit))
			}
		}
		xTicks = xs
	}

	fmt.Fprintf(&b, `<g class="xaxis" transform="translate(0,%s)" fill="none" font-size="10" font-family="sans-serif" text-anchor="middle">`, jsNum(chartHeight))
	fmt.Fprintf(&b, `<path class="domain" stroke="currentColor" d="M%s,%sH%s" filter="url(#xkcdify)" style="stroke: %s;"></path>`,
		jsNum(0.5), jsNum(0.5), jsNum(chartWidth+0.5), stroke)
	for i, v := range xTicks {
		fmt.Fprintf(&b, `<g class="tick" opacity="1" transform="translate(%s,0)"><line stroke="currentColor" y2="0"></line><text fill="currentColor" y="6" dy="0.71em" style="font-family: xkcd; font-size: 16px; fill: %s;">%s</text></g>`,
			jsNum(xScale.apply(v)+0.5), stroke, html.EscapeString(xTickLabels[i]))
	}
	b.WriteString(`</g>`)

	// ---------- Y 轴 ----------
	var yTicks []float64
	var yTickLabels []string
	if o.useLogScale {
		yTicks, yTickLabels = logScaleTicks(maxY)
	} else {
		ys := d3Ticks(0, maxY, 5)
		var unit float64
		for _, v := range ys {
			if v == 0 {
				yTickLabels = append(yTickLabels, " ")
			} else {
				if unit == 0 {
					unit = getNumberFormatUnit(v)
				}
				yTickLabels = append(yTickLabels, formatNumber(v, unit))
			}
		}
		yTicks = ys
	}

	b.WriteString(`<g class="yaxis" fill="none" font-size="10" font-family="sans-serif" text-anchor="end">`)
	fmt.Fprintf(&b, `<path class="domain" stroke="currentColor" d="M-1,%sH0.5V%sH-1" filter="url(#xkcdify)" style="stroke: %s;"></path>`,
		jsNum(chartHeight+0.5), jsNum(0.5), stroke)
	for i, v := range yTicks {
		fmt.Fprintf(&b, `<g class="tick" opacity="1" transform="translate(0,%s)"><line stroke="currentColor" x2="-1"></line><text fill="currentColor" x="-7" dy="0.32em" style="font-family: xkcd; font-size: 16px; fill: %s;">%s</text></g>`,
			jsNum(yScale(v)+0.5), stroke, yTickLabels[i])
	}
	b.WriteString(`</g>`)

	// ---------- 折线 ----------
	for i, ds := range o.datasets {
		fmt.Fprintf(&b, `<path class="xkcd-chart-xyline" d="%s" fill="none" stroke="%s" filter="url(#xkcdify)"></path>`,
			monotonePath(ds.data, xScale, yScale), palette[i%len(palette)])
	}

	// ---------- 图例 ----------
	legendXPadding := 7.0
	legendYPadding := 6.0
	colorBlockWidth := 8.0
	logoSize := 14.0

	owners := map[string]bool{}
	for _, ds := range o.datasets {
		parts := strings.SplitN(ds.label, "/", 2)
		owners[parts[0]] = true
	}
	shouldDrawLogo := len(owners) > 1

	maxTextLength := 0
	for _, ds := range o.datasets {
		if len(ds.label) > maxTextLength {
			maxTextLength = len(ds.label)
		}
	}
	bboxWidth := float64(maxTextLength)*(xkcdCharWidth+0.5) + colorBlockWidth + legendXPadding
	backgroundWidth := math.Max(
		bboxWidth+legendXPadding*2,
		float64(maxTextLength)*xkcdCharWidth+colorBlockWidth+legendXPadding*2+6+boolFloat(shouldDrawLogo)*(legendXPadding+logoSize),
	)
	backgroundHeight := float64(len(o.datasets))*xkcdCharHeight + legendYPadding*2

	legendX := 8.0
	legendY := 5.0
	if o.legendPosition == "bottom-right" {
		legendX = chartWidth - backgroundWidth - 8
		legendY = chartHeight - backgroundHeight - 15
	}

	fmt.Fprintf(&b, `<rect style="fill: %s;" fill-opacity="0.85" stroke="%s" stroke-width="2" rx="5" ry="5" filter="url(#xkcdify)" width="%s" height="%s" x="%s" y="%s"></rect>`,
		bg, stroke, jsNum(backgroundWidth), jsNum(backgroundHeight), jsNum(legendX), jsNum(legendY))
	for i, item := range o.datasets {
		itemY := legendY + 12 + xkcdCharHeight*float64(i)
		fmt.Fprintf(&b, `<rect style="fill: %s;" width="8" height="8" rx="2" ry="2" filter="url(#xkcdify)" x="%s" y="%s"></rect>`,
			palette[i%len(palette)], jsNum(legendX+legendXPadding), jsNum(itemY))
		if shouldDrawLogo && item.logo != "" {
			fmt.Fprintf(&b, `<defs><clipPath id="clip-circle-title-%s"><circle r="7" cx="%s" cy="%s"></circle></clipPath></defs>`,
				item.label, jsNum(legendX+legendXPadding+colorBlockWidth+legendXPadding+logoSize/2), jsNum(itemY-4+logoSize/2))
			fmt.Fprintf(&b, `<image x="%s" y="%s" height="14" width="14" href="%s" clip-path="url(#clip-circle-title-%s)"></image>`,
				jsNum(legendX+legendXPadding+colorBlockWidth+legendXPadding), jsNum(itemY-4), item.logo, item.label)
		}
		fmt.Fprintf(&b, `<text style="font-size: 15px; fill: %s;" x="%s" y="%s">%s</text>`,
			stroke,
			jsNum(legendX+legendXPadding+colorBlockWidth+boolFloat(shouldDrawLogo)*(legendXPadding+logoSize)+6),
			jsNum(itemY+8), html.EscapeString(item.label))
	}

	b.WriteString(`</g>`) // svgChart
	b.WriteString(`</g>`) // chart group

	// ---------- 标题 ----------
	if o.title != "" {
		fmt.Fprintf(&b, `<text style="font-size: 20px; font-weight: bold; fill: %s;" x="50%%" y="30" text-anchor="middle">%s</text>`,
			stroke, o.title)
		fmt.Fprintf(&b, `<svg><defs><clipPath id="clip-circle-title"><circle r="11" cx="%s" cy="23"></circle></clipPath></defs></svg>`,
			jsNum(clientWidth*0.5-73))
		if len(owners) == 1 && len(o.datasets) > 0 && o.datasets[0].logo != "" {
			fmt.Fprintf(&b, `<image x="%s" y="12" height="22" width="22" href="%s" clip-path="url(#clip-circle-title)"></image>`,
				jsNum(clientWidth*0.5-84), o.datasets[0].logo)
		}
	}
	// ---------- X 标签 ----------
	if o.xLabel != "" {
		fmt.Fprintf(&b, `<text style="font-size: 17px; fill: %s;" x="50%%" y="%s" text-anchor="middle">%s</text>`,
			stroke, jsNum(clientHeight-10), o.xLabel)
	}
	// ---------- Y 标签 ----------
	if o.yLabel != "" {
		offsetY := 24.0
		switch {
		case maxY > 100000:
			offsetY = 2
		case maxY > 10000:
			offsetY = 8
		case maxY > 1000:
			offsetY = 12
		case maxY > 100:
			offsetY = 20
		}
		textLength := 100.0 // JSDOM 下无 getComputedTextLength
		offsetX := math.Floor(textLength/2 - clientHeight/2)
		fmt.Fprintf(&b, `<text text-anchor="end" dy=".75em" transform="rotate(-90)" style="font-size: 17px; fill: %s;" y="%s" x="%s">%s</text>`,
			stroke, jsNum(offsetY), jsNum(offsetX), o.yLabel)
	}

	b.WriteString(`</svg>`)
	return b.String()
}

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

// logScaleTicks 复刻 drawYAxis 中 useLogScale 分支的刻度生成
func logScaleTicks(maxValue float64) ([]float64, []string) {
	ticks := []float64{0}
	labels := []string{"0"}
	var formatUnit float64
	push := func(v float64) {
		ticks = append(ticks, v)
		if v == 0 {
			labels = append(labels, "0")
		} else {
			if formatUnit == 0 {
				formatUnit = getNumberFormatUnit(v)
			}
			labels = append(labels, formatNumber(v, formatUnit))
		}
	}

	if maxValue < 10 {
		if maxValue <= 5 {
			push(math.Ceil(maxValue))
		} else {
			push(5)
			push(math.Ceil(maxValue))
		}
		return ticks, labels
	}

	startPower := 1
	if maxValue >= 10000 {
		startPower = 2
	}
	power := startPower
	tickCount := 1
	maxTicks := 6
	for math.Pow(10, float64(power)) <= maxValue && tickCount < maxTicks {
		push(math.Pow(10, float64(power)))
		tickCount++
		power++
	}
	if tickCount < maxTicks && maxValue > ticks[len(ticks)-1] {
		lastTick := ticks[len(ticks)-1]
		if maxValue > lastTick*2 {
			push(math.Pow(10, math.Ceil(math.Log10(maxValue))))
		}
	}
	return ticks, labels
}

// ---------- d3-shape line + curveMonotoneX ----------

func monotonePath(data []xyPoint, xScale linearScale, yScale func(float64) float64) string {
	var b strings.Builder
	var x0, y0 float64
	x1, y1 := math.NaN(), math.NaN() // 初始值不会被读取（首个点走 case 0）
	var t0 float64
	point := 0
	started := false

	emitPoint := func(x, y float64) {
		var t1 float64
		switch point {
		case 0:
			point = 1
			if started {
				fmt.Fprintf(&b, "L%s,%s", jsNum(x), jsNum(y))
			} else {
				fmt.Fprintf(&b, "M%s,%s", jsNum(x), jsNum(y))
				started = true
			}
		case 1:
			point = 2
		case 2:
			point = 3
			t1 = monotoneSlope3(x0, y0, x1, y1, x, y)
			emitBezier(&b, x0, y0, x1, y1, monotoneSlope2(x0, y0, x1, y1, t1), t1)
		default:
			t1 = monotoneSlope3(x0, y0, x1, y1, x, y)
			emitBezier(&b, x0, y0, x1, y1, t0, t1)
		}
		x0, y0 = x1, y1
		x1, y1 = x, y
		t0 = t1
	}
	lineEnd := func() {
		switch point {
		case 2:
			fmt.Fprintf(&b, "L%s,%s", jsNum(x1), jsNum(y1))
		case 3:
			emitBezier(&b, x0, y0, x1, y1, t0, monotoneSlope2(x0, y0, x1, y1, t0))
		}
	}

	for _, p := range data {
		emitPoint(xScale.apply(p.x), yScale(p.y))
	}
	lineEnd()
	return b.String()
}

func emitBezier(b *strings.Builder, x0, y0, x1, y1, t0, t1 float64) {
	dx := (x1 - x0) / 3
	fmt.Fprintf(b, "C%s,%s,%s,%s,%s,%s",
		jsNum(x0+dx), jsNum(y0+dx*t0),
		jsNum(x1-dx), jsNum(y1-dx*t1),
		jsNum(x1), jsNum(y1))
}

// monotoneSlope3 移植 d3-shape 的 slope3（Fritsch–Carlson / Steffen 切线）。
// 原 JS 实现有除 0/除 -0 的边界处理（h0 || h1<0 && -0），本模块数据 x 严格
// 递增（日期聚合后唯一且升序），h0/h1 恒不为 0，直接省略该分支。
func monotoneSlope3(x0, y0, x1, y1, x2, y2 float64) float64 {
	h0 := x1 - x0
	h1 := x2 - x1
	s0 := (y1 - y0) / h0
	s1 := (y2 - y1) / h1
	p := (s0*h1 + s1*h0) / (h0 + h1)
	// 复刻 JS sign(s0)+sign(s1)：负数记 -1，其余（含 0）记 +1
	signs := 1.0
	if s0 < 0 {
		signs = -1
	}
	if s1 < 0 {
		signs -= 1
	} else {
		signs += 1
	}
	r := signs * math.Min(math.Min(math.Abs(s0), math.Abs(s1)), 0.5*math.Abs(p))
	if r == 0 || math.IsNaN(r) {
		return 0 // JS: ... || 0
	}
	return r
}

// monotoneSlope2 移植 d3-shape 的 slope2。x 严格递增保证 h != 0，省略原
// JS 的 h==0 回退。
func monotoneSlope2(x0, y0, x1, y1, t float64) float64 {
	return (3*(y1-y0)/(x1-x0) - t) / 2
}
