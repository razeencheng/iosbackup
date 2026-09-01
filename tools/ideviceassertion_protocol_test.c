#include "ideviceassertion_protocol.h"

#include <assert.h>
#include <math.h>
#include <stdio.h>
#include <string.h>

static void assert_string(plist_t dict, const char *key, const char *expected)
{
	plist_t node = plist_dict_get_item(dict, key);
	char *actual = NULL;

	assert(node != NULL);
	assert(plist_get_node_type(node) == PLIST_STRING);
	plist_get_string_val(node, &actual);
	assert(actual != NULL);
	assert(strcmp(actual, expected) == 0);
	plist_mem_free(actual);
}

static void test_complete_request(void)
{
	plist_t request = iosbk_assertion_request("iOS Backup", "mobilebackup2 Wi-Fi backup", 720);
	double timeout = 0;

	assert(request != NULL);
	assert(plist_get_node_type(request) == PLIST_DICT);
	assert_string(request, "CommandKey", IOSBK_ASSERTION_COMMAND_CREATE);
	assert_string(request, "AssertionTypeKey", IOSBK_ASSERTION_TYPE_WIRELESS_SYNC);
	assert_string(request, "AssertionNameKey", "iOS Backup");
	assert_string(request, "AssertionDetailKey", "mobilebackup2 Wi-Fi backup");

	plist_t timeout_node = plist_dict_get_item(request, "AssertionTimeoutKey");
	assert(timeout_node != NULL);
	assert(plist_get_node_type(timeout_node) == PLIST_REAL);
	plist_get_real_val(timeout_node, &timeout);
	assert(fabs(timeout - 720.0) < 0.0001);

	plist_free(request);
}

static void test_optional_detail(void)
{
	plist_t request = iosbk_assertion_request("iOS Backup", NULL, 1);

	assert(request != NULL);
	assert(plist_dict_get_item(request, "AssertionDetailKey") == NULL);
	plist_free(request);
}

static void test_validation(void)
{
	assert(iosbk_assertion_request(NULL, NULL, 1) == NULL);
	assert(iosbk_assertion_request("", NULL, 1) == NULL);
	assert(iosbk_assertion_request("iOS Backup", NULL, 0) == NULL);
	assert(iosbk_assertion_request("iOS Backup", NULL, IOSBK_ASSERTION_TIMEOUT_MAX + 1) == NULL);

	plist_t request = iosbk_assertion_request("iOS Backup", NULL, IOSBK_ASSERTION_TIMEOUT_MAX);
	assert(request != NULL);
	plist_free(request);
}

int main(void)
{
	test_complete_request();
	test_optional_detail();
	test_validation();
	puts("ideviceassertion protocol tests passed");
	return 0;
}
