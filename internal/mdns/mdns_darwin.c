#include "mdns_darwin.h"

#include <arpa/inet.h>
#include <dispatch/dispatch.h>

extern void goMDNSRegistered(uintptr_t handle, int err, char *name);

static dispatch_queue_t queue(void) {
	static dispatch_queue_t q;
	static dispatch_once_t once;
	dispatch_once(&once, ^{ q = dispatch_queue_create("iphonemirror.mdns", DISPATCH_QUEUE_SERIAL); });
	return q;
}

static void registered(DNSServiceRef ref, DNSServiceFlags flags, DNSServiceErrorType err,
                       const char *name, const char *type, const char *domain, void *ctx) {
	goMDNSRegistered((uintptr_t)ctx, err, (char *)name);
}

int mdns_register(uintptr_t handle, const char *name, const char *type, int port,
                  const void *txt, int txtLen, DNSServiceRef *out) {
	__block DNSServiceErrorType err;
	dispatch_sync(queue(), ^{
		err = DNSServiceRegister(out, 0, kDNSServiceInterfaceIndexAny, name, type, NULL, NULL,
		                         htons((uint16_t)port), (uint16_t)txtLen, txt, registered, (void *)handle);
		if (err == kDNSServiceErr_NoError) {
			err = DNSServiceSetDispatchQueue(*out, queue());
			if (err != kDNSServiceErr_NoError) {
				DNSServiceRefDeallocate(*out);
			}
		}
	});
	return err;
}

// The ref must be deallocated on the queue it was scheduled on.
void mdns_deregister(DNSServiceRef ref) {
	dispatch_sync(queue(), ^{ DNSServiceRefDeallocate(ref); });
}
