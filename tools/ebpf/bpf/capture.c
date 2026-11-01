// SPDX-License-Identifier: GPL-3.0-or-later
// Passive public SSL ABI probes. File/offset attachment follows stackplz; the TLS
// state machine never pauses the application or changes its memory.
typedef unsigned char u8;
typedef unsigned short u16;
typedef unsigned int u32;
typedef int s32;
typedef unsigned long long u64;
#define SEC(name) __attribute__((section(name), used))
#define INLINE static __attribute__((always_inline)) inline
#define __uint(name, value) int (*name)[value]
#define __type(name, value) value *name
#define HASH 1
#define ARRAY 2
#define PERF 4
#define PERCPU_ARRAY 6
#define ANY 0
#define NOEXIST 1
#define CURRENT_CPU 0xffffffffULL
#define CHUNK 4096
#define MAX_CHUNKS 32

#if defined(__TARGET_ARCH_arm64)
struct regs { u64 x[31], sp, pc, pstate; };
#define ARG1(r) ((r)->x[0])
#define ARG2(r) ((r)->x[1])
#define ARG3(r) ((r)->x[2])
#define ARG4(r) ((r)->x[3])
#define RESULT(r) ((r)->x[0])
#define GO_G(r) ((r)->x[28])
#define GO_ARG1(r) ((r)->x[0])
#define GO_ARG2(r) ((r)->x[1])
#define GO_ARG3(r) ((r)->x[2])
#define GO_ARG4(r) ((r)->x[3])
#define GO_ARG5(r) ((r)->x[4])
#define GO_ARG7(r) ((r)->x[6])
#define GO_ARG8(r) ((r)->x[7])
#elif defined(__TARGET_ARCH_x86)
struct regs { u64 r15,r14,r13,r12,bp,bx,r11,r10,r9,r8,ax,cx,dx,si,di,orig_ax,ip,cs,flags,sp,ss; };
#define ARG1(r) ((r)->di)
#define ARG2(r) ((r)->si)
#define ARG3(r) ((r)->dx)
#define ARG4(r) ((r)->cx)
#define RESULT(r) ((r)->ax)
#define GO_G(r) ((r)->r14)
#define GO_ARG1(r) ((r)->ax)
#define GO_ARG2(r) ((r)->bx)
#define GO_ARG3(r) ((r)->cx)
#define GO_ARG4(r) ((r)->di)
#define GO_ARG5(r) ((r)->si)
#define GO_ARG7(r) ((r)->r9)
#define GO_ARG8(r) ((r)->r10)
#else
#error Unsupported capture architecture
#endif

static void *(*lookup)(void *, const void *) = (void *)1;
static long (*update)(void *, const void *, const void *, u64) = (void *)2;
static long (*remove_key)(void *, const void *) = (void *)3;
static u64 (*ktime)(void) = (void *)5;
static u64 (*pid_tid)(void) = (void *)14;
static u64 (*uid_gid)(void) = (void *)15;
static long (*output)(void *, void *, u64, void *, u64) = (void *)25;
static long (*read_user)(void *, u32, const void *) = (void *)112;
static long (*read_kernel)(void *, u32, const void *) = (void *)113;
static void *(*current_task)(void) = (void *)35;

struct connection_key { u64 ssl; u32 pid, pad; };
struct connection { u64 generation, write_sequence, read_sequence; u32 adapter, pad; };
struct session_key { u64 ssl, generation; u32 pid, pad; };
struct owner_key { u64 ssl, generation; u32 pid, direction; };
struct call_key { u64 thread; u32 direction, pad; };
struct call { u64 ssl, generation, buffer, requested, count_pointer, stack; u32 depth, extended; };
struct socket_identity { u64 inode, socket; u32 netns, conflict; };
struct event {
    u64 timestamp, ssl, generation, sequence;
    u32 pid, tid, uid, kind;
    s32 fd;
    u32 original_len, offset, captured_len, flags, reserved;
    u8 data[CHUNK];
};
_Static_assert(__builtin_offsetof(struct event, data) == 72, "event ABI");

#define HASH_MAP(name, key_type, value_type, count) \
    struct { __uint(type, HASH); __uint(max_entries, count); \
    __type(key, key_type); __type(value, value_type); } name SEC(".maps")
HASH_MAP(connections, struct connection_key, struct connection, 8192);
HASH_MAP(owners, struct owner_key, u64, 8192);
HASH_MAP(calls, struct call_key, struct call, 8192);
HASH_MAP(broken, struct session_key, u32, 8192);
HASH_MAP(clears, u64, u64, 8192);
HASH_MAP(socket_seen, struct session_key, struct socket_identity, 8192);
HASH_MAP(uids, u32, u8, 256);
struct { __uint(type, ARRAY); __uint(max_entries, 4);
    __type(key, u32); __type(value, u32); } config SEC(".maps");
