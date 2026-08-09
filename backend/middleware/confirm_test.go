package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
	"golang.org/x/crypto/bcrypt"
)

func TestRequireConfirmNoUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireConfirm())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestRequireConfirmCases(t *testing.T) {
	testutil.SetupDB(t)
	gin.SetMode(gin.TestMode)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	u := &models.User{UserName: "u1", Password: string(hash)}
	if err := db.Get().Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	setUser := func(c *gin.Context) { c.Set(CtxUserKey, u) }

	tests := []struct {
		name   string
		header string
		status int
	}{
		{"no header", "", http.StatusForbidden},
		{"wrong pwd", "nope", http.StatusForbidden},
		{"correct pwd", "secret123", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(setUser, RequireConfirm())
			r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("X-Confirm-Password", tt.header)
			}
			r.ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d", w.Code, tt.status)
			}
		})
	}
}
