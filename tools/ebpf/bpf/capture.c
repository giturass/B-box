// SPDX-License-Identifier: GPL-3.0-or-later
// Public OpenSSL/BoringSSL ABIs only; no private SSL structure offsets.
typedef unsigned char u8;
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
#elif defined(__TARGET_ARCH_x86)
struct regs { u64 r15,r14,r13,r12,bp,bx,r11,r10,r9,r8,ax,cx,dx,si,di,orig_ax,ip,cs,flags,sp,ss; };
#define ARG1(r) ((r)->di)
#define ARG2(r) ((r)->si)
#define ARG3(r) ((r)->dx)
#define ARG4(r) ((r)->cx)
#define RESULT(r) ((r)->ax)
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

struct connection_key { u64 ssl; u32 pid, pad; };
struct connection { u64 generation, sequence; };
struct call_key { u64 thread; u32 direction, pad; };
struct call { u64 ssl, buffer, requested, count_pointer; u32 depth, extended; };
struct event {
    u64 timestamp, ssl, generation, sequence;
    u32 pid, tid, uid, kind;
    s32 fd;
    u32 original_len, offset, captured_len, flags, reserved;
    u8 data[CHUNK];
};
_Static_assert(__builtin_offsetof(struct event, data) == 72, "event ABI");

struct { __uint(type, HASH); __uint(max_entries, 8192);
    __type(key, struct connection_key); __type(value, struct connection); } connections SEC(".maps");
struct { __uint(type, HASH); __uint(max_entries, 8192);
    __type(key, struct call_key); __type(value, struct call); } calls SEC(".maps");
struct { __uint(type, HASH); __uint(max_entries, 256);
    __type(key, u32); __type(value, u8); } uids SEC(".maps");
struct { __uint(type, ARRAY); __uint(max_entries, 3);
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
    u32 *excluded = lookup(&config, &zero);
    if (!excluded || uid == *excluded) return 0;
    u32 *user = lookup(&config, &two);
    if (!user || uid / 100000 != *user) return 0;
    u32 *filtered = lookup(&config, &one);
    return filtered && (!*filtered || lookup(&uids, &uid));
}
INLINE struct connection_key key_for(u64 ssl) {
    struct connection_key key = { .ssl = ssl, .pid = pid_tid() >> 32 };
    return key;
}
INLINE struct event *prepare(u64 ssl, struct connection *connection, u32 kind) {
    u32 zero = 0;
    struct event *event = lookup(&scratch, &zero);
    if (!event) { loss(); return 0; }
    event->timestamp = ktime();
    event->ssl = ssl;
    event->generation = connection->generation;
    event->sequence = __sync_fetch_and_add(&connection->sequence, 1);
    u64 task = pid_tid();
    event->pid = task >> 32; event->tid = (u32)task;
    event->uid = (u32)uid_gid(); event->kind = kind;
    event->fd = -1; event->original_len = 0; event->offset = 0;
    event->captured_len = 0; event->flags = 0; event->reserved = 0;
    return event;
}
INLINE void emit(void *ctx, struct event *event, u32 length) {
    if (output(ctx, &events, CURRENT_CPU, event, 72 + length) < 0) loss();
}
INLINE void create(void *ctx, u64 ssl) {
    if (!ssl || !selected()) return;
    struct connection_key key = key_for(ssl);
    struct connection value = { .generation = ktime() };
    if (update(&connections, &key, &value, ANY)) { loss(); return; }
    struct connection *connection = lookup(&connections, &key);
    if (!connection) { loss(); return; }
    struct event *event = prepare(ssl, connection, 1);
    if (event) emit(ctx, event, 0);
}
INLINE void retire(void *ctx, u64 ssl) {
    struct connection_key key = key_for(ssl);
    struct connection *connection = lookup(&connections, &key);
    if (!connection) return;
    struct event *event = prepare(ssl, connection, 4);
    if (event) emit(ctx, event, 0);
    remove_key(&connections, &key);
}
SEC("uretprobe/ssl_new") int ssl_new(struct regs *ctx) { create(ctx, RESULT(ctx)); return 0; }
SEC("uprobe/ssl_free") int ssl_free(struct regs *ctx) { retire(ctx, ARG1(ctx)); return 0; }
// SSL_clear reuses a public SSL object for another connection.
SEC("uprobe/ssl_clear") int ssl_clear(struct regs *ctx) {
    struct connection_key key = key_for(ARG1(ctx));
    if (lookup(&connections, &key)) { retire(ctx, ARG1(ctx)); create(ctx, ARG1(ctx)); }
    return 0;
}

