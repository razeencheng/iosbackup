package app

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

func TestDeviceRemovalUIAddsConfirmedFifthTabAndImpactPanel(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	sections := []string{`data-sec="backup"`, `data-sec="backups"`, `data-sec="enc"`, `data-sec="danger"`, `data-sec="remove"`}
	last := -1
	for _, section := range sections {
		pos := strings.Index(markup, section)
		if pos < 0 {
			t.Fatalf("正式首页缺少设备功能页签 %s", section)
		}
		if pos <= last {
			t.Fatalf("设备页签顺序错误，%s 没有放在已确认的位置", section)
		}
		last = pos
	}
	for _, want := range []string{
		`data-sec-panel="remove"`,
		`data-i18n="sec.remove"`,
		`data-i18n="remove.reversible"`,
		`data-i18n="remove.stopTitle"`,
		`data-i18n="remove.hideTitle"`,
		`data-i18n="remove.keepTitle"`,
		`data-i18n="remove.keepBody"`,
		`openRemoveDeviceModal('{{$d.UDID}}',this.dataset.name,this.dataset.model)`,
		`#icon-device-remove`,
		`#icon-pause-circle`,
		`#icon-eye-off`,
		`#icon-archive`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("正式首页缺少移除设备交互契约 %q", want)
		}
	}
	for _, phrase := range []string{
		"不会删除已有备份、配置和配对信息",
		"Existing backups, settings, and pairing information are not deleted",
	} {
		if !strings.Contains(markup, phrase) {
			t.Errorf("移除页必须明确保留数据：缺少 %q", phrase)
		}
	}
}

func TestDeviceRemovalConfirmationDialogHasKeyboardAndFocusContract(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, want := range []string{
		`id="removeDeviceModal"`,
		`role="dialog"`,
		`aria-modal="true"`,
		`aria-labelledby="removeDeviceTitle"`,
		`id="removeDeviceTitle"`,
		`id="removeDeviceConfirm"`,
		`onclick="closeRemoveDeviceModal(event)"`,
		`event.target===event.currentTarget`,
		`function openRemoveDeviceModal(udid, name, model)`,
		`function closeRemoveDeviceModal(event)`,
		`function submitRemoveDevice()`,
		`/api/remove-device/`,
		`removeDeviceReturnFocus.focus()`,
		`if(event.key==='Escape')`,
		`function trapFocusWithin(container, event)`,
		`if(event.key==='Tab')`,
		`trapFocusWithin(removeModal,event)`,
		`tr('remove.busy')`,
		`tr('remove.removing')`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("移除确认框缺少可访问性或请求契约 %q", want)
		}
	}
	if strings.Contains(markup, `class="remove-device-spinner"`) {
		t.Fatal("移除请求期间只应使用清晰文字，不应增加旋转图标")
	}
}

func TestDeviceRemovalSemanticIconsExistAtSharedScale(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	if !strings.Contains(markup, `.removal-icon{width:17px;height:17px`) {
		t.Fatal("移除设备图标应使用统一可读尺寸")
	}
	sprite, err := fs.ReadFile(templateFS, "static/ui-icons.svg")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"icon-device-remove", "icon-archive", "icon-undo", "icon-pause-circle", "icon-eye-off"} {
		if !strings.Contains(string(sprite), `id="`+id+`"`) {
			t.Errorf("共享 SVG 图标库缺少 %s", id)
		}
	}
	for _, emoji := range []string{"🗑", "📦", "⏸", "👁", "↩️", "⚠️"} {
		if strings.Contains(markup, emoji) {
			t.Errorf("正式首页不得用 emoji 充当移除设备图标: %s", emoji)
		}
	}
}

func TestHomePageRendersDeviceRemovalControlsForActiveDevice(t *testing.T) {
	const udid = "REMOVE-UI-ACTIVE"
	app := newDeviceRemovalHTTPTestApp(t, udid)
	rr := performRequest(app.setupRoutes(), http.MethodGet, "/", nil, map[string]string{"Accept": "text/html"})
	if rr.Code != http.StatusOK {
		t.Fatalf("首页应正常渲染，得到 %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`data-sec="remove"`, `data-sec-panel="remove"`, udid, `data-name="家庭 iPhone"`, `data-model="iPhone14,4"`} {
		if !strings.Contains(body, want) {
			t.Errorf("活动设备页面缺少移除控件数据 %q", want)
		}
	}
}

func TestRemovedDevicesDrawerIsLazyAccessibleAndRestorable(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, want := range []string{
		`id="removedDevicesMenuItem"`,
		`onclick="openRemovedDevicesDrawer()"`,
		`id="removedDeviceCountBadge"`,
		`id="removedDevicesDrawer"`,
		`role="dialog"`,
		`aria-modal="true"`,
		`aria-labelledby="removedDevicesTitle"`,
		`id="removedDevicesTitle"`,
		`id="removedDevicesClose"`,
		`id="removedDevicesList"`,
		`id="emptyRemovedDevicesButton"`,
		`function openRemovedDevicesDrawer()`,
		`fetch('/api/removed-devices'`,
		`function closeRemovedDevicesDrawer(event)`,
		`function restoreRemovedDeviceFromDrawer(udid, button)`,
		`/api/restore-device/`,
		`removedDevicesReturnFocus.focus()`,
		`trapFocusWithin(removedDrawer,event)`,
		`removedDevicesRequestSerial`,
		`updateRemovedDeviceCount(snap.removed_device_count||0)`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("已移除设备抽屉缺少交互契约 %q", want)
		}
	}
	if strings.Count(markup, `fetch('/api/removed-devices'`) != 1 {
		t.Fatal("已移除设备列表请求只能出现在打开抽屉的动作中")
	}
}

func TestRemovedDevicesDrawerHasCompleteStatesAndI18n(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, want := range []string{
		`removed: {`,
		`loading:{zh:"正在加载已移除设备…",en:"Loading removed devices…"}`,
		`empty:{zh:"还没有已移除设备。",en:"No devices have been removed yet."}`,
		`loadFailed:{zh:"无法加载已移除设备，请稍后重试。",en:"Removed devices could not be loaded. Please try again."}`,
		`backupKept:{zh:"备份已保留",en:"Backup preserved"}`,
		`restoreAction:{zh:"恢复到设备列表",en:"Restore to device list"}`,
		`restoreFailed:{zh:"恢复失败，请稍后重试。",en:"Restore failed. Please try again."}`,
		`function renderRemovedDevices(devices, state)`,
		`document.createElement('article')`,
		`.textContent=`,
		`renderRemovedDevices(removedDevicesCache,'ready')`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("已移除设备抽屉缺少状态或双语文案 %q", want)
		}
	}
	for _, forbidden := range []string{`device.backup_directory`, `device.network_address`, `device.restore_enabled`} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("抽屉不应读取敏感配置字段 %q", forbidden)
		}
	}
}

func TestRemovedDevicesDrawerKeepsMobileLayoutWithinViewport(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, want := range []string{
		`.removed-devices-sheet{width:min(100%,500px)`,
		`.removed-device-metadata{display:grid;grid-template-columns:repeat(3,minmax(0,1fr))`,
		`.removed-device-metadata{grid-template-columns:1fr}`,
		`.removed-devices-sheet{width:100%;border-left:0}`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("已移除设备抽屉缺少移动端布局约束 %q", want)
		}
	}
}
