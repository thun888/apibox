package starhistory

// 本文件移植 d3-array@2.12.1（ticks.js）的数值刻度算法与 d3-time@2.1.1 的
// 时间刻度选择逻辑，用于复刻 star-history 图表的坐标轴刻度。
//
// 相对 JS 版做了三处裁剪，其余逻辑逐行对应：
//   - 时间间隔表只保留 day 及以上粒度。本模块数据最少 5 个不同日期
//     （minDataPoints），时间跨度 ≥ 4 天，目标间隔 = |stop-start|/5 恒
//     ≥ 69.12e6 ms（约 19.2 小时）。按原表 bisector 规则，hour12 与 day1
//     的胜负分界在 61.09e6 ms，因此本模块恒选中 day 或更粗粒度，
//     d3-time 的 millisecond~hour 间隔不可达，不移植。
//   - timeTicks 的 reverse 分支不可达（星标记录按日期升序，start ≤ stop），
//     省略。
//   - 刻度固定按 UTC 对齐。原 JS 版跟随服务器本地时区（d3-time 的 time
//     scale 行为），输出随部署环境漂移；数据日期本身是 UTC 零点，改为 UTC
//     后输出确定。调用方传入 UTC 定位的时间（见 chart.go）。

import (
	"math"
	"time"
)

// ---------- d3-array ticks ----------

var (
	d3e10 = math.Sqrt(50)
	d3e5  = math.Sqrt(10)
	d3e2  = math.Sqrt(2)
)

func d3TickIncrement(start, stop, count float64) float64 {
	step := (stop - start) / math.Max(0, count)
	power := math.Floor(math.Log(step) / math.Ln10)
	err := step / math.Pow(10, power)
	var m float64
	switch {
	case err >= d3e10:
		m = 10
	case err >= d3e5:
		m = 5
	case err >= d3e2:
		m = 2
	default:
		m = 1
	}
	if power >= 0 {
		return m * math.Pow(10, power)
	}
	return -math.Pow(10, -power) / m
}

func d3TickStep(start, stop, count float64) float64 {
	step0 := math.Abs(stop-start) / math.Max(0, count)
	step1 := math.Pow(10, math.Floor(math.Log(step0)/math.Ln10))
	err := step0 / step1
	switch {
	case err >= d3e10:
		step1 *= 10
	case err >= d3e5:
		step1 *= 5
	case err >= d3e2:
		step1 *= 2
	}
	if stop < start {
		return -step1
	}
	return step1
}

func d3Ticks(start, stop, count float64) []float64 {
	var ticks []float64
	reverse := false
	if start == stop && count > 0 {
		return []float64{start}
	}
	if stop < start {
		start, stop = stop, start
		reverse = true
	}
	step := d3TickIncrement(start, stop, count)
	if step == 0 || math.IsInf(step, 0) || math.IsNaN(step) {
		return nil
	}
	if step > 0 {
		r0 := math.Round(start / step)
		r1 := math.Round(stop / step)
		if r0*step < start {
			r0++
		}
		if r1*step > stop {
			r1--
		}
		for r := r0; r <= r1; r++ {
			ticks = append(ticks, r*step)
		}
	} else {
		step = -step
		r0 := math.Round(start * step)
		r1 := math.Round(stop * step)
		if r0/step < start {
			r0++
		}
		if r1/step > stop {
			r1--
		}
		for r := r0; r <= r1; r++ {
			ticks = append(ticks, r/step)
		}
	}
	if reverse {
		for i, j := 0, len(ticks)-1; i < j; i, j = i+1, j-1 {
			ticks[i], ticks[j] = ticks[j], ticks[i]
		}
	}
	return ticks
}

// ---------- d3-time 时间间隔（day 及以上，UTC） ----------

type timeInterval struct {
	// floor 对齐到间隔起点（UTC）
	floor func(t time.Time) time.Time
	// offset 按 step 个间隔偏移（绝对时间加减，与 JS setTime 语义一致）
	offset func(t time.Time, step int) time.Time
	// field 返回用于 every(k) 取模的字段值，可为 nil
	field func(t time.Time) int
}

const (
	durationDay   = 864e5
	durationWeek  = 6048e5
	durationMonth = 2592e6
	durationYear  = 31536e6
)

var (
	intervalDay = timeInterval{
		floor: func(t time.Time) time.Time {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		},
		offset: func(t time.Time, step int) time.Time { return t.AddDate(0, 0, step) },
		field:  func(t time.Time) int { return t.Day() - 1 },
	}
	// week：以周日为一周起点（d3-time 默认 sunday）
	intervalWeek = timeInterval{
		floor: func(t time.Time) time.Time {
			t = t.AddDate(0, 0, -int((t.Weekday()+7)%7))
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		},
		offset: func(t time.Time, step int) time.Time { return t.AddDate(0, 0, step*7) },
	}
	intervalMonth = timeInterval{
		floor: func(t time.Time) time.Time {
			return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		},
		offset: func(t time.Time, step int) time.Time { return t.AddDate(0, step, 0) },
		field:  func(t time.Time) int { return int(t.Month()) - 1 },
	}
	intervalYear = timeInterval{
		floor: func(t time.Time) time.Time {
			return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		},
		offset: func(t time.Time, step int) time.Time { return t.AddDate(step, 0, 0) },
	}
)

