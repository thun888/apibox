package starhistory

// 本文件移植 d3-array@2.12.1（ticks.js）与 d3-time@2.1.1 的时间刻度算法，
// 用于精确复刻 star-history 图表的坐标轴刻度。与 JS 版保持一致：
//   - 时间刻度基于服务器本地时区（d3-time 的 time scale 行为）
//   - tickIntervals 表与 bisectorRight 选择逻辑一一对应

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

// ---------- d3-time 基础时间间隔（本地时区） ----------

type timeInterval struct {
	// floor 对齐到间隔起点（本地时区）
	floor func(t time.Time) time.Time
	// offset 按 step 个间隔偏移（绝对时间加减，与 JS setTime 语义一致）
	offset func(t time.Time, step int) time.Time
	// count 返回 [start,end) 内的间隔数，可为 nil
	count func(start, end time.Time) float64
	// field 返回用于 every(k) 取模的字段值，可为 nil
	field func(t time.Time) int
}

const (
	durationSecond = 1e3
	durationMinute = 6e4
	durationHour   = 36e5
	durationDay    = 864e5
	durationWeek   = 6048e5
	durationMonth  = 2592e6
	durationYear   = 31536e6
)

var (
	intervalMillisecond = timeInterval{
		floor:  func(t time.Time) time.Time { return t },
		offset: func(t time.Time, step int) time.Time { return t.Add(time.Duration(step) * time.Millisecond) },
		count:  func(start, end time.Time) float64 { return float64(end.Sub(start) / time.Millisecond) },
	}
	intervalSecond = timeInterval{
		floor:  func(t time.Time) time.Time { return t.Truncate(time.Second) },
		offset: func(t time.Time, step int) time.Time { return t.Add(time.Duration(step) * time.Second) },
		count:  func(start, end time.Time) float64 { return float64(end.Sub(start) / time.Second) },
		// 与 JS 相同：second 的 field 取 UTC 秒（d3-time 的既有怪癖）
		field: func(t time.Time) int { return t.UTC().Second() },
	}
	intervalMinute = timeInterval{
		floor:  func(t time.Time) time.Time { return t.Truncate(time.Minute) },
		offset: func(t time.Time, step int) time.Time { return t.Add(time.Duration(step) * time.Minute) },
		count:  func(start, end time.Time) float64 { return float64(end.Sub(start) / time.Minute) },
		field:  func(t time.Time) int { return t.Minute() },
	}
	intervalHour = timeInterval{
		floor:  func(t time.Time) time.Time { return t.Truncate(time.Hour) },
		offset: func(t time.Time, step int) time.Time { return t.Add(time.Duration(step) * time.Hour) },
		count:  func(start, end time.Time) float64 { return float64(end.Sub(start) / time.Hour) },
		field:  func(t time.Time) int { return t.Hour() },
	}
	intervalDay = timeInterval{
		floor: func(t time.Time) time.Time {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		},
		offset: func(t time.Time, step int) time.Time { return t.AddDate(0, 0, step) },
		count: func(start, end time.Time) float64 {
			tzCorr := (startZoneOffset(start) - startZoneOffset(end)) * durationMinute
			return float64(end.Sub(start)-time.Duration(tzCorr)*time.Millisecond) / durationDay
		},
		field: func(t time.Time) int { return t.Day() - 1 },
	}
	// week：以周日为一周起点（d3-time 默认 sunday）
	intervalWeek = timeInterval{
		floor: func(t time.Time) time.Time {
			t = t.AddDate(0, 0, -int((t.Weekday()+7)%7))
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		},
		offset: func(t time.Time, step int) time.Time { return t.AddDate(0, 0, step*7) },
		count: func(start, end time.Time) float64 {
			tzCorr := (startZoneOffset(start) - startZoneOffset(end)) * durationMinute
			return float64(end.Sub(start)-time.Duration(tzCorr)*time.Millisecond) / durationWeek
		},
	}
	intervalMonth = timeInterval{
		floor: func(t time.Time) time.Time {
			return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
		},
		offset: func(t time.Time, step int) time.Time { return t.AddDate(0, step, 0) },
		count: func(start, end time.Time) float64 {
			return float64((end.Year()-start.Year())*12 + int(end.Month()) - int(start.Month()))
		},
		field: func(t time.Time) int { return int(t.Month()) - 1 },
	}
	intervalYear = timeInterval{
		floor: func(t time.Time) time.Time {
			return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
		},
		offset: func(t time.Time, step int) time.Time { return t.AddDate(step, 0, 0) },
		count: func(start, end time.Time) float64 {
			return float64(end.Year() - start.Year())
		},
		field: func(t time.Time) int { return t.Year() },
	}
)

