package consumer

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupRouter() *gin.Engine {
	r := gin.Default()
	var service Service

	r.GET("/firebase", service.VerifyFirebase)
	return r
}

func TestService_VerifyFirebase(t *testing.T) {
	router := setupRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/firebase", nil)
	router.ServeHTTP(w, req)
}
