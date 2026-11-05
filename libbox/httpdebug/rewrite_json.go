package httpdebug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/itchyny/gojq"
)

// No module, input or environment loaders are installed. Each run has its own
// JSON tree; compiled jq bytecode can safely be reused across HTTP/2 streams.
func rewriteJSONBody(parent context.Context, code *gojq.Code, body []byte, limit int64, budget time.Duration) ([]byte, error) {
	if budget <= 0 {
		return nil, errors.New("jq processing budget exhausted")
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var input any
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("body must contain one JSON value")
	}
	output := &boundedJSONWriter{ctx: ctx, limit: limit}
	iter := code.RunWithContext(ctx, input)
	count := 0
	for {
		value, more := iter.Next()
		if !more {
			break
		}
		if err, ok := value.(error); ok {
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.Value() == nil {
				break
			}
			return nil, err
		}
		count++
		if count > 8192 {
			return nil, errors.New("jq output exceeds 8192 values")
		}
		if count > 1 {
			if err := output.append([]byte{'\n'}); err != nil {
				return nil, err
			}
		}
		if err := output.value(value, 0); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if count == 0 {
		return body, nil
	}
	return output.Bytes(), nil
}

// Check the output as it is encoded, including string escaping. Do not marshal
// an arbitrarily large jq array/map into a temporary unbounded JSON buffer.
type boundedJSONWriter struct {
	bytes.Buffer
	ctx   context.Context
	limit int64
}

func (w *boundedJSONWriter) append(data []byte) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if int64(w.Len())+int64(len(data)) > w.limit {
		return errRewriteSize
	}
	_, _ = w.Write(data)
	return nil
}

func (w *boundedJSONWriter) value(value any, depth int) error {
	if depth > 256 {
		return errors.New("jq output nesting exceeds 256")
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	switch v := value.(type) {
	case []any:
		if int64(len(v)) > w.limit-int64(w.Len()) {
			return errRewriteSize
		}
		if err := w.append([]byte{'['}); err != nil {
			return err
		}
		for i, item := range v {
			if i > 0 {
				if err := w.append([]byte{','}); err != nil {
					return err
				}
			}
			if err := w.value(item, depth+1); err != nil {
				return err
			}
		}
		return w.append([]byte{']'})
	case map[string]any:
		if int64(len(v)) > w.limit-int64(w.Len()) {
			return errRewriteSize
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		if err := w.append([]byte{'{'}); err != nil {
			return err
		}
		for i, key := range keys {
			if i > 0 {
				if err := w.append([]byte{','}); err != nil {
					return err
				}
			}
			if err := w.value(key, depth+1); err != nil {
				return err
			}
			if err := w.append([]byte{':'}); err != nil {
				return err
			}
			if err := w.value(v[key], depth+1); err != nil {
				return err
			}
		}
		return w.append([]byte{'}'})
	case string:
		if int64(len(v)) > w.limit-int64(w.Len()) {
			return errRewriteSize
		}
	}
	// gojq's marshaler also handles its arbitrary precision integers and
	// floating-point special values using jq's JSON conventions.
	data, err := gojq.Marshal(value)
	if err != nil {
		return err
	}
	return w.append(data)
}
