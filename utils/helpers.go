package utils

import "github.com/go-redis/redis"

// GetRedis returns a *redis.Client instance
func GetRedis() *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})
	return client
}
