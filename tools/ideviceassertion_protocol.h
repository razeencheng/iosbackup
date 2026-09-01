#ifndef IOSBK_IDEVICEASSERTION_PROTOCOL_H
#define IOSBK_IDEVICEASSERTION_PROTOCOL_H

#include <plist/plist.h>

#define IOSBK_ASSERTION_SERVICE "com.apple.mobile.assertion_agent"
#define IOSBK_ASSERTION_TYPE_WIRELESS_SYNC "AMDPowerAssertionTypeWirelessSync"
#define IOSBK_ASSERTION_COMMAND_CREATE "CommandCreateAssertion"
#define IOSBK_ASSERTION_TIMEOUT_MAX 1200U

plist_t iosbk_assertion_request(const char *name, const char *detail, unsigned int timeout_seconds);

#endif
