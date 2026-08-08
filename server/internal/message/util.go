package message

import "time"

func nowMs() int64 { return time.Now().UnixMilli() }