struct { __uint(type, PERCPU_ARRAY); __uint(max_entries, 1);
    __type(key, u32); __type(value, struct event); } scratch SEC(".maps");
struct { __uint(type, PERCPU_ARRAY); __uint(max_entries, 1);
    __type(key, u32); __type(value, u64); } lost SEC(".maps");
struct { __uint(type, PERF); __uint(key_size, 4); __uint(value_size, 4); } events SEC(".maps");

INLINE void loss(void) {
    u32 zero = 0;
    u64 *counter = lookup(&lost, &zero);
    if (counter) __sync_fetch_and_add(counter, 1);
}
INLINE int selected(void) {
    u32 uid = (u32)uid_gid(), zero = 0, one = 1, two = 2;
    u32 *excluded = lookup(&config, &zero), *user = lookup(&config, &two);
    if (!excluded || !user || uid == *excluded || uid / 100000 != *user) return 0;
    u32 *filtered = lookup(&config, &one);
    return filtered && (!*filtered || lookup(&uids, &uid));
}
INLINE struct connection_key key_for(u64 ssl) {
    struct connection_key key = { .ssl = ssl, .pid = pid_tid() >> 32 };
    return key;
}
INLINE struct session_key session_for(u64 ssl, u64 generation) {
    struct session_key key = { .ssl = ssl, .generation = generation, .pid = pid_tid() >> 32 };
    return key;
}
INLINE struct event *prepare(u64 ssl, struct connection *connection, u32 kind) {
    u32 zero = 0;
    struct event *event = lookup(&scratch, &zero);
    if (!event) { loss(); return 0; }
    event->timestamp = ktime();
    event->ssl = ssl;
    event->generation = connection->generation;
    event->sequence = 0;
    // Owner slots serialize each direction without requiring fetched atomics
    // that are unavailable on some Android 5.10 BPF implementations.
    if (kind == 2) event->sequence = ++connection->write_sequence;
    if (kind == 3) event->sequence = ++connection->read_sequence;
    if (kind == 4) {
        event->sequence = connection->write_sequence;
        event->timestamp = connection->read_sequence;
    }
    u64 task = pid_tid();
    event->pid = task >> 32; event->tid = (u32)task;
    event->uid = (u32)uid_gid(); event->kind = kind;
    event->fd = -1; event->original_len = 0; event->offset = 0;
    event->captured_len = 0; event->flags = 0; event->reserved = 0;
    if (kind == 1) event->flags = connection->adapter;
    return event;
}
INLINE void emit(void *ctx, struct event *event, u32 length) {
    if (output(ctx, &events, CURRENT_CPU, event, 72 + length) < 0) loss();
}

// A local capture failure poisons only this SSL generation. A separate gap
// event avoids racing either direction's sequence counter. BPF_NOEXIST elects
// a single reporter even when both directions discover the gap concurrently.
INLINE void gap(void *ctx, u64 ssl, struct connection *connection, u32 reason) {
    struct session_key key = session_for(ssl, connection->generation);
    if (lookup(&broken, &key)) return;
    if (update(&broken, &key, &reason, NOEXIST)) {
        if (!lookup(&broken, &key)) loss();
        return;
    }
    struct event *event = prepare(ssl, connection, 5);
    if (event) { event->flags = reason; emit(ctx, event, 0); }
}
INLINE void retire(void *ctx, u64 ssl) {
    struct connection_key key = key_for(ssl);
    struct connection *connection = lookup(&connections, &key);
    if (!connection) return;
    struct owner_key owner = { .ssl = ssl, .generation = connection->generation,
        .pid = key.pid, .direction = 2 };
    if (lookup(&owners, &owner)) gap(ctx, ssl, connection, 6);
    owner.direction = 3;
    if (lookup(&owners, &owner)) gap(ctx, ssl, connection, 6);
    owner.direction = 0;
    if (lookup(&owners, &owner)) gap(ctx, ssl, connection, 6);
    struct session_key session = session_for(ssl, connection->generation);
    struct event *event = prepare(ssl, connection, 4);
    u32 *reason = lookup(&broken, &session);
    if (event) { if (reason) event->flags = *reason; emit(ctx, event, 0); }
    remove_key(&connections, &key);
    remove_key(&broken, &session);
    remove_key(&socket_seen, &session);
}
INLINE void create_connection(void *ctx, u64 ssl, u32 adapter) {
    if (!ssl || !selected()) return;
    struct connection_key key = key_for(ssl);
    struct connection *previous = lookup(&connections, &key);
    if (previous) { gap(ctx, ssl, previous, 7); retire(ctx, ssl); }
    struct connection value = { .generation = ktime(), .adapter = adapter };
    if (update(&connections, &key, &value, NOEXIST)) { loss(); return; }
    struct connection *connection = lookup(&connections, &key);
    if (!connection) { loss(); return; }
    struct event *event = prepare(ssl, connection, 1);
    if (event) emit(ctx, event, 0);
}
INLINE void create(void *ctx, u64 ssl) { create_connection(ctx, ssl, 0); }

