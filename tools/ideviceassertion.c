#define _POSIX_C_SOURCE 200809L

#include "ideviceassertion_protocol.h"

#include <errno.h>
#include <getopt.h>
#include <libimobiledevice/libimobiledevice.h>
#include <libimobiledevice/lockdown.h>
#include <libimobiledevice/property_list_service.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#define TOOL_NAME "ideviceassertion"
#define DEFAULT_ASSERTION_SECONDS 720U
#define REPLY_TIMEOUT_MS 30000U

static volatile sig_atomic_t stop_requested = 0;

static void handle_signal(int signum)
{
	(void)signum;
	stop_requested = 1;
}

static void print_usage(FILE *stream)
{
	fprintf(stream,
		"Usage: %s -n -u UDID [--timeout SECONDS] [--hold SECONDS] [--name NAME] [--detail TEXT]\n"
		"\n"
		"Acquire a bounded AMDPowerAssertionTypeWirelessSync assertion.\n"
		"The assertion is released when this process exits.\n",
		TOOL_NAME);
}

static int parse_seconds(const char *value, unsigned int *result)
{
	char *end = NULL;
	errno = 0;
	unsigned long parsed = strtoul(value, &end, 10);
	if (errno != 0 || end == value || *end != '\0' || parsed == 0 || parsed > IOSBK_ASSERTION_TIMEOUT_MAX) {
		return -1;
	}
	*result = (unsigned int)parsed;
	return 0;
}

static int response_has_error(plist_t response)
{
	if (response == NULL || plist_get_node_type(response) != PLIST_DICT) {
		return 1;
	}

	plist_t error_node = plist_dict_get_item(response, "Error");
	if (error_node == NULL) {
		return 0;
	}

	char *message = NULL;
	if (plist_get_node_type(error_node) == PLIST_STRING) {
		plist_get_string_val(error_node, &message);
	}
	fprintf(stderr, "ASSERTION_REJECTED%s%s\n", message != NULL ? " error=" : "", message != NULL ? message : "");
	if (message != NULL) {
		plist_mem_free(message);
	}
	return 1;
}

static int hold_connection(unsigned int hold_seconds)
{
	struct timespec delay = {.tv_sec = 1, .tv_nsec = 0};
	for (unsigned int elapsed = 0; elapsed < hold_seconds && !stop_requested; elapsed++) {
		struct timespec remaining = delay;
		while (nanosleep(&remaining, &remaining) != 0) {
			if (errno != EINTR) {
				return -1;
			}
			if (stop_requested) {
				return 0;
			}
		}
	}
	return 0;
}

