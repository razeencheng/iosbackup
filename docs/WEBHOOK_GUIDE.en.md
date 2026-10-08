# Webhook request and receiver reference

[简体中文](WEBHOOK_GUIDE.md) · [Notification manual](manual/notifications.md)

This describes the current request format and restrictions; it does not deploy a receiver for you. Webhooks send JSON notifications to a service reachable from the container's network. Backup execution does not wait for real-event delivery and is not interrupted by delivery failure.

## Request contract

Supported methods are `POST`, `PUT` and `PATCH`, defaulting to `POST` when empty. The default content type is `application/json`. Any `2xx` response indicates this request was accepted, not that downstream processing completed. The HTTP client has a 10-second overall request timeout; there is no durable queue or guaranteed retry/delivery.

```json
{
  "type": "backup_failed",
  "level": "error",
  "title": "Backup failed",
  "content": "Test device backup failed: no space",
  "device_name": "Test iPhone",
  "device_udid": "EXAMPLE-DEVICE-ID",
  "timestamp": "2026-10-06T12:34:56+08:00",
  "extra_fields": {"reason": "No space"}
}
```

This is a fictional sanitized example. See the [event reference in the notification manual](manual/notifications.md#event-reference) for events. Levels are `info`, `warning` or `error`; system events without a device may omit device fields, and `extra_fields` is optional. Accept additional fields and do not depend on fixed wording of Chinese titles. Body templates change `content` without removing other fields.

## URL, method and header fields

Use the [notification manual](manual/notifications.md) for routine configuration and testing. The current UI has no custom-header editor. Integrations requiring headers need an administrator familiar with authentication/CSRF to manage the configuration API.

This is an API field illustration, not a complete configuration to submit. `POST /api/notifications/config` saves the entire notification configuration. Sending a fragment as a patch may remove other channels or rules:

```json
{
  "webhook_configs": [{
    "name": "backup_receiver",
    "url": "https://notify.example.com/iosbackup",
    "method": "POST",
    "headers": {"Authorization": "Bearer EXAMPLE_RECEIVER_TOKEN"},
    "enabled": true,
    "replace_secret": true
  }],
  "notification_rules": {
    "backup_success": ["Webhook_backup_receiver"],
    "backup_failed": ["Webhook_backup_receiver"]
  }
}
```

Preserve other fields and channels from the complete GET configuration. Empty secret fields preserve stored values when both `replace_secret` and `clear_secret` are `false`. With `replace_secret: true`, supply the URL and complete headers together. Changing only the URL in the UI clears headers previously set through the API; update channels with headers together through the API and test again. Writes require authentication plus an `X-IOSBK-CSRF` header with the current page's `iosbk-csrf` meta value. See the [notification manual](manual/notifications.md#secret-storage-and-api-checks) for read-only checks and response meanings. Never save real credentials, tokens or requests as public examples.

Headers are limited to 20 entries and 8 KiB total for names and values. Values cannot contain CR/LF; `Host`, `Content-Length`, `Connection`, proxy authentication and other hop-by-hop headers are prohibited. URLs cannot contain `user:password@host` credentials.

## Network restrictions and private receivers

Public notification URLs require HTTPS. Private HTTPS destinations still require `IOSBK_WEBHOOK_ALLOW_CIDRS`. HTTP accepts only a literal private IP, not a hostname even if it resolves to a private address. Loopback, unspecified, multicast and link-local addresses remain prohibited regardless of CIDR scope. Resolved connection addresses are checked as well. Self-hosted Bark shares this allowlist; it does not allow private Telegram or WeCom targets.

If your receiver really runs at an address you administer, such as `192.168.50.10`, add a single-host range to the app environment in Compose:

```yaml
services:
  iosbackup:
    environment:
      IOSBK_WEBHOOK_ALLOW_CIDRS: "192.168.50.10/32"
```

Replace this example private address with the actual service IP; prefer HTTPS with a valid certificate. Merge the fragment and recreate the container using [applying configuration](manual/configuration.md). Set this entry directly in Compose's `environment`. The HTTP client does not use `HTTP_PROXY`/`HTTPS_PROXY` environment proxies; routing and firewall rules must permit direct access.

## Minimum receiver behavior and acceptance

1. Provide a dedicated endpoint and validate method, content type, body size and supported event types.
2. Protect transport with HTTPS and validate a random authorization token or an equivalent receiver credential; do not rely only on an obscure URL.
3. Return `2xx` promptly after accepting the event, placing slow work in your own queue. Do not assume exactly-once delivery; deduplicate if necessary.
4. Log only diagnostic fields, hiding authorization headers, full UDIDs, personal file information and URL secrets.
5. Follow the [notification verification steps](manual/notifications.md#check-test-delivery-then-a-real-event) to validate test and real events, checking the actual requests at the receiver as well.

For receiver failures, inspect authentication for `401/403`, direct network reachability for timeouts, and the CIDR for private-target rejection. Do not repeatedly recreate the backup instance to debug a receiver. See the [notification failure branches](manual/notifications.md#when-messages-do-not-arrive) for rules, switches and missing real events.
