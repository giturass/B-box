package httpdebug

import (
	"context"
	"errors"
	"math"
	"time"
)

func exceedsBodyLimit(size, limit int64) bool { return limit > 0 && size > limit }

func configuredBodyLimit(limit int64) (int64, error) {
	if limit < -1 {
		return 0, errors.New("body limit must be positive, or -1 for unlimited")
	}
	return max(limit, 0), nil
}

// These are integer representation boundaries, not Android capacity estimates.
func validTimeoutMS(value int) bool {
	return value >= -1 && int64(value) <= math.MaxInt64/int64(time.Millisecond)
}

func scriptContext(parent context.Context, timeoutMS int) (context.Context, context.CancelFunc) {
	if timeoutMS <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, time.Duration(timeoutMS)*time.Millisecond)
}