int main(int argc, char **argv)
{
	const char *udid = NULL;
	const char *name = "iOS Backup";
	const char *detail = "mobilebackup2 Wi-Fi backup";
	unsigned int assertion_seconds = DEFAULT_ASSERTION_SECONDS;
	unsigned int hold_seconds = DEFAULT_ASSERTION_SECONDS;
	int use_network = 0;
	int exit_code = 1;

	idevice_t device = NULL;
	lockdownd_client_t lockdown = NULL;
	lockdownd_service_descriptor_t service = NULL;
	property_list_service_client_t assertion = NULL;
	plist_t request = NULL;
	plist_t response = NULL;

	static const struct option long_options[] = {
		{"timeout", required_argument, NULL, 't'},
		{"hold", required_argument, NULL, 'H'},
		{"name", required_argument, NULL, 'N'},
		{"detail", required_argument, NULL, 'D'},
		{"help", no_argument, NULL, 'h'},
		{NULL, 0, NULL, 0},
	};

	int option = 0;
	while ((option = getopt_long(argc, argv, "nu:t:H:N:D:h", long_options, NULL)) != -1) {
		switch (option) {
		case 'n':
			use_network = 1;
			break;
		case 'u':
			udid = optarg;
			break;
		case 't':
			if (parse_seconds(optarg, &assertion_seconds) != 0) {
				fprintf(stderr, "Invalid --timeout: %s (expected 1..%u)\n", optarg, IOSBK_ASSERTION_TIMEOUT_MAX);
				return 2;
			}
			break;
		case 'H':
			if (parse_seconds(optarg, &hold_seconds) != 0) {
				fprintf(stderr, "Invalid --hold: %s (expected 1..%u)\n", optarg, IOSBK_ASSERTION_TIMEOUT_MAX);
				return 2;
			}
			break;
		case 'N':
			name = optarg;
			break;
		case 'D':
			detail = optarg;
			break;
		case 'h':
			print_usage(stdout);
			return 0;
		default:
			print_usage(stderr);
			return 2;
		}
	}

	if (!use_network || udid == NULL || udid[0] == '\0' || optind != argc) {
		fprintf(stderr, "A network UDID is required.\n");
		print_usage(stderr);
		return 2;
	}

	struct sigaction action;
	memset(&action, 0, sizeof(action));
	action.sa_handler = handle_signal;
	sigemptyset(&action.sa_mask);
	if (sigaction(SIGINT, &action, NULL) != 0 || sigaction(SIGTERM, &action, NULL) != 0) {
		perror("sigaction");
		return 1;
	}

	idevice_error_t idevice_result = idevice_new_with_options(&device, udid, IDEVICE_LOOKUP_NETWORK);
	if (idevice_result != IDEVICE_E_SUCCESS) {
		fprintf(stderr, "ASSERTION_FAILED step=device error=%s (%d)\n", idevice_strerror(idevice_result), idevice_result);
		goto cleanup;
	}

	lockdownd_error_t lockdown_result = lockdownd_client_new_with_handshake(device, &lockdown, TOOL_NAME);
	if (lockdown_result != LOCKDOWN_E_SUCCESS) {
		fprintf(stderr, "ASSERTION_FAILED step=lockdown error=%s (%d)\n", lockdownd_strerror(lockdown_result), lockdown_result);
		goto cleanup;
	}

	lockdown_result = lockdownd_start_service(lockdown, IOSBK_ASSERTION_SERVICE, &service);
	if (lockdown_result != LOCKDOWN_E_SUCCESS || service == NULL || service->port == 0) {
		fprintf(stderr, "ASSERTION_FAILED step=start_service error=%s (%d)\n", lockdownd_strerror(lockdown_result), lockdown_result);
		goto cleanup;
	}

	property_list_service_error_t service_result = property_list_service_client_new(device, service, &assertion);
	if (service_result != PROPERTY_LIST_SERVICE_E_SUCCESS) {
		fprintf(stderr, "ASSERTION_FAILED step=connect_service error=%d\n", service_result);
		goto cleanup;
	}

	request = iosbk_assertion_request(name, detail, assertion_seconds);
	if (request == NULL) {
		fprintf(stderr, "ASSERTION_FAILED step=request\n");
		goto cleanup;
	}

	service_result = property_list_service_send_xml_plist(assertion, request);
	if (service_result != PROPERTY_LIST_SERVICE_E_SUCCESS) {
		fprintf(stderr, "ASSERTION_FAILED step=send error=%d\n", service_result);
		goto cleanup;
	}

	service_result = property_list_service_receive_plist_with_timeout(assertion, &response, REPLY_TIMEOUT_MS);
	if (service_result != PROPERTY_LIST_SERVICE_E_SUCCESS || response_has_error(response)) {
		fprintf(stderr, "ASSERTION_FAILED step=receive error=%d\n", service_result);
		goto cleanup;
	}

	printf("ASSERTION_ACQUIRED udid=%s timeout=%u hold=%u\n", udid, assertion_seconds, hold_seconds);
	fflush(stdout);
	if (hold_connection(hold_seconds) != 0) {
		perror("nanosleep");
		goto cleanup;
	}

	exit_code = 0;

cleanup:
	if (response != NULL) {
		plist_free(response);
	}
	if (request != NULL) {
		plist_free(request);
	}
	if (assertion != NULL) {
		property_list_service_client_free(assertion);
	}
	if (service != NULL) {
		lockdownd_service_descriptor_free(service);
	}
	if (lockdown != NULL) {
		lockdownd_client_free(lockdown);
	}
	if (device != NULL) {
		idevice_free(device);
	}
	if (exit_code == 0) {
		puts("ASSERTION_RELEASED");
	}
	return exit_code;
}
