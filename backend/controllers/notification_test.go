package controllers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

func TestNotifications(t *testing.T) {
	testutil.SetupDB(t)
	u := &models.User{UserName: "nuser"}
	db.Get().Create(u)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxUserKey, u) })
	r.GET("/n", ListNotifications)
	r.GET("/u", UnreadCount)
	r.PUT("/r/:id", MarkRead)
	r.PUT("/all", MarkAllRead)
	r.DELETE("/n/:id", DeleteNotification)

	// 创建两条通知
	db.Get().Create(&models.Notification{UserID: u.ID, Title: "a", Read: false})
	n2 := models.Notification{UserID: u.ID, Title: "b", Read: false}
	db.Get().Create(&n2)

	// 列表
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/n?page=1&size=10", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}

	// 未读数
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/u", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("unread status = %d", w2.Code)
	}

	// 标记单条已读
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPut, "/r/"+strconv.Itoa(int(n2.ID)), nil)
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("markread status = %d", w3.Code)
	}

	// 全部已读
	w4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPut, "/all", nil)
	r.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("markall status = %d", w4.Code)
	}

	// 删除
	w5 := httptest.NewRecorder()
	req5 := httptest.NewRequest(http.MethodDelete, "/n/"+strconv.Itoa(int(n2.ID)), nil)
	r.ServeHTTP(w5, req5)
	if w5.Code != http.StatusOK {
		t.Fatalf("delete status = %d", w5.Code)
	}
}
