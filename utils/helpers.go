package utils

import (
	"github.com/adonese/noebs/dashboard"
	"github.com/go-redis/redis"
	"github.com/jinzhu/gorm"
)

// GetRedis returns a *redis.Client instance
func GetRedis() *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})
	return client
}

func SaveRedisList(r *redis.Client, key string, value interface{}) error {
	_, err := r.LPush(key, value).Result()
	return err

}

func GetOrDefault(keys map[string]interface{}, key, def string) (string, bool) {
	value, ok := keys[key]
	if !ok {
		return def, ok
	}
	return value.(string), ok
}

func Database(dialect string, fname string) *gorm.DB {
	db, err := gorm.Open(dialect, fname)
	if err != nil {
	}

	db.AutoMigrate(&dashboard.Transaction{})
	return db
}

func PanfromMobile(username string, r *redis.Client) (string, bool) {
	c, err := r.HGet(username, "main_card").Result()
	if err == nil {
		return c, true
	} else {
		c, err := r.LRange(username+":pans", 0, 0).Result()
		if err == nil {
			return c[0], true
		}
	}
	return "", false
}
