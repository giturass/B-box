// SPDX-License-Identifier: GPL-3.0-or-later
// Go 1.22-1.27 ABIInternal, amd64/arm64. Return probes are ordinary uprobes
// placed on decoded RET instructions, never kernel uretprobes on Go stacks.
// runtime.g begins with stack{lo,hi}; no tls.Conn/net.Conn private offsets.
struct go_key { u64 g; u32 pid, direction; };
struct go_stack { u64 lo, hi; };
struct go_call { struct call call; struct go_stack stack; };
struct go_handshake { u64 ssl, generation, sp; struct go_stack stack; };
HASH_MAP(go_calls, struct go_key, struct go_call, 8192);
HASH_MAP(go_handshakes, struct go_key, struct go_handshake, 8192);

INLINE struct go_key go_key_for(struct regs *ctx, u32 direction) {
    struct go_key key = { .g = GO_G(ctx), .pid = pid_tid() >> 32, .direction = direction };
    return key;
}
INLINE void go_release(struct go_key key, struct call call) {
    struct owner_key owner = { .ssl = call.ssl, .generation = call.generation,
        .pid = key.pid, .direction = key.direction };
    remove_key(&owners, &owner);
}
INLINE int go_same_frame(u64 old_sp, struct go_stack old, u64 sp, struct go_stack current) {
    return old_sp >= old.lo && old_sp < old.hi && sp >= current.lo && sp < current.hi &&
        old.hi - old_sp == current.hi - sp;
}
INLINE u64 go_buffer(u64 buffer, struct go_stack old, struct go_stack current) {
    if (buffer >= old.lo && buffer < old.hi) return current.hi - (old.hi - buffer);
    return buffer;
}
INLINE int go_enter(struct regs *ctx, u32 direction) {
    if (!selected()) return 0;
    struct go_key key = go_key_for(ctx, direction);
    if (!key.g) return 0;
    u64 ssl = GO_ARG1(ctx);
    struct go_stack stack = {};
    if (read_user(&stack, sizeof(stack), (void *)key.g)) return 0;
    struct connection_key conn_key = key_for(ssl);
    struct connection *connection = lookup(&connections, &conn_key);
    struct go_call *previous = lookup(&go_calls, &key);
    if (previous) {
        // Go's stack-split prologue can re-enter the same function before
        // executing its body. Other nested/reused calls cannot be paired.
        if (previous->call.ssl != ssl || previous->call.requested != GO_ARG3(ctx) ||
            (previous->stack.lo == stack.lo && previous->stack.hi == stack.hi) ||
            !go_same_frame(previous->call.stack, previous->stack, ctx->sp, stack) ||
            go_buffer(previous->call.buffer, previous->stack, stack) != GO_ARG2(ctx)) {
            struct connection_key old_key = key_for(previous->call.ssl);
            struct connection *old = lookup(&connections, &old_key);
            if (old && old->generation == previous->call.generation) gap(ctx, old_key.ssl, old, 5);
            if (connection) gap(ctx, ssl, connection, 2);
        }
        go_release(key, previous->call);
        remove_key(&go_calls, &key);
    }
    struct go_call value = { .call = { .ssl = ssl, .buffer = GO_ARG2(ctx),
        .requested = GO_ARG3(ctx), .stack = ctx->sp, .depth = 1 } };
    value.stack = stack;
    if (connection && connection->adapter == 1) {
        value.call.generation = connection->generation;
        struct owner_key owner = { .ssl = ssl, .generation = connection->generation,
            .pid = key.pid, .direction = direction };
        if (update(&owners, &owner, &key.g, NOEXIST)) { gap(ctx, ssl, connection, 2); return 0; }
    }
    // A pre-existing connection has no generation. A genuinely new client
    // handshake below can bind this first Read/Write before its return.
    if (update(&go_calls, &key, &value, ANY)) {
        go_release(key, value.call);
        if (connection) gap(ctx, ssl, connection, 4);
    }
    return 0;
}
INLINE int go_leave(struct regs *ctx, u32 direction) {
    struct go_key key = go_key_for(ctx, direction);
    struct go_call *active = lookup(&go_calls, &key);
    if (!active) return 0;
    struct go_call value = *active;
    remove_key(&go_calls, &key);
    struct go_stack stack = {};
    if (RESULT(ctx) > CHUNK * MAX_CHUNKS && RESULT(ctx) < (1ULL << 63)) {
        struct connection_key conn_key = key_for(value.call.ssl);
        struct connection *connection = lookup(&connections, &conn_key);
        if (connection && connection->generation == value.call.generation) gap(ctx, conn_key.ssl, connection, 1);
    } else if (read_user(&stack, sizeof(stack), (void *)key.g)) {
        struct connection_key conn_key = key_for(value.call.ssl);
        struct connection *connection = lookup(&connections, &conn_key);
        if (connection && connection->generation == value.call.generation) gap(ctx, conn_key.ssl, connection, 3);
    } else if (!go_same_frame(value.call.stack, value.stack, ctx->sp, stack)) {
        struct connection_key conn_key = key_for(value.call.ssl);
        struct connection *connection = lookup(&connections, &conn_key);
        if (connection && connection->generation == value.call.generation) gap(ctx, conn_key.ssl, connection, 5);
    } else {
        // Stack copies preserve distance from stack.hi. Heap buffers are left
        // untouched; the Go runtime keeps them alive for the call.
        value.call.buffer = go_buffer(value.call.buffer, value.stack, stack);
        capture_return(ctx, direction, value.call);
    }
    go_release(key, value.call);
    return 0;
}

