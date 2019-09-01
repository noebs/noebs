package utils

import (
	"github.com/gin-gonic/gin"
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

func GetOrDefault(c *gin.Context, key, def string) (string, bool) {
	value, ok := c.Get(key)
	if !ok {
		return def, ok
	}
	return value.(string), ok
}