SEC("uretprobe/ssl_new") int ssl_new(struct regs *ctx) { create(ctx, RESULT(ctx)); return 0; }
SEC("uprobe/ssl_free") int ssl_free(struct regs *ctx) { retire(ctx, ARG1(ctx)); return 0; }
SEC("uprobe/ssl_clear_enter") int ssl_clear_enter(struct regs *ctx) {
    u64 ssl = ARG1(ctx), thread = pid_tid();
    struct connection_key key = key_for(ssl);
    struct connection *connection = lookup(&connections, &key);
    if (!connection) return 0;
    struct owner_key owner = { .ssl = ssl, .generation = connection->generation,
        .pid = key.pid, .direction = 2 };
    u64 *writer = lookup(&owners, &owner);
    u64 write_owner = writer ? *writer : 0;
    owner.direction = 3;
    u64 *reader = lookup(&owners, &owner);
    u64 read_owner = reader ? *reader : 0;
    owner.direction = 0;
    u64 *handshake = lookup(&owners, &owner);
    u64 handshake_owner = handshake ? *handshake : 0;
    // OpenSSL's state_machine calls SSL_clear while the first SSL_read/write
    // lazily initializes its handshake. No application bytes exist yet and
    // this thread owns the only active direction: retain that fresh lifetime.
    if (!connection->write_sequence && !connection->read_sequence &&
        (!handshake_owner || handshake_owner == thread) &&
        ((write_owner == thread && !read_owner) || (read_owner == thread && !write_owner) ||
         (handshake_owner == thread && !write_owner && !read_owner))) return 0;
    // A reset after data, or from another thread, cannot migrate an in-flight
    // call into a fresh generation without losing its returned bytes. Retire
    // it and require a later idle clear/new before observing another stream.
    if (!write_owner && !read_owner && !handshake_owner && update(&clears, &thread, &ssl, ANY)) loss();
    retire(ctx, ssl);
    return 0;
}
SEC("uretprobe/ssl_clear_leave") int ssl_clear_leave(struct regs *ctx) {
    u64 thread = pid_tid(), *saved = lookup(&clears, &thread);
    if (!saved) return 0;
    u64 ssl = *saved;
    remove_key(&clears, &thread);
    if ((s32)RESULT(ctx) == 1) create(ctx, ssl);
    return 0;
}

