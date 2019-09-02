package utils

import (
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
