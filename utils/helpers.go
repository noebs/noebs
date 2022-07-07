package utils

import (
	"log"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/go-redis/redis/v7"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Service struct {
	Redis *redis.Client
	Db    *gorm.DB
}

// GetRedisClient returns a *redis.Client instance
func GetRedisClient(addr string) *redis.Client {
	if addr == "" {
		if ebs_fields.SecretConfig.RedisPort != "" {
			addr = ebs_fields.SecretConfig.RedisPort
		} else {
			addr = "localhost:6379"
		}

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

func Database(fname string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(fname), &gorm.Config{})
	if err != nil {
		log.Printf("error in opening db: %v", err)
		return nil, err
	}
	return db, nil
}
