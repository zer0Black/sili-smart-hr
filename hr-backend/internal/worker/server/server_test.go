package server

import (
	"testing"
	"time"
)

// 首次失败 n=0 是 asynq 实际调用值（自增前传入），负移位曾致进程崩溃。
func TestRetryDelay(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{0, 30 * time.Second},
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 120 * time.Second},
		{5, 8 * time.Minute},
		{6, 10 * time.Minute}, // 16min 封顶
		{25, 10 * time.Minute},
		{36, 10 * time.Minute}, // 移位溢出点之后仍安全
		{1000, 10 * time.Minute},
	}
	for _, c := range cases {
		if got := retryDelay(c.n); got != c.want {
			t.Errorf("retryDelay(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}