INLINE int enter(struct regs *ctx, u32 direction, u32 extended) {
    if (!selected()) return 0;
    struct call_key key = { .thread = pid_tid(), .direction = direction };
    struct call *active = lookup(&calls, &key);
    if (active) { active->depth++; return 0; } // _ex wrappers must not duplicate bytes.
    struct connection_key conn_key = key_for(ARG1(ctx));
    if (!lookup(&connections, &conn_key)) return 0; // Connections predating capture are skipped.
    struct call value = { .ssl = ARG1(ctx), .buffer = ARG2(ctx), .requested = ARG3(ctx),
        .count_pointer = ARG4(ctx), .depth = 1, .extended = extended };
    if (update(&calls, &key, &value, ANY)) loss();
    return 0;
}
INLINE int leave(struct regs *ctx, u32 direction) {
    struct call_key key = { .thread = pid_tid(), .direction = direction };
    struct call *active = lookup(&calls, &key);
    if (!active) return 0;
    if (active->depth > 1) { active->depth--; return 0; }
    struct call value = *active;
    remove_key(&calls, &key);
    u64 length = 0;
    int failed = 0;
    if (value.extended) {
        if ((s32)RESULT(ctx) != 1) return 0;
        if (read_user(&length, sizeof(length), (void *)value.count_pointer)) failed = 1;
    } else {
        s32 result = (s32)RESULT(ctx);
        if (result <= 0) return 0;
        length = (u32)result;
    }
    if (!length && !failed) return 0;
    struct connection_key conn_key = key_for(value.ssl);
    struct connection *connection = lookup(&connections, &conn_key);
    if (!connection) { loss(); return 0; }
    struct event *event = prepare(value.ssl, connection, direction);
    if (!event) return 0;
    if (failed || length > value.requested || length > CHUNK * MAX_CHUNKS) {
        event->flags = 1; emit(ctx, event, 0); return 0;
    }
    event->original_len = (u32)length;
#pragma unroll
    for (u32 i = 0; i < MAX_CHUNKS; i++) {
        u32 offset = i * CHUNK;
        if (offset >= length) break;
        u32 count = (u32)length - offset;
        if (count > CHUNK) count = CHUNK;
        if (!count) break;
        event->offset = offset;
        event->captured_len = count;
        if (read_user(event->data, count, (void *)(value.buffer + offset))) {
            event->flags = 1; event->captured_len = 0; emit(ctx, event, 0); break;
        }
        emit(ctx, event, count);
    }
    return 0;
}
SEC("uprobe/ssl_write_enter") int ssl_write_enter(struct regs *ctx) { return enter(ctx, 2, 0); }
SEC("uretprobe/ssl_write_leave") int ssl_write_leave(struct regs *ctx) { return leave(ctx, 2); }
SEC("uprobe/ssl_read_enter") int ssl_read_enter(struct regs *ctx) { return enter(ctx, 3, 0); }
SEC("uretprobe/ssl_read_leave") int ssl_read_leave(struct regs *ctx) { return leave(ctx, 3); }
SEC("uprobe/ssl_write_ex_enter") int ssl_write_ex_enter(struct regs *ctx) { return enter(ctx, 2, 1); }
SEC("uprobe/ssl_read_ex_enter") int ssl_read_ex_enter(struct regs *ctx) { return enter(ctx, 3, 1); }
char license[] SEC("license") = "GPL";

