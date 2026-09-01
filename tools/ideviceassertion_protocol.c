#include "ideviceassertion_protocol.h"

#include <string.h>

plist_t iosbk_assertion_request(const char *name, const char *detail, unsigned int timeout_seconds)
{
	if (name == NULL || name[0] == '\0' || timeout_seconds == 0 || timeout_seconds > IOSBK_ASSERTION_TIMEOUT_MAX) {
		return NULL;
	}

	plist_t request = plist_new_dict();
	if (request == NULL) {
		return NULL;
	}

	plist_dict_set_item(request, "CommandKey", plist_new_string(IOSBK_ASSERTION_COMMAND_CREATE));
	plist_dict_set_item(request, "AssertionTypeKey", plist_new_string(IOSBK_ASSERTION_TYPE_WIRELESS_SYNC));
	plist_dict_set_item(request, "AssertionNameKey", plist_new_string(name));
	plist_dict_set_item(request, "AssertionTimeoutKey", plist_new_real((double)timeout_seconds));
	if (detail != NULL && detail[0] != '\0') {
		plist_dict_set_item(request, "AssertionDetailKey", plist_new_string(detail));
	}

	return request;
}
