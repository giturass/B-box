// SPDX-License-Identifier: GPL-3.0-or-later
// CI-only public SSL ABI fixture. It performs no cryptography; smoke.py covers
// actual OpenSSL TLS. No runtime relocations are needed when mapped from a ZIP.
#include <stddef.h>
#define API __attribute__((visibility("default"), noinline))

struct fixture_ssl {
    const unsigned char *response;
    size_t response_size, position;
    int clear_result;
    volatile int block, entered;
    int abandon;
    void *jump[5];
};

API void *SSL_new(void *context) { return context; }
API void SSL_free(void *ssl) { __asm__ volatile("" : : "r"(ssl) : "memory"); }
API int SSL_clear(struct fixture_ssl *ssl) {
    if (!ssl->clear_result) return 0;
    ssl->position = 0;
    return 1;
}
API int SSL_write(struct fixture_ssl *ssl, const void *buffer, int length) {
    if (ssl->abandon == 2) {
        // Linux x86_64 SYS_exit_group: exercise process death during an active
        // call without introducing a libc relocation into the mapped fixture.
        __asm__ volatile("syscall" : : "a"(231L), "D"(0L) : "rcx", "r11", "memory");
        __builtin_unreachable();
    }
    if (ssl->abandon == 1) __builtin_longjmp(ssl->jump, 1);
    if (ssl->block && __atomic_exchange_n(&ssl->entered, 1, __ATOMIC_RELAXED) == 0) {
        while (ssl->block) __asm__ volatile("" : : : "memory");
    }
    __asm__ volatile("" : : "r"(buffer) : "memory");
    return length;
}
API int SSL_read(struct fixture_ssl *ssl, unsigned char *buffer, int length) {
    size_t available = ssl->response_size - ssl->position;
    if (length < 0) return -1;
    size_t count = (size_t)length < available ? (size_t)length : available;
    for (size_t i = 0; i < count; i++) buffer[i] = ssl->response[ssl->position + i];
    ssl->position += count;
    return (int)count;
}
API int SSL_write_ex(struct fixture_ssl *ssl, const void *buffer, size_t length, size_t *written) {
    int result = SSL_write(ssl, buffer, (int)length);
    *written = result > 0 ? (size_t)result : 0;
    return result > 0;
}
API int SSL_read_ex(struct fixture_ssl *ssl, void *buffer, size_t length, size_t *readbytes) {
    int result = SSL_read(ssl, buffer, (int)length);
    *readbytes = result > 0 ? (size_t)result : 0;
    return result > 0;
}
API int SSL_write_early_data(struct fixture_ssl *ssl, const void *buffer, size_t length, size_t *written) {
    return SSL_write_ex(ssl, buffer, length, written);
}
API int SSL_read_early_data(struct fixture_ssl *ssl, void *buffer, size_t length, size_t *readbytes) {
    SSL_read_ex(ssl, buffer, length, readbytes);
    return 2; // SSL_READ_EARLY_DATA_FINISH can carry bytes.
}
API int fixture_abandoned_write(struct fixture_ssl *ssl, const void *buffer, int length) {
    if (__builtin_setjmp(ssl->jump) == 0) {
        ssl->abandon = 1;
        SSL_write(ssl, buffer, length);
    }
    ssl->abandon = 0;
    return 1;
}