SEC("uprobe/go_write_enter") int go_write_enter(struct regs *ctx) { return go_enter(ctx, 2); }
SEC("uprobe/go_write_leave") int go_write_leave(struct regs *ctx) { return go_leave(ctx, 2); }
SEC("uprobe/go_read_enter") int go_read_enter(struct regs *ctx) { return go_enter(ctx, 3); }
SEC("uprobe/go_read_leave") int go_read_leave(struct regs *ctx) { return go_leave(ctx, 3); }
SEC("uprobe/go_close") int go_close(struct regs *ctx) { retire(ctx, GO_ARG1(ctx)); return 0; }

SEC("uprobe/go_handshake_enter") int go_handshake_enter(struct regs *ctx) {
    if (!selected()) return 0;
    struct go_key key = go_key_for(ctx, 0);
    if (!key.g) return 0;
    u64 ssl = GO_ARG1(ctx);
    struct go_stack stack = {};
    if (read_user(&stack, sizeof(stack), (void *)key.g)) return 0;
    struct go_handshake *prior = lookup(&go_handshakes, &key);
    if (prior) {
        if (prior->ssl == ssl && (prior->stack.lo != stack.lo || prior->stack.hi != stack.hi) &&
            go_same_frame(prior->sp, prior->stack, ctx->sp, stack)) {
            prior->sp = ctx->sp;
            prior->stack = stack;
            return 0; // verified stack-split re-entry
        }
        struct connection_key old_key = key_for(prior->ssl);
        struct connection *old = lookup(&connections, &old_key);
        if (old && old->generation == prior->generation) gap(ctx, old_key.ssl, old, 5);
        struct owner_key old_owner = { .ssl = prior->ssl, .generation = prior->generation, .pid = key.pid };
        remove_key(&owners, &old_owner);
    }
    create_connection(ctx, ssl, 1);
    struct connection_key conn_key = key_for(ssl);
    struct connection *connection = lookup(&connections, &conn_key);
    if (!connection) return 0;
    struct go_handshake value = { .ssl = ssl, .generation = connection->generation, .sp = ctx->sp, .stack = stack };
    if (update(&go_handshakes, &key, &value, ANY)) { gap(ctx, ssl, connection, 4); return 0; }
    struct owner_key handshake_owner = { .ssl = ssl, .generation = connection->generation, .pid = key.pid };
    if (update(&owners, &handshake_owner, &key.g, NOEXIST)) gap(ctx, ssl, connection, 2);
#pragma unroll
    for (u32 direction = 2; direction <= 3; direction++) {
        key.direction = direction;
        struct go_call *call = lookup(&go_calls, &key);
        if (call && call->call.ssl == ssl && !call->call.generation) {
            struct owner_key owner = { .ssl = ssl, .generation = connection->generation,
                .pid = key.pid, .direction = direction };
            if (update(&owners, &owner, &key.g, NOEXIST)) gap(ctx, ssl, connection, 2);
            else call->call.generation = connection->generation;
        }
    }
    return 0;
}
SEC("uprobe/go_handshake_leave") int go_handshake_leave(struct regs *ctx) {
    struct go_key key = go_key_for(ctx, 0);
    struct go_handshake *handshake = lookup(&go_handshakes, &key);
    u64 failed_ssl = 0;
    if (handshake) {
        struct go_stack stack = {};
        if (read_user(&stack, sizeof(stack), (void *)key.g) ||
            !go_same_frame(handshake->sp, handshake->stack, ctx->sp, stack)) {
            struct connection_key conn_key = key_for(handshake->ssl);
            struct connection *connection = lookup(&connections, &conn_key);
            if (connection && connection->generation == handshake->generation) gap(ctx, conn_key.ssl, connection, 5);
        }
        struct owner_key owner = { .ssl = handshake->ssl, .generation = handshake->generation, .pid = key.pid };
        remove_key(&owners, &owner);
        // clientHandshake returns an error interface. tls.Dial closes the
        // underlying net.Conn directly on error, without calling tls.Close.
        if (RESULT(ctx)) failed_ssl = handshake->ssl;
    }
    remove_key(&go_handshakes, &key);
    if (failed_ssl) retire(ctx, failed_ssl);
    return 0;
}

SEC("uprobe/go_keylog") int go_keylog(struct regs *ctx) {
    if (!selected()) return 0;
    u32 option = 3;
    u32 *enabled = lookup(&config, &option);
    if (!enabled || !*enabled) return 0;
    struct go_key key = go_key_for(ctx, 0);
    struct go_handshake *handshake = lookup(&go_handshakes, &key);
    if (!handshake) return 0;
    struct connection_key conn_key = key_for(handshake->ssl);
    struct connection *connection = lookup(&connections, &conn_key);
    if (!connection || connection->generation != handshake->generation) return 0;
    u64 label_len = GO_ARG3(ctx), random_len = GO_ARG5(ctx), secret_len = GO_ARG8(ctx);
    if (!label_len || label_len > 64 || random_len != 32 || secret_len < 16 || secret_len > 64) return 0;
    struct event *event = prepare(handshake->ssl, connection, 7);
    if (!event) return 0;
    __builtin_memset(event->data, 0, 164);
    event->data[0] = (u8)label_len;
    event->data[1] = (u8)secret_len;
    if (read_user(event->data + 4, (u32)label_len, (void *)GO_ARG2(ctx)) ||
        read_user(event->data + 68, 32, (void *)GO_ARG4(ctx)) ||
        read_user(event->data + 100, (u32)secret_len, (void *)GO_ARG7(ctx))) return 0;
    event->original_len = event->captured_len = 164;
    emit(ctx, event, 164);
    return 0;
}
