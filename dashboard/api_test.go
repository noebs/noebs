package dashboard

import (
	"testing"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

func TestService_calculateOffset(t *testing.T) {
	type fields struct {
		Redis *redis.Client
		Db    *gorm.DB
	}
	type args struct {
		page     int
		pageSize int
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   uint
	}{
		{"calculateOffset", fields{}, args{page: 0, pageSize: 10}, 0},
		{"calculateOffset", fields{}, args{page: 1, pageSize: 10}, 0},
		{"calculateOffset", fields{}, args{page: 2, pageSize: 10}, 10},
		{"calculateOffset", fields{}, args{page: 3, pageSize: 10}, 20},
		{"calculateOffset", fields{}, args{page: 4, pageSize: 10}, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Service{
				Redis: tt.fields.Redis,
				Db:    tt.fields.Db,
			}
			if got := s.calculateOffset(tt.args.page, tt.args.pageSize); got != tt.want {
				t.Errorf("Service.calculateOffset() = %v, want %v", got, tt.want)
			}
		})
	}
}
