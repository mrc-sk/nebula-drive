package controllers

import (
	"net/http"
	"testing"
	"time"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
	"golang.org/x/crypto/bcrypt"
)

// seedSessions 为用户建 n 个会话，返回 (userID, 明文密码)
func seedSessions(t *testing.T, name string, n int) (*models.User, string) {
	t.Helper()
	pwd := "OldPass123"
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u := makeUser(t, name, false)
	u.Password = string(hash)
	if err := db.Get().Save(u).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		sid := "sess-" + name + "-" + itoa(uint(i))
		if err := db.Get().Create(&models.Session{
			UserID: u.ID, Token: "tok-" + sid, SessionID: sid,
			ExpiresAt: time.Now().Add(time.Hour),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return u, pwd
}

func sessionCount(t *testing.T, userID uint) int64 {
	t.Helper()
	var n int64
	db.Get().Model(&models.Session{}).Where("user_id = ?", userID).Count(&n)
	return n
}

// 改密码后，攻击者手上的旧 session 必须被吊销。
//
// 这个测试是本轮修复的核心验证。若只断言「密码改成��了」而不查 session，
// 那么即使删掉整个吊销逻辑测试照样通过 —— 漏洞仍在。
func TestChangePasswordRevokesAllSessions(t *testing.T) {
	testutil.SetupDB(t)
	u, oldPwd := seedSessions(t, "victim", 3)

	r := userRouter(u)
	r.POST("/auth/change-password", ChangePassword)
	w := doJSON(r, http.MethodPost, "/auth/change-password", map[string]any{
		"old": oldPwd, "new": "NewPass456",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	// 旧会话必须全部失效……
	if n := sessionCount(t, u.ID); n != 1 {
		t.Fatalf("改密码后应只剩 1 个新会话（当前浏览器），实际 %d 个 —— 旧会话未吊销", n)
	}
	// ……且剩下那个不能是任何一个旧的
	var s models.Session
	db.Get().Where("user_id = ?", u.ID).First(&s)
	if s.SessionID == "sess-victim-0" || s.SessionID == "sess-victim-1" || s.SessionID == "sess-victim-2" {
		t.Fatalf("残留的仍是旧会话: %s", s.SessionID)
	}
}

// 改密码必须写审计日志（这类操作要能追溯）
func TestChangePasswordWritesAuditLog(t *testing.T) {
	testutil.SetupDB(t)
	u, oldPwd := seedSessions(t, "audited", 1)

	r := userRouter(u)
	r.POST("/auth/change-password", ChangePassword)
	w := doJSON(r, http.MethodPost, "/auth/change-password", map[string]any{
		"old": oldPwd, "new": "NewPass456",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var n int64
	db.Get().Model(&models.AuditLog{}).Where("action = ?", "change_password").Count(&n)
	if n == 0 {
		t.Fatal("改密码未写审计日志")
	}
}

// 原密码错误时不得吊销会话（否则可被用来强制踢人下线）
func TestChangePasswordWrongOldKeepsSessions(t *testing.T) {
	testutil.SetupDB(t)
	u, _ := seedSessions(t, "wrongold", 2)
	before := sessionCount(t, u.ID)

	r := userRouter(u)
	r.POST("/auth/change-password", ChangePassword)
	w := doJSON(r, http.MethodPost, "/auth/change-password", map[string]any{
		"old": "NotThePassword1", "new": "NewPass456",
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if after := sessionCount(t, u.ID); after != before {
		t.Fatalf("原密码错误却改了会话数：%d → %d", before, after)
	}
}

// 新密码不达标时不得吊销会话
func TestChangePasswordWeakNewKeepsSessions(t *testing.T) {
	testutil.SetupDB(t)
	u, oldPwd := seedSessions(t, "weaknew", 2)
	before := sessionCount(t, u.ID)

	r := userRouter(u)
	r.POST("/auth/change-password", ChangePassword)
	w := doJSON(r, http.MethodPost, "/auth/change-password", map[string]any{
		"old": oldPwd, "new": "abc", // 太短
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if after := sessionCount(t, u.ID); after != before {
		t.Fatalf("密码校验失败却吊销了会话：%d → %d", before, after)
	}
}

// CreateUser 必须走与注册/改密码同一套密码策略，
// 否则管理端能创建 "123" 这种弱密码账号。
func TestCreateUserEnforcesPasswordPolicy(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "poladmin", true)
	r := userRouter(admin)
	r.POST("/users", CreateUser)

	// 太短
	w := doJSON(r, http.MethodPost, "/users", map[string]any{
		"userName": "weak1", "password": "ab1",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("弱密码 status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	// 无大写
	w2 := doJSON(r, http.MethodPost, "/users", map[string]any{
		"userName": "weak2", "password": "abcdefg1",
	})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("无大写 status = %d, want 400", w2.Code)
	}
	// 无数字
	w3 := doJSON(r, http.MethodPost, "/users", map[string]any{
		"userName": "weak3", "password": "Abcdefgh",
	})
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("无数字 status = %d, want 400", w3.Code)
	}
	// 达标密码应成功
	w4 := doJSON(r, http.MethodPost, "/users", map[string]any{
		"userName": "strong1", "password": "Str0ngPass",
	})
	if w4.Code != http.StatusOK {
		t.Fatalf("达标密码 status = %d body=%s", w4.Code, w4.Body.String())
	}
	// 被拒的账号不应落库
	for _, n := range []string{"weak1", "weak2", "weak3"} {
		var c int64
		db.Get().Model(&models.User{}).Where("user_name = ?", n).Count(&c)
		if c != 0 {
			t.Fatalf("%s 不该被创建", n)
		}
	}
}