INLINE int enter(struct regs *ctx, u32 direction, u32 extended) {
    if (!selected()) return 0;
    u64 ssl = ARG1(ctx);
    struct connection_key conn_key = key_for(ssl);
    struct connection *connection = lookup(&connections, &conn_key);
    struct call_key key = { .thread = pid_tid(), .direction = direction };
    struct call *active = lookup(&calls, &key);
    if (active && ctx->sp >= active->stack) {
        // A skipped return (for example longjmp) must not retain this thread's
        // owner slot or poison every later SSL object. Equal-stack tail calls
        // cannot be paired reliably either, so retire only the old generation.
        struct call stale = *active;
        struct connection_key outer_key = key_for(stale.ssl);
        struct connection *outer = lookup(&connections, &outer_key);
        if (outer && outer->generation == stale.generation) gap(ctx, stale.ssl, outer, 5);
        struct owner_key owner = { .ssl = stale.ssl, .generation = stale.generation,
            .pid = conn_key.pid, .direction = direction };
        remove_key(&calls, &key);
        remove_key(&owners, &owner);
        active = 0;
    }
    if (active) {
        if (!connection || active->ssl != ssl || active->generation != connection->generation) {
            if (connection) gap(ctx, ssl, connection, 2);
            struct connection_key outer_key = key_for(active->ssl);
            struct connection *outer = lookup(&connections, &outer_key);
            if (outer && outer->generation == active->generation) gap(ctx, active->ssl, outer, 2);
        }
        if (active->depth < 64) {
            active->depth++;
        } else {
            struct connection_key outer_key = key_for(active->ssl);
            struct connection *outer = lookup(&connections, &outer_key);
            if (outer && outer->generation == active->generation) gap(ctx, active->ssl, outer, 8);
        }
        return 0; // Public _ex/normal wrappers are captured exactly once.
    }
    if (!connection) return 0; // Never attach halfway through an existing TLS stream.
    struct session_key session = session_for(ssl, connection->generation);
    if (lookup(&broken, &session)) return 0;
    struct owner_key owner = { .ssl = ssl, .generation = connection->generation,
        .pid = conn_key.pid, .direction = direction };
    u64 thread = pid_tid();
    if (update(&owners, &owner, &thread, NOEXIST)) { gap(ctx, ssl, connection, 2); return 0; }
    struct call value = { .ssl = ssl, .generation = connection->generation,
        .buffer = ARG2(ctx), .requested = ARG3(ctx), .count_pointer = ARG4(ctx),
        .stack = ctx->sp, .depth = 1, .extended = extended };
    if (update(&calls, &key, &value, NOEXIST)) {
        remove_key(&owners, &owner);
        gap(ctx, ssl, connection, 4);
    }
    return 0;
}
INLINE void capture_return(struct regs *ctx, u32 direction, struct call value) {
    struct connection_key key = key_for(value.ssl);
    struct connection *connection = lookup(&connections, &key);
    if (!connection || connection->generation != value.generation) return;
    struct session_key session = session_for(value.ssl, value.generation);
    if (lookup(&broken, &session)) return;
    u64 length = 0;
    s32 result = (s32)RESULT(ctx);
    if (value.extended) {
        // SSL_read_early_data returns SUCCESS=1 or FINISH=2. Both may
        // carry a count; the ordinary _ex and early write APIs return 1.
        if (result != 1 && !(value.extended == 2 && result == 2)) return;
        if (read_user(&length, sizeof(length), (void *)value.count_pointer)) {
            gap(ctx, value.ssl, connection, 3); return;
        }
    } else {
        if (result <= 0) return;
        length = (u32)result;
    }
    if (!length) return;
    if (length > value.requested || length > CHUNK * MAX_CHUNKS) {
        gap(ctx, value.ssl, connection, 1); return;
    }
    struct event *event = prepare(value.ssl, connection, direction);
    if (!event) return;
    event->original_len = (u32)length;
#pragma unroll
    for (u32 i = 0; i < MAX_CHUNKS; i++) {
        u32 offset = i * CHUNK;
        if (offset >= length) break;
        u32 count = (u32)length - offset;
        if (count > CHUNK) count = CHUNK;
        if (!count) break;
        event->offset = offset; event->captured_len = count;
        if (read_user(event->data, count, (void *)(value.buffer + offset))) {
            gap(ctx, value.ssl, connection, 3); break;
        }
        emit(ctx, event, count);
    }
}
INLINE int leave(struct regs *ctx, u32 direction) {
    struct call_key key = { .thread = pid_tid(), .direction = direction };
    struct call *active = lookup(&calls, &key);
    if (!active) return 0;
    if (active->depth > 1) { active->depth--; return 0; }
    struct call value = *active;
    remove_key(&calls, &key);
    capture_return(ctx, direction, value);
    struct owner_key owner = { .ssl = value.ssl, .generation = value.generation,
        .pid = key.thread >> 32, .direction = direction };
    remove_key(&owners, &owner);
    return 0;
}
SEC("uprobe/ssl_write_enter") int ssl_write_enter(struct regs *ctx) { return enter(ctx, 2, 0); }
SEC("uretprobe/ssl_write_leave") int ssl_write_leave(struct regs *ctx) { return leave(ctx, 2); }
SEC("uprobe/ssl_read_enter") int ssl_read_enter(struct regs *ctx) { return enter(ctx, 3, 0); }
SEC("uretprobe/ssl_read_leave") int ssl_read_leave(struct regs *ctx) { return leave(ctx, 3); }
SEC("uprobe/ssl_write_ex_enter") int ssl_write_ex_enter(struct regs *ctx) { return enter(ctx, 2, 1); }
SEC("uprobe/ssl_read_ex_enter") int ssl_read_ex_enter(struct regs *ctx) { return enter(ctx, 3, 1); }
SEC("uprobe/ssl_read_early_enter") int ssl_read_early_enter(struct regs *ctx) { return enter(ctx, 3, 2); }
SEC("uprobe/ssl_handshake_enter") int ssl_handshake_enter(struct regs *ctx) { return enter(ctx, 0, 0); }
SEC("uretprobe/ssl_handshake_leave") int ssl_handshake_leave(struct regs *ctx) {
    struct call_key key = { .thread = pid_tid(), .direction = 0 };
    struct call *active = lookup(&calls, &key);
    if (!active) return 0;
    if (active->depth > 1) { active->depth--; return 0; }
    struct owner_key owner = { .ssl = active->ssl, .generation = active->generation,
        .pid = key.thread >> 32, .direction = 0 };
    remove_key(&calls, &key);
    remove_key(&owners, &owner);
    return 0;
}

#include "gotls.h"
#include "sockets.h"
char bbox_license[] SEC("license") = "GPL";
