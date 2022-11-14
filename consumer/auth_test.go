package consumer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	firebase "firebase.google.com/go/v4"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"
	"gorm.io/gorm"
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

func TestService_SendPush(t *testing.T) {
	opt := option.WithCredentialsFile("../firebase-sdk.json")
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		t.FailNow()
	}
	type fields struct {
		Redis       *redis.Client
		Db          *gorm.DB
		NoebsConfig ebs_fields.NoebsConfig
		Logger      *logrus.Logger
		FirebaseApp *firebase.App
		Auth        Auther
	}
	type args struct {
		m pushData
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{"test firebaseff", fields{FirebaseApp: app}, args{m: pushData{To: "dK64gIe5TzOGOqA7y8RcQv:APA91bGR-eX9UEFrKi8XXxjXIr2aPE0tOpMz3DXeYnKnHpZ-XkXDdRQ-ybsWKmXU0681hJWH483kuUgjG3iWr1mXj3RPZc-rhksBojG9MiKmW5ZrHoQJse3I87gvRFYGZVGN70bpiRLx"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{
				Redis:       tt.fields.Redis,
				Db:          tt.fields.Db,
				NoebsConfig: tt.fields.NoebsConfig,
				Logger:      tt.fields.Logger,
				FirebaseApp: tt.fields.FirebaseApp,
				Auth:        tt.fields.Auth,
			}
			s.SendPush(tt.args.m)
		})
	}
}
