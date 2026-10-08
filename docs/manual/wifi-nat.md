# Advanced troubleshooting: idle NAT connections across subnets

[简体中文](wifi-nat.zh-CN.md) · [Operations manual](../OPERATIONS.md) · [Wi-Fi guide](wifi.md)

## Goal and prerequisites

This page is for administrators who manage their router and understand connection tracking and packet captures. The goal is to determine whether the router is interrupting one cross-subnet backup stream. It is not a reason to change the firewall for every Wi-Fi problem. Complete [ordinary troubleshooting](troubleshooting.md) and a same-version USB comparison first. Without evidence pointing to the router, stop at diagnosis.

The following preserves one diagnosed, validated field case and its rule constraints. All addresses are documentation placeholders. Other routers, fw4/nftables, different rule ordering, or different network topologies cannot use it unchanged. Have a protected router configuration copy and an independent management path before changing anything. Do not change forwarding rules during an active backup.

### Wi-Fi online but backup stays at 0%: inspect idle NAT connections

Discovery, heartbeats, and backup data can use different TCP connections. A visible mDNS device or a working heartbeat does not prove that the backup stream is usable. While the phone scans files and prepares its manifest locally, the backup connection can be idle for several minutes; a temporary 0% alone is not a failure.

In one routed setup using SNAT, USB completed with the same version and backup set, while Wi-Fi received `Connection reset by peer` after the phone finished scanning. An independent read-only query succeeded initially but failed when reusing the same connection after 240 idle seconds. Captures at both ends showed the phone sending an ACK to the original NAT port, the router immediately returning RST, and the NAS receiving neither that phone packet nor the reset. Bypassing flow offload only for this device pair made the identical idle test pass; a complete Wi-Fi incremental backup then succeeded.

This located the failure in that router's software flow-offload path: its idle NAT mapping expired early, preventing reply translation back to the NAS. A missing entry in one conntrack sample is insufficient evidence; combine captures at both ends with a connection-reuse comparison. The 240 seconds describe this test interval, not a universal NAT timeout or a defect in every router.

Diagnosis order:

1. Preserve the original job phase, last activity time, and logs, and confirm authorization for this backup was completed. Do not start by restarting components or deleting pairing records or the backup set.
2. Complete a USB comparison with the same version and backup set, then identify the first phone-side error in the Wi-Fi session. A space-query error following a connection reset does not by itself mean the disk is full.
3. Capture the target connection at both the NAS and router. Distinguish a router-generated RST from a host-generated close forwarded through NAT. Observe conntrack over time and reuse one connection before and after an idle interval.
4. After evidence implicates offload, change only the device pair's offload policy and repeat the same test. A failed backup needs a new session and fresh phone authorization; the old stream cannot resume.

#### Validated OpenWrt fw3 / iptables workaround

This procedure applies to a reviewed fw3 ruleset where existing policies such as MIA still run first, `forwarding_rule` precedes `FLOWOFFLOAD`, and the normal policy already accepts established connections for this pair. Two exact `ESTABLISHED` TCP ACCEPT rules use normal forwarding for the pair while new connections and other devices retain the original rules. Do not apply it unchanged to fw4 / nftables or a different rule order.

Create `/etc/firewall.iosbackup-wifi-nooffload` on the router with the following contents. The example addresses are reserved for documentation: replace them with the actual NAS and phone IPv4 addresses. SNAT relies on connection tracking; preserve any narrowly scoped SNAT rule already verified as necessary.

```sh
#!/bin/sh
NAS_IP=192.0.2.10
PHONE_IP=198.51.100.20
for direction in nas_to_phone phone_to_nas; do
    case "$direction" in
        nas_to_phone) src=$NAS_IP; dst=$PHONE_IP ;;
        phone_to_nas) src=$PHONE_IP; dst=$NAS_IP ;;
    esac
    iptables -w 5 -C forwarding_rule -s "$src/32" -d "$dst/32" -p tcp \
        -m conntrack --ctstate ESTABLISHED \
        -m comment --comment 'iosbackup-wifi-nooffload' -j ACCEPT 2>/dev/null ||
    iptables -w 5 -I forwarding_rule 1 -s "$src/32" -d "$dst/32" -p tcp \
        -m conntrack --ctstate ESTABLISHED \
        -m comment --comment 'iosbackup-wifi-nooffload' -j ACCEPT || exit 1
done
```

