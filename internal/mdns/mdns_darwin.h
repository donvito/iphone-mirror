#include <dns_sd.h>
#include <stdint.h>

int mdns_register(uintptr_t handle, const char *name, const char *type, int port,
                  const void *txt, int txtLen, DNSServiceRef *out);
void mdns_deregister(DNSServiceRef ref);
