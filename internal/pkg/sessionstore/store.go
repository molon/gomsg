package sessionstore

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gomodule/redigo/redis"
	"github.com/molon/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/*
// 这个结构后续还是可以根据 u:uid1 作为 HashTag 支持集群访问的
"msg/u:uid1/ss": {
    "sid1":"platform1-bid1",
    "sid2":"platform3-bid1",
}
*/

var ErrNoUid = status.Errorf(codes.InvalidArgument, "uid is required")

func ussKey(uid string) string {
	return fmt.Sprintf("msg/u:%s/ss", uid)
}

type Store struct {
	logger    *logrus.Entry
	redisPool *redis.Pool
}

func NewStore(
	logger *logrus.Logger,
	redisPool *redis.Pool,
) *Store {
	ll := logger.WithFields(logrus.Fields{
		"pkg": "sessionstore",
		"mod": "store",
	})

	return &Store{
		logger:    ll,
		redisPool: redisPool,
	}
}

func (ss *Store) SetSession(ctx context.Context, sess Session) error {
	if !sess.Valid() {
		return errors.Wrapf("session is not valid")
	}

	conn, err := ss.redisPool.GetContext(ctx)
	if err != nil {
		return errors.Wrap(err)
	}
	defer conn.Close()

	// 存入即可，奇怪的是 redis 不支持 HSET k hk hv NX 命令
	if _, err := conn.Do("HSETNX", ussKey(sess.Uid), sess.Sid, fmt.Sprintf("%s-%s", sess.Platform, sess.Bid)); err != nil {
		return errors.Wrap(err)
	}

	return nil
}

func (ss *Store) DeleteSessions(ctx context.Context, uid string, sids []string) error {
	if len(uid) < 1 {
		return errors.Wrap(ErrNoUid)
	}

	if len(sids) <= 0 {
		return nil
	}

	// 建立连接
	conn, err := ss.redisPool.GetContext(ctx)
	if err != nil {
		return errors.Wrap(err)
	}
	defer conn.Close()

	// 整理删除语句结构
	dArgs := []interface{}{ussKey(uid)}
	for _, sid := range sids {
		dArgs = append(dArgs, sid)
	}

	if _, err := conn.Do("HDEL", dArgs...); err != nil {
		return errors.Wrap(err)
	}

	return nil
}

// 获取session信息，map 形式 SidToSession
func (ss *Store) GetSidToSession(ctx context.Context, uid string, opts ...GetOption) (map[string]Session, error) {
	if len(uid) < 1 {
		return nil, errors.Wrap(ErrNoUid)
	}

	ops := &getOptions{}
	for _, opt := range opts {
		opt(ops)
	}

	var (
		sidToDetail map[string]string
		err         error
	)

	conn, err := ss.redisPool.GetContext(ctx)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer conn.Close()

	ussKey := ussKey(uid)
	if len(ops.sids) < 1 {
		sidToDetail, err = redis.StringMap(conn.Do("HGETALL", ussKey))
		if err != nil {
			return nil, errors.Wrap(err)
		}
	} else {
		args := []interface{}{ussKey}
		for _, sid := range ops.sids {
			args = append(args, sid)
		}

		details, err := redis.Strings(conn.Do("HMGET", args...))
		if err != nil {
			return nil, errors.Wrap(err)
		}

		sidToDetail = map[string]string{}
		for i, sid := range ops.sids {
			if len(details[i]) > 0 { // 有内容的我们才care
				sidToDetail[sid] = details[i]
			}
		}
	}

	sesses := map[string]Session{}
	if len(sidToDetail) < 1 {
		return sesses, nil
	}

	dArgs := []interface{}{ussKey}
	for sid, detail := range sidToDetail {
		es := strings.Split(detail, "-")
		if len(es) != 2 { // 烂数据就直接忽略且顺便删除
			dArgs = append(dArgs, sid)
			continue
		}

		sesses[sid] = Session{
			Sid:      sid,
			Uid:      uid,
			Platform: es[0],
			Bid:      es[1],
		}
	}

	if len(dArgs) > 1 {
		if _, err := conn.Do("HDEL", dArgs...); err != nil {
			return nil, errors.Wrap(err)
		}
	}

	return sesses, nil
}

// 获取session信息，map 形式 PlatformToSessions
func (ss *Store) GetPlatformToSessions(ctx context.Context, uid string, opts ...GetOption) (map[string][]Session, error) {
	sesses, err := ss.GetSidToSession(ctx, uid, opts...)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	platformToSessions := map[string][]Session{}
	for _, sess := range sesses {
		platformToSessions[sess.Platform] = append(platformToSessions[sess.Platform], sess)
	}

	// 会话列表里按sid倒序，这样是为了让最晚建立的会话成为第一个会话，晚为大
	for platform, _ := range platformToSessions {
		sort.Sort(sort.Reverse(SessionSlice(platformToSessions[platform])))
	}

	return platformToSessions, nil
}

// 获取session信息
func (ss *Store) GetSessions(ctx context.Context, uid string, opts ...GetOption) ([]Session, error) {
	sesses, err := ss.GetSidToSession(ctx, uid, opts...)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	sessions := []Session{}
	for _, sess := range sesses {
		sessions = append(sessions, sess)
	}

	// 会话列表里按sid倒序，这样是为了让最晚建立的会话成为第一个会话，晚为大
	sort.Sort(sort.Reverse(SessionSlice(sessions)))

	return sessions, nil
}

type getOptions struct {
	sids []string
}

type GetOption func(*getOptions)

func WithSids(sids []string) GetOption {
	return func(opts *getOptions) {
		opts.sids = sids
	}
}
