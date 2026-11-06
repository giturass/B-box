package httpdebug

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dop251/goja"
)

const maxProcessingCacheBytes = 64 * 1024 * 1024

type processingResource struct {
	path     string
	limit    int64
	script   bool
	interval time.Duration
	debug    bool
}

func compileProcessingResource(path string, limit int64, script bool, interval int64, debug bool) (*processingResource, error) {
	if path == "" || len(path) > 8192 || strings.ContainsAny(path, "\r\n\x00") {
		return nil, errors.New("resource file path or URL is invalid")
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		if _, err := parseRewriteURL(path); err != nil {
			return nil, err
		}
	} else if !filepath.IsLocal(path) || strings.Contains(path, "://") {
		return nil, errors.New("local resource paths must stay inside the profile directory")
	}
	if interval == 0 {
		interval = 86400
	}
	if interval < 1 || interval > 31536000 {
		return nil, errors.New("resource update interval must be 1..31536000 seconds")
	}
	return &processingResource{path: path, limit: limit, script: script, interval: time.Duration(interval) * time.Second, debug: debug}, nil
}

func (s *processingResource) remote() bool {
	return strings.HasPrefix(s.path, "http://") || strings.HasPrefix(s.path, "https://")
}

func (s *processingResource) contentType() string {
	path := s.path
	if u, err := url.Parse(path); err == nil {
		path = u.Path
	}
	if kind := mime.TypeByExtension(filepath.Ext(path)); kind != "" {
		return kind
	}
	return "application/octet-stream"
}

type processingResourceValue struct {
	data       []byte
	program    *goja.Program
	sourceHash [sha256.Size]byte
}

func (s *processingResource) validate(data []byte, state *resourceState) (processingResourceValue, error) {
	if int64(len(data)) > s.limit {
		return processingResourceValue{}, errors.New("resource exceeds size limit")
	}
	value := processingResourceValue{data: data}
	if s.script {
		if !s.debug {
			value.sourceHash = sha256.Sum256(data)
			if state.program != nil && state.sourceHash == value.sourceHash {
				value.program = state.program
				return value, nil
			}
		}
		program, err := compileScriptSource(s.path, string(data), "", nil)
		if err != nil {
			return processingResourceValue{}, err
		}
		value.program = program
	}
	return value, nil
}

type resourceCacheDocument struct {
	Fetched time.Time `json:"fetched"`
	Data    []byte    `json:"data"`
}

type resourceState struct {
	gate        chan struct{}
	lastAttempt time.Time
	next        time.Time
	// Loads hold gate and retain only the last usable program for this resource.
	program    *goja.Program
	sourceHash [sha256.Size]byte
}

func (s *resourceState) cacheProgram(spec *processingResource, value processingResourceValue) {
	if spec.script && !spec.debug {
		s.program = value.program
		s.sourceHash = value.sourceHash
	}
}

// URL bodies are read from disk; each script retains at most one compiled program.
// Each consumer owns a body reservation until its response/script is released.
type processingResources struct {
	ctx              context.Context
	cancel           context.CancelFunc
	directory        string
	profileDirectory string
	dial             RewriteDialFunc
	roots            *x509.CertPool
	mu               sync.Mutex
	diskMu           sync.Mutex
	states           map[string]*resourceState
	specs            []*processingResource
	wg               sync.WaitGroup
}

func newProcessingResources(parent context.Context, directory, profileDirectory string, dial RewriteDialFunc, roots *x509.CertPool, specs []*processingResource) *processingResources {
	ctx, cancel := context.WithCancel(parent)
	r := &processingResources{ctx: ctx, cancel: cancel, directory: filepath.Join(directory, "resource-cache"),
		profileDirectory: profileDirectory, dial: dial, roots: roots, states: make(map[string]*resourceState), specs: specs}
	r.wg.Add(1)
	go r.refreshLoop()
	return r
}

