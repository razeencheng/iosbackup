package app

import (
	"strings"
	"testing"
)

func TestNotificationNameInputDoesNotRerenderItsContainer(t *testing.T) {
	data, err := templateFS.ReadFile("templates/notifications.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	start := strings.Index(html, "function setField(")
	end := strings.Index(html[start:], "\nfunction ")
	if start < 0 || end < 0 {
		t.Fatal("未找到 setField 函数")
	}
	setField := html[start : start+end]
	if strings.Contains(setField, "render()") {
		t.Fatalf("名称输入不能调用 render() 替换当前输入框：%s", setField)
	}
	for _, want := range []string{
		"function syncNotifierNameUI",
		"renderRules()",
		`id="channelTitle-`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("通知名称局部更新应包含 %q", want)
		}
	}
}
