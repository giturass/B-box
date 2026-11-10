package httpdebug

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/dlclark/regexp2/v2"
)

type regexContext struct{ ctx context.Context }

func newRegexContext(ctx context.Context) *regexContext {
	return &regexContext{ctx: ctx}
}

type rewriteRegexp struct {
	linear       *regexp.Regexp
	backtracking *regexp2.Regexp
	groups       []int
	groupSlots   map[int]int
}

func compileRewritePattern(pattern string) (*rewriteRegexp, error) {
	if linear, err := regexp.Compile(pattern); err == nil {
		return &rewriteRegexp{linear: linear}, nil
	}
	backtracking, err := regexp2.Compile(pattern, regexp2.RE2, regexp2.OptionMaintainCaptureOrder())
	if err != nil {
		return nil, err
	}
	if backtracking.RightToLeft() {
		return nil, errors.New("right-to-left regular expression mode is not supported")
	}
	groups := backtracking.GetGroupNumbers()
	slots := make(map[int]int, len(groups))
	for index, number := range groups {
		slots[number] = index
	}
	return &rewriteRegexp{backtracking: backtracking, groups: groups, groupSlots: slots}, nil
}

func (r *rewriteRegexp) captureIndex(number int) int {
	if r.linear != nil {
		return number
	}
	if index, found := r.groupSlots[number]; found {
		return index
	}
	return -1
}

func (r *rewriteRegexp) SubexpIndex(name string) int {
	if r.linear != nil {
		return r.linear.SubexpIndex(name)
	}
	return r.captureIndex(r.backtracking.GroupNumberFromName(name))
}

// Compiled expressions stay immutable. Each backtracking invocation owns its
// cancellation check and runner pool, so one request cannot cancel another.
func (r *rewriteRegexp) run(scope *regexContext, action func(context.Context, *regexp2.Regexp) error) error {
	ctx := context.Background()
	if scope != nil {
		ctx = scope.ctx
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var matcher *regexp2.Regexp
	if r.backtracking != nil {
		matcher = r.backtracking.WithContext(ctx)
	}
	if err := action(ctx, matcher); err != nil {
		return fmt.Errorf("regular expression processing: %w", err)
	}
	return ctx.Err()
}

func (r *rewriteRegexp) match(value string, scope *regexContext) (matched bool, err error) {
	err = r.run(scope, func(_ context.Context, matcher *regexp2.Regexp) error {
		if r.linear != nil {
			matched = r.linear.MatchString(value)
			return nil
		}
		var matchErr error
		matched, matchErr = matcher.MatchString(value)
		return matchErr
	})
	return
}

func (r *rewriteRegexp) eachMatch(value string, scope *regexContext, visit func([]int) error) error {
	return r.run(scope, func(ctx context.Context, matcher *regexp2.Regexp) error {
		if r.linear != nil {
			for _, match := range r.linear.FindAllStringSubmatchIndex(value, -1) {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := visit(match); err != nil {
					return err
				}
			}
			return nil
		}
		match, err := matcher.FindStringMatch(value)
		previousEnd := -1
		for match != nil && err == nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			start, length := match.ByteRange()
			// Match Go RE2's handling of adjacent empty matches.
			if length != 0 || start != previousEnd {
				indices := make([]int, len(r.groups)*2)
				for i := range indices {
					indices[i] = -1
				}
				for index, number := range r.groups {
					group := match.GroupByNumber(number)
					if group != nil && len(group.Captures) > 0 {
						at, size := group.ByteRange()
						indices[index*2], indices[index*2+1] = at, at+size
					}
				}
				if err = visit(indices); err != nil {
					return err
				}
				previousEnd = start + length
			}
			match, err = matcher.FindNextMatch(match)
		}
		return err
	})
}
