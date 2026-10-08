# 高级排错：跨网段 Wi-Fi 的空闲 NAT 连接

[English](wifi-nat.md) · [返回操作手册](../OPERATIONS.zh-CN.md) · [Wi-Fi 指南](wifi.zh-CN.md)

## 目标与适用条件

本页面向能管理路由器、理解连接跟踪和抓包的管理员。目标是确认某一条跨网段备份数据流是否被路由器中断，而不是为所有 Wi-Fi 问题修改防火墙。先完成[普通排错](troubleshooting.zh-CN.md)和同版本 USB 对照；没有证据指向路由器时停在诊断阶段。

以下保留一次已经定位并验证的现场案例及适用规则。地址全部是文档占位；不同路由器、fw4/nftables、规则顺序或网络拓扑不能直接套用。操作前必须有路由器配置副本和独立管理通道；不要在正在运行的备份中修改转发规则。

### Wi-Fi 在线但备份长期停在 0%：检查空闲 NAT 连接

设备发现、心跳和备份数据可以使用不同的 TCP 连接。mDNS 能找到设备、心跳仍然正常，不能证明备份数据流可用。手机在本机扫描和准备清单时，备份连接可能连续数分钟没有传输；这个阶段短暂的 0% 本身不等于故障。

一次跨网段、使用 SNAT 的现场问题中，同版本与备份集的 USB 备份成功，Wi-Fi 却在手机扫描后收到 `Connection reset by peer`。独立只读查询第一次成功，同一连接空闲 240 秒后复用失败。双端抓包确认：手机向原 NAT 端口发来 ACK，路由器立即回 RST，而 NAS 没收到这次来包。仅绕过这对设备的流量卸载后，相同空闲测试通过，随后一次完整 Wi-Fi 增量备份成功。

这将问题定位到该路由器的软件流量卸载路径：空闲 NAT 映射提前失效，手机的回包无法还原到 NAS。一次连接跟踪查询没有找到记录，还不足以证明映射丢失，需要结合双端抓包与连接复用对照；240 秒是本次测试间隔，不是统一的 NAT 超时标准，也不能将结论推广到所有路由器。

排查顺序：

1. 保存原任务的阶段、最后活动时间和日志，确认本次手机密码授权已完成；不要先重启组件或删除配对与备份集。
2. 用相同版本与备份集完成 USB 对照，再分析 Wi-Fi 会话中手机退出的第一条错误。空间查询失败若发生在连接重置之后，不能直接判断磁盘已满。
3. 在 NAS 和路由器同时抓取目标连接，区分路由器独立产生的 RST 与主机正常关闭后经 NAT 转发的 RST。持续观察连接跟踪记录，并复用同一连接做空闲前后对照。
4. 确认卸载路径有关后，仅改变这对设备的卸载策略，重复相同测试。原任务失败后需要新会话及新的手机授权，不能接着使用旧连接。

#### 已验证的 OpenWrt fw3 / iptables 处理方式

以下方案适用于已经核对规则顺序的 fw3 路由器：原有 MIA 等前置策略仍先执行，`forwarding_rule` 位于 `FLOWOFFLOAD` 之前，正常策略原本就允许这对设备的已建立连接。两条精确的 `ESTABLISHED` TCP ACCEPT 规则让它们走常规转发，新连接和其他设备仍经过原有规则。不要直接套用到 fw4 / nftables 或规则顺序不同的设备。

在路由器新增 `/etc/firewall.iosbackup-wifi-nooffload`，内容如下。示例地址来自文档保留网段，必须分别替换为实际 NAS 和手机 IPv4 地址；SNAT 使用连接跟踪，保留之前已经验证需要的精确 SNAT 规则。

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

先备份 `/etc/config/firewall`，再新增下列 include，并赋予脚本执行权限。脚本用于添加规则；include 用于防火墙启动或重载时重新加载。仅修改应用 Compose 或重建容器不会持久化路由器规则。

```uci
config include 'iosbackup_wifi_nooffload'
    option path '/etc/firewall.iosbackup-wifi-nooffload'
    option reload '1'
    option enabled '1'
```

确认地址和规则顺序后执行脚本，以 `iptables -S forwarding_rule` 和 `iptables -L forwarding_rule -nv` 核验两条规则的位置及命中；重复执行不应叠加。代价是这对设备的 TCP 使用常规转发。手机地址变化时需同步更新两条规则和原 SNAT；可以设置固定 DHCP 租约，减少地址变化。

回滚时删除这个 include 并 `uci commit firewall`，再用相同的源/目的地址、TCP、`ESTABLISHED`、comment 和 ACCEPT 条件执行两次 `iptables -D forwarding_rule ...`，仅移除本次规则。不要覆盖整个防火墙配置，以免丢失后续修改。

现场已核验运行时规则、落盘脚本和 include；未通过重启路由器或全局重载验证启动恢复。若需验证该项，应另选维护窗口。最后需要完成一次真实 Wi-Fi 备份，确认页面显示成功、成功记录已保存到磁盘、备份子进程已退出且任务占用已释放；只看到 100% 不足以确认完成。本次没有执行真机恢复演练。

无活动超时解决断流后的无限等待，路由器规则解决这次复现的断流原因。二者互补；增加超时时间、反复刷新 mDNS 或重建相同应用镜像都不能修复已经失效的 NAT 映射。


## 持久化和撤销时的检查

1. 在路由器上保存原 `/etc/config/firewall` 到受保护副本，记录已有同名 include/脚本是否存在；存在时先比较内容，不覆盖未知配置。给新增脚本执行权限：`chmod 700 /etc/firewall.iosbackup-wifi-nooffload`。
2. 添加上述 include 后，用 `uci commit firewall` 保存 UCI 改动。手工执行脚本只让本次规则生效，不能替代启动恢复验证；不要为了检查这两条规则直接重载整个生产防火墙。
3. 通过规则位置、命中计数、相同空闲对照和一次真实备份验证效果。记录代价是这对设备不再使用流量卸载；若复现不变，按下文撤销，不把规则不断推广到更大网段。
4. 撤销时先移除本次新增的 include 并保存 UCI；如果同名项原本存在，只还原本次修改。再针对两条**原样匹配** 的源/目标、TCP、`ESTABLISHED`、comment 和 ACCEPT 规则，各执行一次删除。

   ```sh
   iptables -w 5 -D forwarding_rule -s 192.0.2.10/32 -d 198.51.100.20/32 -p tcp \
       -m conntrack --ctstate ESTABLISHED \
       -m comment --comment 'iosbackup-wifi-nooffload' -j ACCEPT
   iptables -w 5 -D forwarding_rule -s 198.51.100.20/32 -d 192.0.2.10/32 -p tcp \
       -m conntrack --ctstate ESTABLISHED \
       -m comment --comment 'iosbackup-wifi-nooffload' -j ACCEPT
   ```

   必须换成当初添加规则时使用的实际地址。只有确认脚本是本次新增且已无 include 引用时，才移走该脚本；保留原配置副本。核对指定 comment 的规则已移除及普通网络仍通，不使用整条链清空或整份旧配置覆盖。

**停止条件：** 无法确认规则位置、不能区分 RST 来源、没有可靠管理通道、撤销结果不明确，均应停止写入并请网络维护者检查。抓包含设备地址和可能的个人数据，仅保存在受保护位置，不直接上传原始 pcap。

下一步：回到 [Wi-Fi 验证](wifi.zh-CN.md)，启动新备份并确认成功完成；记录中仍应注明尚未测试路由器重启或全局重载后的效果。
