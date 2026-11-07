package controllers

import (
	"testing"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// 这组测试钉死宿主与邮件插件之间的**字段契约**。
//
// 为什么必须单独测：字段名是两处独立代码之间的约定 ——
// 宿主在 CreateNotification 里发 {"to","subject","body"}，
// 而邮件插件在 examples/plugins/smtp-email（另一个 module）里读
// ctx["to"] / ctx["subject"] / ctx["body"]。
// 任何一边改名都**不会有编译错误**：插件只会静默读不到字段，
// 表现为"装了邮件插件却发不出信"，排查起来极费时间。
//
// 插件那一侧的消费逻辑由 pkg/plugin/host 的 SMTP 端到端测试覆盖，
// 这里负责宿主这一侧。

func TestCreateNotificationFiresEmailHook(t *testing.T) {
	testutil.SetupDB(t)

	u := &models.User{UserName: "mailer", Email: "user@example.com"}
	if err := db.Get().Create(u).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	var got map[string]any
	plugin.Register(plugin.HookEmail, func(ctx map[string]any) (map[string]any, error) {
		got = make(map[string]any, len(ctx))
		for k, v := range ctx {
			got[k] = v
		}
		return nil, nil
	})
	defer plugin.UnregisterAll(plugin.HookEmail)

	CreateNotification(u.ID, "上传完成", "你的文件已就绪", "info", "system")

	if got == nil {
		t.Fatal("创建通知未触发 onEmail —— 邮件外发的唯一入口断了，站内通知永远到不了邮箱")
	}
	if got["to"] != "user@example.com" {
		t.Errorf("to = %v, want %q（插件按这个键名取收件人）", got["to"], "user@example.com")
	}
	if got["subject"] != "上传完成" {
		t.Errorf("subject = %v, want %q", got["subject"], "上传完成")
	}
	if got["body"] != "你的文件已就绪" {
		t.Errorf("body = %v, want %q", got["body"], "你的文件已就绪")
	}
}

// TestCreateNotificationSkipsEmailHookWithoutAddress 用户没填邮箱时不该触发邮件钩子。
//
// 否则插件会拿到空的收件人：轻则白发一次失败日志，重则某些实现
// 把空收件人当成"发给自己"而泄露内容。
func TestCreateNotificationSkipsEmailHookWithoutAddress(t *testing.T) {
	testutil.SetupDB(t)

	u := &models.User{UserName: "nomail"} // 没有 Email
	if err := db.Get().Create(u).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	fired := false
	plugin.Register(plugin.HookEmail, func(ctx map[string]any) (map[string]any, error) {
		fired = true
		return nil, nil
	})
	defer plugin.UnregisterAll(plugin.HookEmail)

	CreateNotification(u.ID, "t", "b", "info", "system")

	if fired {
		t.Error("用户邮箱为空时不应触发 onEmail")
	}
}
