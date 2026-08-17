package utils

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIsAllowedStar(t *testing.T) {
	if !IsAllowed([]string{"*"}, "any-host.example.com") {
		t.Error("bare * should allow any host")
	}
	if !IsAllowed([]string{"localhost:4000", "*"}, "any-host.example.com") {
		t.Error("bare * in list should allow any host")
	}
	if IsAllowed([]string{"localhost:4000"}, "evil.example.com") {
		t.Error("non-star list should not allow unrelated host")
	}
	if IsAllowed(nil, "any-host.example.com") {
		t.Error("empty list should not allow any host")
	}
}

func TestCheckRefererStar(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Referer", "https://any-host.example.com/page")

	if !CheckReferer([]string{"*"}, c) {
		t.Error("CheckReferer with * should allow any referer host")
	}

	c.Request.Header.Set("Referer", "")
	if CheckReferer([]string{"*"}, c) {
		t.Error("empty Referer should still be rejected")
	}
}