// startZoneOffset 返回时间所在时区的偏移（分钟，UTC 以东为正），与 JS
// getTimezoneOffset()（UTC 以西为正）互为相反数，用于 count 的时区修正。
func startZoneOffset(t time.Time) float64 {
	_, offsetSec := t.Zone()
	return -float64(offsetSec) / 60
}

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

// every 返回步长为 k 的子间隔（k<=1 返回自身），k 非正/NaN/Inf 返回 nil。
func (iv *timeInterval) every(k float64) *timeInterval {
	kf := math.Floor(k)
	if math.IsNaN(kf) || math.IsInf(kf, 0) || !(kf > 0) {
		return nil
	}
	if !(kf > 1) {
		return iv
	}
	if iv.field != nil {
		return iv.filter(func(t time.Time) bool {
			return iv.field(t)%int(kf) == 0
		})
	}
	return iv.filter(func(t time.Time) bool {
		return math.Mod(iv.count(time.UnixMilli(0), t), kf) == 0
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

// yearEvery 移植 year.js 的 every 优化实现
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
			return time.Date(y, 1, 1, 0, 0, 0, 0, t.Location())
		},
		offset: func(t time.Time, step int) time.Time {
			return time.Date(t.Year()+step*ki, 1, 1, 0, 0, 0, 0, t.Location())
		},
	}
}

// millisecondEvery 移植 millisecond.js 的 every 优化实现
func millisecondEvery(k float64) *timeInterval {
	kf := math.Floor(k)
	if math.IsNaN(kf) || math.IsInf(kf, 0) || !(kf > 0) {
		return nil
	}
	if !(kf > 1) {
		return &intervalMillisecond
	}
	return &timeInterval{
		floor: func(t time.Time) time.Time {
			return time.UnixMilli(int64(math.Floor(float64(t.UnixMilli())/kf)) * int64(kf))
		},
		offset: func(t time.Time, step int) time.Time {
			return time.UnixMilli(t.UnixMilli() + int64(step)*int64(kf))
		},
		count: func(start, end time.Time) float64 {
			return float64(end.Sub(start)/time.Millisecond) / kf
		},
	}
}

// ---------- d3-time ticks ----------

type tickEntry struct {
	iv       *timeInterval
	step     float64
	duration float64
}

var tickIntervals = []tickEntry{
	{&intervalMillisecond, 1, 1},
	{&intervalMillisecond, 5, 5},
	{&intervalMillisecond, 15, 15},
	{&intervalMillisecond, 30, 30},
	{&intervalSecond, 1, durationSecond},
	{&intervalSecond, 5, 5 * durationSecond},
	{&intervalSecond, 15, 15 * durationSecond},
	{&intervalSecond, 30, 30 * durationSecond},
	{&intervalMinute, 1, durationMinute},
	{&intervalMinute, 5, 5 * durationMinute},
	{&intervalMinute, 15, 15 * durationMinute},
	{&intervalMinute, 30, 30 * durationMinute},
	{&intervalHour, 1, durationHour},
	{&intervalHour, 3, 3 * durationHour},
	{&intervalHour, 6, 6 * durationHour},
	{&intervalHour, 12, 12 * durationHour},
	{&intervalDay, 1, durationDay},
	{&intervalDay, 2, 2 * durationDay},
	{&intervalWeek, 1, durationWeek},
	{&intervalMonth, 1, durationMonth},
	{&intervalMonth, 3, 3 * durationMonth},
	{&intervalYear, 1, durationYear},
}

// timeTickInterval 移植 d3-time ticker：目标间隔 = |stop-start|/count，
// 在 tickIntervals 表上做 bisectorRight 选择，边界情况走 year/millisecond 的
// every 优化分支。
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
		return millisecondEvery(math.Max(d3TickStep(float64(start.UnixMilli()), float64(stop.UnixMilli()), count), 1))
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
// 注意与 JS 相同，stop 是包含的（+1ms）。
func timeTicks(start, stop time.Time, count float64) []time.Time {
	reverse := stop.Before(start)
	if reverse {
		start, stop = stop, start
	}
	var ticks []time.Time
	if iv := timeTickInterval(start, stop, count); iv != nil {
		ticks = iv.rangeDates(start, stop.Add(time.Millisecond))
	}
	if reverse {
		for i, j := 0, len(ticks)-1; i < j; i, j = i+1, j-1 {
			ticks[i], ticks[j] = ticks[j], ticks[i]
		}
	}
	return ticks
}
