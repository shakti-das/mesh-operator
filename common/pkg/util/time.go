package util

import (
	"time"
)

var startTime = time.Now()

func TimeToUnixMillis(timestamp time.Time) int64 {
	return timestamp.UnixNano() / int64(time.Millisecond)
}

func Uptime() time.Duration {
	return time.Since(startTime)
}

func UptimeMs() int64 {
	return Uptime().Nanoseconds() / int64(time.Millisecond)
}
