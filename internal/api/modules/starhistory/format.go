package starhistory

// 数字与时间线标签格式化，移植自：
//   - shared/packages/utils/getFormatNumber.tsx
//   - shared/packages/utils/getFormatTimeline.tsx（dayjs duration 常量）
//   - d3-scale@3.3.0 time.js 的 tickFormat 边界链（非 multiFormat）

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// jsNum 模拟 JS 数字转字符串（shortest round-trip），保证路径坐标等
// 输出与 d3-path 一致。
func jsNum(v float64) string {
	if v == 0 || math.IsNaN(v) {
		return "0"
	}
	a := math.Abs(v)
	if a >= 1e21 || a < 1e-6 {
		s := strconv.FormatFloat(v, 'e', -1, 64)
		if i := strings.IndexByte(s, 'e'); i >= 0 {
			mant, exp := s[:i], s[i+1:]
			if strings.HasPrefix(exp, "-") && len(exp) > 2 {
				exp = "-" + strings.TrimLeft(exp[1:], "0")
			}
			return mant + "e" + exp
		}
		return s
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// jsToFixed 近似 JS Number.prototype.toFixed(digits)（正值，四舍五入、
// 遇 .5 进位），用于 k/M 单位的 1 位小数与月/年的整数输出。
func jsToFixed(v float64, digits int) string {
	m := math.Pow(10, float64(digits))
	r := math.Floor(v*m+0.5) / m
	return strconv.FormatFloat(r, 'f', digits, 64)
}

// ---------- getFormatNumber ----------

func getNumberFormatUnit(n float64) float64 {
	if n >= 1000000 {
		return 1000000
	}
	if n >= 300 {
		return 1000
	}
	return 1
}

func formatNumber(n float64, unit float64) string {
	if unit == 1 {
		return jsNum(n)
	}
	if unit == 1000000 {
		if n >= 1000000 && math.Mod(n, 1000000) == 0 {
			return jsNum(n/1000000) + "M"
		}
		return jsToFixed(n/1000000, 1) + "M"
	}
	if n >= 1000 && math.Mod(n, 1000) == 0 {
		return jsNum(n/1000) + "k"
	}
	return jsToFixed(n/1000, 1) + "k"
}

// ---------- getFormatTimeline ----------

// dayjs duration 常量（毫秒）：year=365.25d, month=30.4375d
const (
	tlYearMs  = 31557600000
	tlMonthMs = 2629800000
	tlWeekMs  = 604800000
	tlDayMs   = 86400000
)

func getTimestampFormatUnit(timestamp float64) string {
	if timestamp/tlYearMs > 1 {
		return "year"
	} else if timestamp/tlMonthMs > 1 {
		return "month"
	} else if timestamp/tlWeekMs > 1 {
		return "week"
	}
	return "day"
}

func formatTimeline(timestamp float64, unit string) string {
	if timestamp == 0 {
		return "day one"
	}
	seconds := math.Floor(timestamp / 1000)
	days := math.Floor(seconds / 86400)
	weeks := math.Floor(days / 7)
	months := jsToFixed(days/30, 0)
	years := jsToFixed(days/365, 0)
	switch unit {
	case "day":
		if days == 1 {
			return "a day"
		}
		return jsNum(days) + " days"
	case "week":
		if weeks == 1 {
			return "a week"
		}
		return jsNum(weeks) + " weeks"
	case "month":
		if months == "1" {
			return "a month"
		}
		return months + " months"
	default:
		if years == "1" {
			return "a year"
		}
		return years + " years"
	}
}

// ---------- d3-scale v3 time.tickFormat ----------

func d3TickFormat(t time.Time) string {
	second := func(d time.Time) time.Time { return d.Truncate(time.Second) }
	minute := func(d time.Time) time.Time { return d.Truncate(time.Minute) }
	hour := func(d time.Time) time.Time { return d.Truncate(time.Hour) }
	day := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
	}
	month := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, d.Location())
	}
	year := func(d time.Time) time.Time {
		return time.Date(d.Year(), 1, 1, 0, 0, 0, 0, d.Location())
	}
	// week：周日 00:00 作为一周起点（d3-time 默认 sunday）
	weekFloor := func(d time.Time) time.Time {
		d = d.AddDate(0, 0, -int((d.Weekday()+7)%7))
		return day(d)
	}

	// d3-scale v3 的两级边界判断链（不同于 v4+ 的 multiFormat）：
	// 格式选择取决于“按该粒度向下取整后是否还等于自身”
	switch {
	case second(t).Before(t):
		return t.Format(".000") // formatMillisecond ".%L"
	case minute(t).Before(t):
		return t.Format(":05") // formatSecond ":%S"
	case hour(t).Before(t):
		return t.Format("03:04") // formatMinute "%I:%M"
	case day(t).Before(t):
		return t.Format("3 PM") // formatHour "%I %p"
	case month(t).Before(t):
		if weekFloor(t).Before(t) {
			return t.Format("Mon 02") // formatDay "%a %d"
		}
		return t.Format("Jan 02") // formatWeek "%b %d"
	case year(t).Before(t):
		return t.Format("January") // formatMonth "%B"
	}
	return t.Format("2006") // formatYear "%Y"
}

// escapeXML 转义文本节点中的 XML 特殊字符
func escapeXML(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
