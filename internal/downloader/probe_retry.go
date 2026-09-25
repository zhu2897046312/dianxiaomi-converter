package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

func retryableProbeError(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || (errors.As(err, &ne) && (ne.Timeout() || ne.Temporary()))
}

func probeWithRetry(ctx context.Context, client *http.Client, url string, cfg Config, onRetry func(int, error)) (int, int, error) {
	var last error
	for attempt := 1; attempt <= cfg.ProbeRetries+1; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, attempt - 1, err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.ProbeTimeoutSeconds)*time.Second)
		status, err := Probe(attemptCtx, client, url)
		cancel()
		if err == nil {
			return status, attempt, nil
		}
		if ctx.Err() != nil {
			return 0, attempt, ctx.Err()
		}
		last = err
		if attempt > cfg.ProbeRetries || !retryableProbeError(err) {
			return 0, attempt, fmt.Errorf("检测失败（已尝试 %d 次，未收到有效 HTTP 状态；未触发补下载）：%w", attempt, err)
		}
		if onRetry != nil {
			onRetry(attempt+1, err)
		}
		timer := time.NewTimer(time.Duration(cfg.ProbeRetryDelayMS) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, attempt, ctx.Err()
		case <-timer.C:
		}
	}
	return 0, 0, last
}

func HTTPStatusLabel(status int) string {
	if status == 0 {
		return "未收到响应"
	}
	return fmt.Sprintf("HTTP %d", status)
}
