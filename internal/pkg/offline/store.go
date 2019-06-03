package offline

import (
	"context"

	"github.com/gomodule/redigo/redis"
	"github.com/molon/pkg/errors"
)

type Store struct {
	redisPool *redis.Pool
}

func InitStore(
	ctx context.Context,
	redisPool *redis.Pool,
) (*Store, error) {
	s := &Store{
		redisPool: redisPool,
	}

	if err := s.init(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// 初始化需要用到的lua脚本
func (s *Store) init(ctx context.Context) error {
	conn, err := s.redisPool.GetContext(ctx)
	if err != nil {
		return errors.Wrap(err)
	}
	defer conn.Close()

	if err := writeLua.Load(conn); err != nil {
		return errors.Wrap(err)
	}

	if err := deleteLua.Load(conn); err != nil {
		return errors.Wrap(err)
	}

	if err := cleanLua.Load(conn); err != nil {
		return errors.Wrap(err)
	}

	return nil
}
