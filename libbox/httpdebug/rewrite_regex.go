package httpdebug

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/dlclark/regexp2/v2"
)

const (
	regexMatchTimeout    = 200 * time.Millisecond
	regexRequestBudget   = 500 * time.Millisecond
	maxRegexReplacements = 8192
)

var regexSlots = make(chan struct{}, 4)
var errRegexBudget = errors.New("HTTP regular expression processing budget exhausted")

// The budget counts matching, replacement and capacity waits, across both
// phases. Uploads, scripts and upstream latency do not consume regex time.
type regexBudget struct {
	ctx       context.Context
	remaining time.Duration
}

func newRegexBudget(ctx context.Context) *regexBudget {
	return &regexBudget{ctx: ctx, remaining: regexRequestBudget}
}

type rewriteRegexp struct {
	linear       *regexp.Regexp
	backtracking *regexp2.Regexp
	gate         chan struct{}
	groups       int
}

func compileRewritePattern(pattern string) (*rewriteRegexp, error) {
	if len(pattern) > 4096 {
		return nil, errors.New("regular expression exceeds 4096 bytes")
	}
	linear, linearErr := regexp.Compile(pattern)
	if linearErr == nil {
		if linear.NumSubexp() > 32 {
			return nil, errors.New("regular expression exceeds 32 capture groups")
		}
		return &rewriteRegexp{linear: linear, groups: linear.NumSubexp() + 1}, nil
	}
	backtracking, err := regexp2.Compile(pattern, regexp2.RE2, regexp2.OptionMaintainCaptureOrder(),
		regexp2.OptionMaxBacktrackingStackSize(100000), regexp2.OptionMaxCachedRuneBufferLength(64*1024))
	if err != nil {
		return nil, err
	}
	numbers := backtracking.GetGroupNumbers()
	for _, number := range numbers {
		if number > 32 || len(numbers) > 33 {
			return nil, errors.New("regular expression exceeds 32 capture groups")
		}
	}
	if backtracking.RightToLeft() {
		return nil, errors.New("right-to-left regular expression mode is not supported")
	}
	backtracking.MatchTimeout = regexMatchTimeout
	return &rewriteRegexp{backtracking: backtracking, gate: make(chan struct{}, 1), groups: 33}, nil
}

func (r *rewriteRegexp) SubexpIndex(name string) int {
	if r.linear != nil {
		return r.linear.SubexpIndex(name)
	}
	return r.backtracking.GroupNumberFromName(name)
}

func (r *rewriteRegexp) run(b *regexBudget, action func(time.Time) error) (err error) {
	if b == nil {
		b = newRegexBudget(context.Background())
	}
	if err = b.ctx.Err(); err != nil {
		return err
	}
	if b.remaining <= 0 {
		return errRegexBudget
	}
	start := time.Now()
	deadline := start.Add(b.remaining)
	defer func() {
		b.remaining -= time.Since(start)
		if err == nil {
			if b.ctx.Err() != nil {
				err = b.ctx.Err()
			} else if b.remaining <= 0 {
				err = errRegexBudget
			}
		}
	}()
	if r.backtracking != nil {
		ctx, cancel := context.WithDeadline(b.ctx, deadline)
		defer cancel()
		select {
		case r.gate <- struct{}{}:
			defer func() { <-r.gate }()
		case <-ctx.Done():
			return fmt.Errorf("regular expression capacity: %w", ctx.Err())
		}
		select {
		case regexSlots <- struct{}{}:
			defer func() { <-regexSlots }()
		case <-ctx.Done():
			return fmt.Errorf("regular expression capacity: %w", ctx.Err())
		}
		// MatchTimeout is the library's only mutable match option. The gate
		// protects it, including all FindNextMatch calls in one replacement.
		r.backtracking.MatchTimeout = min(regexMatchTimeout, time.Until(deadline))
		if r.backtracking.MatchTimeout <= 0 {
			return errRegexBudget
		}
	}
	err = action(deadline)
	if err != nil {
		return fmt.Errorf("regular expression processing: %w", err)
	}
	return nil
}

func (r *rewriteRegexp) match(value string, b *regexBudget) (matched bool, err error) {
	err = r.run(b, func(time.Time) error {
		if r.linear != nil {
			matched = r.linear.MatchString(value)
			return nil
		}
		var matchErr error
		matched, matchErr = r.backtracking.MatchString(value)
		return matchErr
	})
	return
}

func (r *rewriteRegexp) eachMatch(value string, b *regexBudget, visit func([]int) error) error {
	return r.run(b, func(deadline time.Time) error {
		if r.linear != nil {
			matches := r.linear.FindAllStringSubmatchIndex(value, maxRegexReplacements+1)
			if len(matches) > maxRegexReplacements {
				return errors.New("rewrite exceeds 8192 replacements in one message")
			}
			for _, match := range matches {
				if time.Now().After(deadline) {
					return errRegexBudget
				}
				if err := visit(match); err != nil {
					return err
				}
			}
			return nil
		}
		match, err := r.backtracking.FindStringMatch(value)
		previousEnd, count := -1, 0
		for match != nil && err == nil {
			if time.Now().After(deadline) {
				return errRegexBudget
			}
			start, length := match.ByteRange()
			// RE2 suppresses an empty match adjacent to the previous match.
			if length != 0 || start != previousEnd {
				count++
				if count > maxRegexReplacements {
					return errors.New("rewrite exceeds 8192 replacements in one message")
				}
				indices := make([]int, r.groups*2)
				for i := range indices {
					indices[i] = -1
				}
				for _, number := range r.backtracking.GetGroupNumbers() {
					group := match.GroupByNumber(number)
					if group != nil && len(group.Captures) > 0 {
						at, size := group.ByteRange()
						indices[number*2], indices[number*2+1] = at, at+size
					}
				}
				if err = visit(indices); err != nil {
					return err
				}
				previousEnd = start + length
			}
			r.backtracking.MatchTimeout = min(regexMatchTimeout, time.Until(deadline))
			if r.backtracking.MatchTimeout <= 0 {
				return errRegexBudget
			}
			match, err = r.backtracking.FindNextMatch(match)
		}
		return err
	})
}
