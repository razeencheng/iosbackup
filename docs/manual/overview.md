# Before you begin: environment and support boundaries

[简体中文](overview.zh-CN.md) · [Back to the operations manual](../OPERATIONS.md)

## Goal and scope

Before installing, check the host requirements below and choose the matching installation guide. This manual is for a single administrator backing up an iPhone/iPad to a home Linux host or NAS. Complete a USB backup first, then configure scheduling, Wi-Fi, and encryption separately as needed.

iOS Backup runs iOS communication tools on your host and writes backups to your storage. The Web UI controls jobs and displays status. It is not cloud storage and does not keep an off-site copy for you. Whether a backup contains the data you need, whether it is readable, and whether it can be restored require separate checks.

## Prerequisites

| Item | What to confirm |
|---|---|
| Host | A trusted Linux host, or a Synology NAS that supports a compatible Container Manager package. Image build targets are `linux/amd64` and `linux/arm64`; confirm that your specific image contains your architecture. |
| Containers | Docker Engine and a Compose plugin that provides `docker compose`; permission to manage Docker and create persistent directories. See the [Synology guide](synology.md) for its version requirements. |
| Device and connection | An unlockable iPhone/iPad, its passcode, a USB cable supporting data, and access to a USB port on the host running the service. |
| Storage | A writable local filesystem with enough free space and inodes. Allow the device's used capacity plus headroom for the first backup and leave room for the host. Actual backup size can differ. |
| Network | Your browser can reach the host and the image source is accessible. Wi-Fi is not required for initial installation; validate it separately if enabled. |
| Credentials | Safe storage for the administrator password, default `secret_key` (or external master key), and any existing device backup encryption password, including protected offline copies. |
| Image source | Use the official `ghcr.io/razeencheng/iosbackup` image and record its version or digest. See [compose.yaml](../../compose.yaml) for the default reference. |

## Choose a route

1. **General Linux host:** follow [Install, sign in, and protect access](installation.md). Run container commands on the host physically connected to the phone and holding the backups.
2. **Synology DSM:** follow [Install on Synology](synology.md). Check your NAS model, DSM version, and Container Manager version; use a separate deployment directory.
3. **Windows or macOS:** native device-backup service operation is not promised on these platforms. Docker Desktop USB and host-network behavior are outside this tutorial's acceptance scope. These systems can be browser clients or prepare the phone using Apple tools as described in [Wi-Fi backups](wifi.md).
4. **Existing installation:** retain passwords, data directories, all Compose configuration files, and any external master key in use. Follow [Upgrade and roll back](upgrade.md) or [Recover an instance](instance-recovery.md). Do not repeat fresh-instance initialization commands.

## Understand permissions and storage

The default Compose configuration uses a privileged container, host networking, and the host's USB and udev information. A privileged container has extensive host privileges, so both image and host must be trusted. The Web service listens on `0.0.0.0:9000` with the default Compose settings. Host networking uses the host's port directly; there is no separate `ports` mapping. Restrict access sources and prefer an HTTPS reverse proxy or protected tunnel when accessing from another machine. Do not expose the port directly to the public internet.

The installation guide places these relative paths in one **deployment directory** , which may differ from the source directory:

| Host path inside the deployment directory | Container path | Purpose |
|---|---|---|
| `data/backups` | `/backups` | Device backups and optional unpacked output |
| `data/configs` | `/configs` | Device settings, administrator password, authentication/CSRF state, default master key secret_key, encrypted notification secrets and saved backup passwords |
| `data/lockdown` | `/var/lib/lockdown` | Pairing records between this instance and devices |
| `compose.yaml` | None | Image, port, logging, and data mounts; edit ordinary configuration here |

Recovery needs all three complete directories, all Compose configuration files and external credentials in use. A container image, an exported Compose file alone, or only the phone backup directory is not a complete instance copy. See [Back up and recover the instance](instance-recovery.md).

## Use features according to maturity

See [Feature status](../FEATURE_STATUS.md) for the authoritative maturity levels, default gates, and support boundaries. This manual explains prerequisites for each task: complete a USB backup test first, then configure Wi-Fi and encryption as needed. See [Experimental operations](experimental.md) for features that modify or delete data.

Automatic backup means starting a job when its conditions are met. The phone may still request its passcode. Do not assume every backup will run unattended after installation.

## Completion criteria and failure choices

Before installation, identify the host, image and architecture, storage location, permitted Web UI users, and the material needed to recover after losing the host. Resolve any unknown item first; container startup alone does not answer these questions.

If the platform, USB access, or image architecture is incompatible, choose a suitable Linux host. Do not disable authentication or open permissions across the NAS to make the tutorial work. If this host holds your only existing backup, create an independent copy before changing encryption settings or trying experimental features.

The [tutorial images](../images/tutorial/README.md) show Raspberry Pi ARM64 and Synology NAS examples; the image inventory records their sources and validation scope. A successful USB job and an on-disk completion marker do not replace encrypted-manifest reading or device-restoration checks. Validate each path for your devices and deployment environment.

## Next steps

Choose [Linux installation](installation.md) or [Synology installation](synology.md), then complete [your first USB backup](usb-backup.md). Use the [configuration reference](configuration.md) for parameter details and [troubleshooting](troubleshooting.md) for symptoms.
