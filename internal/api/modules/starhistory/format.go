package starhistory

// 数字与时间线标签格式化，移植自：
//   - shared/packages/utils/getFormatNumber.tsx
//   - shared/packages/utils/getFormatTimeline.tsx（dayjs duration 常量）
//   - d3-scale@3.3.0 time.js 的 tickFormat 边界链（非 multiFormat）
//
// 小数格式化用 strconv.FormatFloat 近似 JS toFixed（.5 边界为银行家舍入而非
// JS 的五入，仅标签末位可能有差，如 50 星显示 "0.0k" 而非 "0.1k"）。

import (
	"math"
	"strconv"
	"time"
)

// jsNum 输出与 JS 数字转字符串等价的表示（最短往返、-0 归一为 0）。
// 本模块数值域（坐标 ≤ ~1000、时间戳毫秒 ≤ ~3.2e11）远离 JS 的指数阈值
// （1e21 / 1e-6），'f' 最短形式与 JS 输出一致；NaN 归一为 0 防止渲染垃圾。
func jsNum(v float64) string {
	if math.IsNaN(v) {
		return "0"
	}
	return strconv.FormatFloat(v+0, 'f', -1, 64)
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
		return strconv.FormatFloat(n/1000000, 'f', 1, 64) + "M"
	}
	if n >= 1000 && math.Mod(n, 1000) == 0 {
		return jsNum(n/1000) + "k"
	}
	return strconv.FormatFloat(n/1000, 'f', 1, 64) + "k"
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
	months := strconv.FormatFloat(days/30, 'f', 0, 64)
	years := strconv.FormatFloat(days/365, 'f', 0, 64)
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
