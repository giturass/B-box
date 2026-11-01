// SPDX-License-Identifier: GPL-3.0-or-later
// Optional CO-RE program. All kernel field offsets are relocated from this
// device's BTF. It is loaded separately so missing BTF cannot disable TLS I/O.
#define CORE __attribute__((preserve_access_index))
struct fdtable { u32 max_fds; struct file **fd; } CORE;
struct files_struct { struct fdtable *fdt; } CORE;
struct task_struct { struct files_struct *files; } CORE;
struct inode { u16 i_mode; unsigned long i_ino; } CORE;
struct file { struct inode *f_inode; void *private_data; } CORE;
struct socket { short type; struct sock *sk; } CORE;
struct ns_common { u32 inum; } CORE;
struct net { struct ns_common ns; } CORE;
struct possible_net { struct net *net; } CORE;
struct in6_addr { u8 bytes[16]; };
struct sock_common {
    u32 skc_daddr, skc_rcv_saddr;
    u16 skc_dport, skc_num, skc_family;
    struct in6_addr skc_v6_daddr, skc_v6_rcv_saddr;
    struct possible_net skc_net;
} CORE;
struct sock { struct sock_common __sk_common; } CORE;
struct raw_syscall { u64 regs, number; };
struct socket_data { u64 inode; u8 source[16], destination[16]; u16 source_port, destination_port, family, pad; u32 netns, reserved; };
_Static_assert(sizeof(struct socket_data) == 56, "socket event ABI");
#define KREAD(dst, src) read_kernel(&(dst), sizeof(dst), &(src))

INLINE int socket_syscall(u64 number) {
#if defined(__TARGET_ARCH_arm64)
    return number == 63 || number == 64 || number == 65 || number == 66 ||
           number == 206 || number == 207 || number == 211 || number == 212 || number == 242 || number == 269;
#else
    return number == 0 || number == 1 || number == 19 || number == 20 ||
           number == 44 || number == 45 || number == 46 || number == 47 || number == 299 || number == 307;
#endif
}

SEC("raw_tracepoint/sys_enter") int socket_enter(struct raw_syscall *ctx) {
    if (!selected() || !socket_syscall(ctx->number)) return 0;
    struct regs *regs = (void *)ctx->regs;
    u64 argument = 0, goroutine = 0;
    if (read_kernel(&argument, sizeof(argument), &ARG1(regs)) ||
        read_kernel(&goroutine, sizeof(goroutine), &GO_G(regs))) return 0;
    s32 fd = (s32)argument;
    if (fd < 0) return 0;
    u64 ssl = 0, generation = 0;
    struct call_key call_key = { .thread = pid_tid() };
#pragma unroll
    for (u32 direction = 0; direction < 4; direction++) {
        call_key.direction = direction;
        struct call *call = lookup(&calls, &call_key);
        if (call) {
            if (ssl && (ssl != call->ssl || generation != call->generation)) return 0;
            ssl = call->ssl; generation = call->generation;
        }
    }
    if (goroutine) {
        struct go_key key = { .g = goroutine, .pid = pid_tid() >> 32 };
        struct go_handshake *handshake = lookup(&go_handshakes, &key);
        if (handshake) {
            if (ssl && (ssl != handshake->ssl || generation != handshake->generation)) return 0;
            ssl = handshake->ssl; generation = handshake->generation;
        }
#pragma unroll
        for (u32 direction = 2; direction <= 3; direction++) {
            key.direction = direction;
            struct go_call *call = lookup(&go_calls, &key);
            if (call && call->call.generation) {
                if (ssl && (ssl != call->call.ssl || generation != call->call.generation)) return 0;
                ssl = call->call.ssl; generation = call->call.generation;
            }
        }
    }
    if (!ssl || !generation) return 0;
    struct connection_key key = key_for(ssl);
    struct connection *connection = lookup(&connections, &key);
    if (!connection || connection->generation != generation) return 0;
    struct task_struct *task = current_task();
    struct files_struct *files = 0;
    struct fdtable *table = 0;
    struct file **fds = 0, *file = 0;
    u32 max_fds = 0;
    if (KREAD(files, task->files) || !files || KREAD(table, files->fdt) || !table ||
        KREAD(max_fds, table->max_fds) || (u32)fd >= max_fds || KREAD(fds, table->fd) || !fds ||
        read_kernel(&file, sizeof(file), &fds[fd]) || !file) return 0;
    struct inode *inode = 0;
    u16 mode = 0;
    struct socket *socket = 0;
    struct sock *sock = 0;
    short type = 0;
    if (KREAD(inode, file->f_inode) || !inode || KREAD(mode, inode->i_mode) || (mode & 0170000) != 0140000 ||
        KREAD(socket, file->private_data) || !socket || KREAD(type, socket->type) || type != 1 ||
        KREAD(sock, socket->sk) || !sock) return 0;
    struct socket_data data = {};
    struct net *net = 0;
    if (KREAD(data.inode, inode->i_ino) || !data.inode ||
        KREAD(data.family, sock->__sk_common.skc_family) ||
        KREAD(data.source_port, sock->__sk_common.skc_num) ||
        KREAD(data.destination_port, sock->__sk_common.skc_dport) ||
        !data.source_port || !data.destination_port ||
        KREAD(net, sock->__sk_common.skc_net.net) || !net || KREAD(data.netns, net->ns.inum)) return 0;
    if (data.family == 2) {
        if (read_kernel(data.source, 4, &sock->__sk_common.skc_rcv_saddr) ||
            read_kernel(data.destination, 4, &sock->__sk_common.skc_daddr)) return 0;
    } else if (data.family == 10) {
        if (read_kernel(data.source, 16, &sock->__sk_common.skc_v6_rcv_saddr) ||
            read_kernel(data.destination, 16, &sock->__sk_common.skc_v6_daddr)) return 0;
    } else return 0;
    data.destination_port = __builtin_bswap16(data.destination_port);
    struct session_key session = session_for(ssl, generation);
    struct socket_identity *previous = lookup(&socket_seen, &session);
    if (previous && previous->conflict) return 0;
    if (previous && previous->inode == data.inode && previous->socket == (u64)sock && previous->netns == data.netns) return 0;
    struct socket_identity identity = { .inode = data.inode, .socket = (u64)sock, .netns = data.netns };
    u32 conflict = 0;
    if (!previous && update(&socket_seen, &session, &identity, NOEXIST)) {
        // The other direction may have won the insertion on another CPU.
        // Recheck its identity instead of silently accepting that race.
        previous = lookup(&socket_seen, &session);
        if (!previous) { loss(); return 0; }
        if (previous->conflict || (previous->inode == data.inode &&
            previous->socket == (u64)sock && previous->netns == data.netns)) return 0;
    }
    if (previous) { previous->conflict = 1; conflict = 1; }
    struct event *event = prepare(ssl, connection, 6);
    if (!event) return 0;
    event->fd = fd;
    event->flags = conflict;
    event->original_len = event->captured_len = sizeof(data);
    __builtin_memcpy(event->data, &data, sizeof(data));
    emit(ctx, event, sizeof(data));
    return 0;
}