func (r *processingResources) key(spec *processingResource) string {
	identity := spec.path
	if !spec.remote() {
		identity = filepath.Join(r.profileDirectory, spec.path)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%t:%d:%s", spec.script, spec.limit, identity)))
	return hex.EncodeToString(sum[:])
}

func (r *processingResources) state(spec *processingResource) *resourceState {
	key := r.key(spec)
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.states[key]
	if state == nil {
		state = &resourceState{gate: make(chan struct{}, 1)}
		r.states[key] = state
	}
	return state
}

func (r *processingResources) get(parent context.Context, spec *processingResource) (processingResourceValue, *bodyLease, error) {
	if r == nil {
		return processingResourceValue{}, nil, errors.New("file/URL resources require the running local service")
	}
	lease := reserveBodyWork(0)
	if lease == nil {
		return processingResourceValue{}, nil, errors.New("resource loading skipped: global body budget exhausted")
	}
	value, err := r.load(parent, spec, lease, false)
	if err != nil {
		lease.release()
		return processingResourceValue{}, nil, err
	}
	return value, lease, nil
}

func (r *processingResources) load(parent context.Context, spec *processingResource, lease *bodyLease, refresh bool) (processingResourceValue, error) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(r.ctx, cancel)
	defer func() { stop(); cancel() }()
	state := r.state(spec)
	select {
	case state.gate <- struct{}{}:
		defer func() { <-state.gate }()
	case <-ctx.Done():
		return processingResourceValue{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return processingResourceValue{}, err
	}
	if !spec.remote() {
		if spec.script && !spec.debug && state.program != nil {
			return processingResourceValue{program: state.program}, nil
		}
		data, err := r.readLocal(spec, lease)
		if err != nil {
			return processingResourceValue{}, err
		}
		value, err := spec.validate(data, state)
		if err == nil {
			state.cacheProgram(spec, value)
		}
		return value, err
	}
	cached, fetched, cacheErr := r.readCache(spec, state, lease)
	if cacheErr == nil {
		state.cacheProgram(spec, cached)
		if !refresh {
			return cached, nil
		}
	}
	now := time.Now()
	if cacheErr == nil && now.Before(fetched.Add(spec.interval)) {
		state.next = fetched.Add(spec.interval)
		return cached, nil
	}
	if now.Before(state.next) {
		if cacheErr == nil {
			return cached, nil
		}
		return processingResourceValue{}, errors.New("resource download retry is waiting after an earlier failure")
	}
	state.lastAttempt = now
	state.next = now.Add(min(spec.interval, time.Minute))
	if r.dial == nil {
		if cacheErr == nil {
			return cached, nil
		}
		return processingResourceValue{}, errors.New("resource downloads require a running sing-box route")
	}
	fetchCtx, stopFetch := context.WithTimeout(ctx, 15*time.Second)
	defer stopFetch()
	transport := newRewriteTransport(fetchCtx, r.dial, r.roots)
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	policy := scriptHTTPPolicy{unrestricted: true}
	client := &http.Client{Transport: transport, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("resource redirect limit exceeded")
		}
		return validateScriptHTTPDestination(request, policy)
	}}
	response, data, err := performScriptHTTPRequest(fetchCtx, client,
		scriptHTTPRequestOptions{url: spec.path, method: "GET", headers: make(http.Header), binary: true},
		spec.limit, policy, lease)
	if err == nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
		err = fmt.Errorf("resource HTTP status %d", response.StatusCode)
	}
	var value processingResourceValue
	if err == nil {
		value, err = spec.validate(data, state)
	}
	if err == nil {
		err = r.writeCache(ctx, spec, data)
	}
	if err != nil {
		writeLog("warn", "Resource "+spec.path+": update failed; keeping last usable cache: "+err.Error())
		if cacheErr == nil {
			return cached, nil
		}
		return processingResourceValue{}, err
	}
	state.cacheProgram(spec, value)
	state.next = time.Now().Add(spec.interval)
	return value, nil
}

