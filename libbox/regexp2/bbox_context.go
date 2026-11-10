package regexp2

import "context"

// WithCancelCheck shares immutable bytecode and gives this caller an isolated
// runner pool. The callback executes on the matching goroutine, including while
// backtracking, and must not mutate the original expression.
func (re *Regexp) WithCancelCheck(check func() error) *Regexp {
	copy := *re
	copy.cancelCheck = check
	copy.runnerPool = nil
	copy.initCaches()
	return &copy
}

func (re *Regexp) WithContext(ctx context.Context) *Regexp {
	return re.WithCancelCheck(ctx.Err)
}
