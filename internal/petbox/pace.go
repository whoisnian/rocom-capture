package petbox

import (
	"math/rand/v2"
	"time"
)

// Pacer 生成「像人手在拖」的操作间隔。
//
// 参照真人操作:在仓库里把一只宠物拖到目标格,连带找位置大约 1~3 秒一次;连着拖几十次
// 之后总会停一下(翻页、看一眼、走神),所以除了每步的抖动还给一个周期性的长歇。
// 这两层加起来是为了让请求节奏不至于是等间隔的机器节拍。
type Pacer struct {
	Interval  time.Duration // 每步基准间隔
	Jitter    float64       // 抖动比例 0~1:实际间隔在 Interval×(1±Jitter) 内均匀取值
	RestEvery int           // 每这么多步歇一次(0 = 不歇)
	Rest      time.Duration // 歇多久(同样带抖动)
}

// DefaultPacer 是一组保守的默认值。
var DefaultPacer = Pacer{Interval: 2 * time.Second, Jitter: 0.4, RestEvery: 25, Rest: 15 * time.Second}

// Next 返回第 n 步(从 1 起)之后应等待的时长。
func (p Pacer) Next(n int) time.Duration {
	d := jitter(p.Interval, p.Jitter)
	if p.RestEvery > 0 && n%p.RestEvery == 0 {
		d += jitter(p.Rest, p.Jitter)
	}
	return d
}

// Estimate 估算走完 steps 步的总时长(取各步期望值,不含最后一步之后的等待)。
func (p Pacer) Estimate(steps int) time.Duration {
	if steps <= 1 {
		return 0
	}
	total := time.Duration(steps-1) * p.Interval
	if p.RestEvery > 0 {
		total += time.Duration((steps-1)/p.RestEvery) * p.Rest
	}
	return total
}

func jitter(d time.Duration, ratio float64) time.Duration {
	if d <= 0 {
		return 0
	}
	if ratio <= 0 {
		return d
	}
	f := 1 + ratio*(2*rand.Float64()-1)
	if f < 0 {
		f = 0
	}
	return time.Duration(float64(d) * f)
}
