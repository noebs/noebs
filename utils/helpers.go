package utils

import (
	//"github.com/adonese/noebs/dashboard"

	"github.com/go-redis/redis/v7"
	"github.com/jinzhu/gorm"
)

type Service struct {
	Redis *redis.Client
	Db    *gorm.DB
}

// GetRedisClient returns a *redis.Client instance
func GetRedisClient(addr string) *redis.Client {
	if addr == "" {
		addr = "100.69.151.58:6379" // TODO #78 read this from env
	}
	client := redis.NewClient(&redis.Options{
		Addr: addr,
		DB:   0,
	})
	return client
}

//SaveRedisList saves to a list in redis
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

func Database(dialect, fname string) (*gorm.DB, error) {
	db, err := gorm.Open(dialect, fname)
	if err != nil {
		return nil, err
	}
	return db, nil
}
