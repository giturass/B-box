package libbox

import (
	"context"
	"encoding/base64"
	"errors"
	"sync/atomic"

	"github.com/sagernet/sing-box/experimental/httpdebug"
)

// An injection task owns one cancellable write to a flow in this engine. A
// reload cannot redirect its stream ID to a replacement service instance.
type UICaptureInjectTask struct {
	ctx    context.Context
	cancel context.CancelFunc
	engine *httpdebug.Engine
	used   atomic.Bool
}

func NewUICaptureInjectTask() (*UICaptureInjectTask, error) {
	if !uiCaptureLifecycle.TryRLock() {
		return nil, errors.New("local service is changing during stream injection")
	}
	defer uiCaptureLifecycle.RUnlock()
	engine := httpdebug.Current()
	if engine == nil {
		return nil, errors.New("local stream processing is not running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &UICaptureInjectTask{ctx: ctx, cancel: cancel, engine: engine}, nil
}

func (t *UICaptureInjectTask) Inject(streamID, direction string, opcode int32, dataBase64 string) error {
	if t == nil || !t.used.CompareAndSwap(false, true) {
		return errors.New("stream injection task has already been used")
	}
	if err := t.ctx.Err(); err != nil {
		return err
	}
	data, err := base64.StdEncoding.Strict().DecodeString(dataBase64)
	if err != nil {
		return errors.New("stream injection data must be valid Base64")
	}
	return t.engine.InjectStream(t.ctx, streamID, direction, int(opcode), data)
}

func (t *UICaptureInjectTask) Close() {
	if t != nil {
		t.cancel()
	}
}