Back up `/etc/config/firewall`, add this include, and make the script executable. Running the script installs the rules; the include reloads them during firewall startup or reload. Changing the application Compose file or recreating its container does not persist router rules.

```uci
config include 'iosbackup_wifi_nooffload'
    option path '/etc/firewall.iosbackup-wifi-nooffload'
    option reload '1'
    option enabled '1'
```

After reviewing the addresses and rule order, run the script and inspect `iptables -S forwarding_rule` and `iptables -L forwarding_rule -nv` for both rule placement and hits. Repeated execution must not duplicate the rules. The cost is normal forwarding for this pair's TCP traffic. If the phone address changes, update both rules and the original SNAT rule; a fixed DHCP lease can reduce address drift.

To roll back, delete this include and run `uci commit firewall`, then issue two `iptables -D forwarding_rule ...` commands with the same source/destination addresses, TCP, `ESTABLISHED`, comment, and ACCEPT match. Remove only these rules; replacing the entire firewall configuration could discard later changes.

The field validation checked runtime rules, the saved script, and the include. Router reboot and full firewall reload were not exercised; schedule those separately in a maintenance window if needed. Finally, complete a real Wi-Fi backup and check that the page shows success, the success record was saved to disk, the backup child process exited, and the resources held by the job were released. A 100% display alone is insufficient. No device restore exercise was performed.

The inactivity guard bounds waiting after a lost stream; the router workaround addresses the reproduced transport failure. Increasing timeouts, repeatedly refreshing mDNS, or rebuilding the same application image cannot repair an expired NAT mapping.


## Persistence and removal checks

1. On the router, save a protected copy of the original `/etc/config/firewall`. Record whether an include or script with the same name already exists; compare it before making changes and never overwrite unknown configuration. Give the new script execute permission: `chmod 700 /etc/firewall.iosbackup-wifi-nooffload`.
2. After adding the include above, persist the UCI change with `uci commit firewall`. Executing the script applies rules now, but does not validate startup recovery. Do not reload the entire production firewall simply to check these two rules.
3. Verify rule order/hits, repeat the same idle comparison, and complete a real backup. Record that this device pair no longer uses flow offloading. If the failure persists, remove the workaround instead of expanding it to larger subnets.
4. To remove it, first remove the include added for this change and save UCI; if an entry already existed, undo only your changes. Then delete each rule once using its **exact original** source/destination, TCP, `ESTABLISHED`, comment, and ACCEPT conditions.

   ```sh
   iptables -w 5 -D forwarding_rule -s 192.0.2.10/32 -d 198.51.100.20/32 -p tcp \
       -m conntrack --ctstate ESTABLISHED \
       -m comment --comment 'iosbackup-wifi-nooffload' -j ACCEPT
   iptables -w 5 -D forwarding_rule -s 198.51.100.20/32 -d 192.0.2.10/32 -p tcp \
       -m conntrack --ctstate ESTABLISHED \
       -m comment --comment 'iosbackup-wifi-nooffload' -j ACCEPT
   ```

   Replace these addresses with those used when the rules were added. Move the script aside only after confirming it was created for this change and has no remaining include reference. Preserve the original configuration copy. Check that the matching comment rules are gone and ordinary connectivity works. Do not flush the chain or replace the whole configuration with an old copy.

**Stop conditions:** unknown rule order, an unproven RST source, no reliable management path, or an uncertain removal result. Stop writing and have the network maintainer inspect the configuration. Packet captures include device addresses and potentially personal data; protect them and do not upload raw pcap files.

Next: return to [Wi-Fi validation](wifi.md) and confirm success in a new session. Keep unperformed router-reboot/firewall-reload checks explicitly unverified in your record.
