package utils

import (
	//"github.com/adonese/noebs/dashboard"
	"github.com/go-redis/redis"
)

// GetRedis returns a *redis.Client instance
func GetRedis() *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})
	return client
}

//SaveRedisList
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