func (r *processingResources) readLocal(spec *processingResource, lease *bodyLease) ([]byte, error) {
	if r.profileDirectory == "" {
		return nil, errors.New("local resources need the active profile directory")
	}
	root, err := os.OpenRoot(r.profileDirectory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.OpenFile(spec.path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > spec.limit {
		return nil, errors.New("local resource must be a regular file within the size limit")
	}
	return readScriptHTTPBody(file, spec.limit, lease, 0)
}

func (r *processingResources) cacheRoot(create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(r.directory, 0o700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(r.directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("resource cache is not a private directory")
	}
	return os.OpenRoot(r.directory)
}

func (r *processingResources) readCache(spec *processingResource, state *resourceState, lease *bodyLease) (processingResourceValue, time.Time, error) {
	root, err := r.cacheRoot(false)
	if err != nil {
		return processingResourceValue{}, time.Time{}, err
	}
	defer root.Close()
	file, err := root.OpenFile(r.key(spec)+".json", os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return processingResourceValue{}, time.Time{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	maximum := spec.limit*2 + 4096
	if err != nil {
		return processingResourceValue{}, time.Time{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maximum {
		return processingResourceValue{}, time.Time{}, errors.New("invalid cached resource file")
	}
	data, err := readScriptHTTPBody(file, maximum, lease, 0)
	if err != nil {
		return processingResourceValue{}, time.Time{}, err
	}
	var document resourceCacheDocument
	if int64(len(data)) > maximum {
		return processingResourceValue{}, time.Time{}, errors.New("cached resource exceeds size limit")
	}
	if err = json.Unmarshal(data, &document); err != nil {
		return processingResourceValue{}, time.Time{}, err
	}
	if document.Fetched.IsZero() || document.Fetched.After(time.Now().Add(time.Minute)) {
		return processingResourceValue{}, time.Time{}, errors.New("invalid resource cache timestamp")
	}
	value, err := spec.validate(document.Data, state)
	return value, document.Fetched, err
}

func (r *processingResources) writeCache(ctx context.Context, spec *processingResource, data []byte) error {
	r.diskMu.Lock()
	defer r.diskMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := r.cacheRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	encoded, err := json.Marshal(resourceCacheDocument{Fetched: time.Now(), Data: data})
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(r.directory)
	if err != nil {
		return err
	}
	name := r.key(spec) + ".json"
	total := int64(len(encoded))
	for _, entry := range entries {
		if entry.Name() == name || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("invalid resource cache entry")
		}
		total += info.Size()
	}
	if total > maxProcessingCacheBytes {
		return errors.New("resource disk cache exceeds 64 MiB")
	}
	pending := name + ".pending"
	// A leftover pending file is never a committed cache and is safe to remove.
	_ = root.Remove(pending)
	file, err := root.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { file.Close(); _ = root.Remove(pending) }()
	if n, err := file.Write(encoded); err != nil {
		return err
	} else if n != len(encoded) {
		return io.ErrShortWrite
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return root.Rename(pending, name)
}

func (r *processingResources) refreshLoop() {
	defer r.wg.Done()
	period := time.Minute
	for _, spec := range r.specs {
		if spec.remote() {
			period = min(period, spec.interval)
		}
	}
	for {
		for _, spec := range r.specs {
			if !spec.remote() {
				continue
			}
			if r.ctx.Err() != nil {
				return
			}
			state := r.state(spec)
			select {
			case state.gate <- struct{}{}:
				due := !time.Now().Before(state.next)
				<-state.gate
				if !due {
					continue
				}
			default:
				continue
			}
			lease := reserveBodyWork(0)
			if lease == nil {
				continue
			}
			_, _ = r.load(r.ctx, spec, lease, true)
			lease.release()
		}
		timer := time.NewTimer(period)
		select {
		case <-r.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *processingResources) Close() {
	if r != nil {
		r.cancel()
		r.wg.Wait()
	}
}

// Kept separate from configuration validation: no file or network is consulted
// while editing, checking, formatting, backing up or importing a profile.
func compileScriptResource(label, source, path, reference string, resources map[string]RewriteResource, interval int64, debug bool) (*goja.Program, *processingResource, error) {
	choices := 0
	for _, value := range []string{source, path, reference} {
		if value != "" {
			choices++
		}
	}
	if choices != 1 {
		return nil, nil, fmt.Errorf("%s: choose exactly one of source, script_path or resource", label)
	}
	if reference != "" {
		resource, found := resources[reference]
		if !found {
			return nil, nil, fmt.Errorf("%s: unknown script resource %q", label, reference)
		}
		if resource.DataType == "file" {
			path = resource.Data
			if interval == 0 {
				interval = resource.UpdateInterval
			}
		} else {
			data, err := decodeRewriteData(resource.DataType, resource.Data, MaxScriptBytes)
			if err != nil {
				return nil, nil, err
			}
			source = string(data)
		}
	}
	if path != "" {
		spec, err := compileProcessingResource(path, MaxScriptBytes, true, interval, debug)
		return nil, spec, err
	}
	program, err := compileScriptSource(label, source, "", nil)
	return program, nil, err
}