// ceil 对齐到下一个间隔起点（JS: floori(+date-1), offseti(+date,1), floori(+date)）
func (iv *timeInterval) ceil(t time.Time) time.Time {
	t = iv.floor(t.Add(-time.Millisecond))
	t = iv.offset(t, 1)
	return iv.floor(t)
}

// rangeDates 返回 [start,stop) 内间隔起点列表；stop 须晚于 start。
func (iv *timeInterval) rangeDates(start, stop time.Time) []time.Time {
	var out []time.Time
	start = iv.ceil(start)
	if !start.Before(stop) {
		return nil
	}
	for {
		previous := start
		out = append(out, previous)
		start = iv.offset(start, 1)
		start = iv.floor(start)
		if !(previous.Before(start) && start.Before(stop)) {
			break
		}
	}
	return out
}

// every 返回步长为 k 的子间隔（k<=1 返回自身）。
// 表中 k>1 的只有 day.every(2) 与 month.every(3)，两者都有 field，直接取模
// 过滤；原 JS 依赖 count 的通用回退路径在本模块不可达，不移植。
func (iv *timeInterval) every(k float64) *timeInterval {
	kf := math.Floor(k)
	if math.IsNaN(kf) || math.IsInf(kf, 0) || !(kf > 0) {
		return nil
	}
	if !(kf > 1) {
		return iv
	}
	return iv.filter(func(t time.Time) bool {
		return iv.field(t)%int(kf) == 0
	})
}

func (iv *timeInterval) filter(test func(time.Time) bool) *timeInterval {
	return &timeInterval{
		floor: func(t time.Time) time.Time {
			for {
				t = iv.floor(t)
				if test(t) {
					return t
				}
				t = t.Add(-time.Millisecond)
			}
		},
		offset: func(t time.Time, step int) time.Time {
			if step < 0 {
				for i := step; i < 0; i++ {
					t = iv.offset(t, -1)
					for !test(t) {
						t = iv.offset(t, -1)
					}
				}
			} else {
				for i := 0; i < step; i++ {
					t = iv.offset(t, 1)
					for !test(t) {
						t = iv.offset(t, 1)
					}
				}
			}
			return t
		},
	}
}

// yearEvery 移植 year.js 的 every 优化实现（目标间隔大于 1 年时按步长取整）
func yearEvery(k float64) *timeInterval {
	kf := math.Floor(k)
	if math.IsNaN(kf) || math.IsInf(kf, 0) || !(kf > 0) {
		return nil
	}
	if !(kf > 1) {
		return &intervalYear
	}
	ki := int(kf)
	return &timeInterval{
		floor: func(t time.Time) time.Time {
			y := int(math.Floor(float64(t.Year())/kf)) * ki
			return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		},
		offset: func(t time.Time, step int) time.Time {
			return time.Date(t.Year()+step*ki, 1, 1, 0, 0, 0, 0, time.UTC)
		},
	}
}

// ---------- d3-time ticks ----------

type tickEntry struct {
	iv       *timeInterval
	step     float64
	duration float64
}

// tickIntervals 简化表：只保留 day 及以上粒度（原因见文件头注释）。
var tickIntervals = []tickEntry{
	{&intervalDay, 1, durationDay},
	{&intervalDay, 2, 2 * durationDay},
	{&intervalWeek, 1, durationWeek},
	{&intervalMonth, 1, durationMonth},
	{&intervalMonth, 3, 3 * durationMonth},
	{&intervalYear, 1, durationYear},
}

// timeTickInterval 移植 d3-time ticker：目标间隔 = |stop-start|/count，在
// tickIntervals 表上做 bisectorRight 选择，边界情况走 year 的 every 优化
// 分支。target 小于一天（i==0）时直接选 day——本模块时间跨度 ≥ 4 天，
// 更细粒度既不可达也不在简化表中。
func timeTickInterval(start, stop time.Time, count float64) *timeInterval {
	target := math.Abs(float64(stop.Sub(start))/float64(time.Millisecond)) / count
	i := 0
	for i < len(tickIntervals) && tickIntervals[i].duration <= target {
		i++
	}
	if i == len(tickIntervals) {
		return yearEvery(d3TickStep(float64(start.UnixMilli())/durationYear, float64(stop.UnixMilli())/durationYear, count))
	}
	if i == 0 {
		return &intervalDay
	}
	var e tickEntry
	if target/tickIntervals[i-1].duration < tickIntervals[i].duration/target {
		e = tickIntervals[i-1]
	} else {
		e = tickIntervals[i]
	}
	return e.iv.every(e.step)
}

// timeTicks 移植 d3-time ticks(start, stop, count)：interval.range(start, +stop+1)
// 注意与 JS 相同，stop 是包含的（+1ms）。调用方保证 start ≤ stop（星标记录
// 按日期升序），省略 JS 的 reverse 分支。
func timeTicks(start, stop time.Time, count float64) []time.Time {
	iv := timeTickInterval(start, stop, count)
	if iv == nil {
		return nil
	}
	return iv.rangeDates(start, stop.Add(time.Millisecond))
}
