package fdl

import "time"

// civil 返回 unix 秒对应的本地时间（年、月、日、时、分）。上游没有
// 时区数据库、用 `date +%z` 取当前本地偏移；这里等价地用本地时区的
// 当前偏移。
func civil(secs uint64) (y int64, m, d, h, mi int) {
	t := time.Unix(int64(secs), 0).In(time.FixedZone("", int(localOffset())))
	return int64(t.Year()), int(t.Month()), t.Day(), t.Hour(), t.Minute()
}

func localOffset() int64 {
	_, off := time.Now().Zone()
	return int64(off)
}
