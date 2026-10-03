package controllers

import (
	"net/http"
	"testing"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// 自操作保护：管理员不能取消自己的管理员权限。
// 变异验证：若把 UpdateUser 里的 self-demote 拦截去掉，请求会返回 200 并把
// is_admin 翻成 false，本测试的 got.IsAdmin 断言即告失败——证明守卫真实生效。
func TestUpdateUserSelfDemoteBlocked(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "selfadmin1", true)
	r := userRouter(admin)
	r.PUT("/users/:id", UpdateUser)
	w := doJSON(r, http.MethodPut, "/users/"+itoa(admin.ID), map[string]any{"isAdmin": false})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (self demote blocked), body=%s", w.Code, w.Body.String())
	}
	var got models.User
	db.Get().First(&got, admin.ID)
	if !got.IsAdmin {
		t.Fatal("self demote should have been rejected, but is_admin flipped to false")
	}
}

// 自操作保护：管理员不能封禁/禁用自己（status 非 0 即 auth.go 的"已封禁"）。
// 变异验证：去掉 self-disable 拦截后，status 会被写成 1，got.Status != 0 断言失败。
func TestUpdateUserSelfDisableBlocked(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "selfadmin2", true)
	r := userRouter(admin)
	r.PUT("/users/:id", UpdateUser)
	status := 1
	w := doJSON(r, http.MethodPut, "/users/"+itoa(admin.ID), map[string]any{"status": status})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (self disable blocked), body=%s", w.Code, w.Body.String())
	}
	var got models.User
	db.Get().First(&got, admin.ID)
	if got.Status != 0 {
		t.Fatal("self disable should have been rejected, but status flipped away from 0")
	}
}

// 自操作保护不应误伤：管理员改自己的 nickName 等中性字段应当成功且仍保留管理员身份。
func TestUpdateUserSelfNicknameOK(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "selfadmin3", true)
	r := userRouter(admin)
	r.PUT("/users/:id", UpdateUser)
	w := doJSON(r, http.MethodPut, "/users/"+itoa(admin.ID), map[string]any{"nickName": "StillAdmin"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (self nickname allowed), body=%s", w.Code, w.Body.String())
	}
	var got models.User
	db.Get().First(&got, admin.ID)
	if got.NickName != "StillAdmin" || !got.IsAdmin {
		t.Fatalf("self nickname update failed: %+v", got)
	}
}

// 自操作保护只针对自己：管理员降级"其他"管理员应当成功。
func TestUpdateUserAdminDemotesOtherOK(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "selfadmin4", true)
	other := makeUser(t, "other1", true) // 起始为管理员
	r := userRouter(admin)
	r.PUT("/users/:id", UpdateUser)
	w := doJSON(r, http.MethodPut, "/users/"+itoa(other.ID), map[string]any{"isAdmin": false})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (admin can demote another), body=%s", w.Code, w.Body.String())
	}
	var got models.User
	db.Get().First(&got, other.ID)
	if got.IsAdmin {
		t.Fatal("other admin should have been demoted")
	}
	// 守护者自身不受影响
	var a models.User
	db.Get().First(&a, admin.ID)
	if !a.IsAdmin {
		t.Fatal("demoting another user must not affect the acting admin")
	}
}
