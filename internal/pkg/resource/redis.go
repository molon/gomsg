package resource

import (
	"fmt"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/molon/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func NewRedisPool(logger *logrus.Logger) *redis.Pool {
	addr := fmt.Sprintf("%s:%d", viper.GetString("redis.address"), viper.GetInt("redis.port"))

	logger.Infof("Init redis pool at %s", addr)

	return &redis.Pool{
		MaxIdle:     1024,
		MaxActive:   10240,
		IdleTimeout: 120 * time.Second,
		Dial: func() (redis.Conn, error) {
			conn, err := redis.Dial(
				"tcp",
				addr,
				redis.DialConnectTimeout(200*time.Millisecond),
				redis.DialReadTimeout(500*time.Millisecond),
				redis.DialWriteTimeout(500*time.Millisecond),
			)
			if err != nil {
				return nil, errors.WithStack(err)
			}
			return conn, nil
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			// 一分钟内使用过就不检测了
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return errors.WithStack(err)
		},
	}
}
