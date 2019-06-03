package offline

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/gomodule/redigo/redis"
	"github.com/molon/pkg/errors"
	"github.com/molon/gomsg/pb/msgpb"
)

// 用户在某平台的离线消息映射记录
func upomsKey(uid string, platform string) string {
	return fmt.Sprintf("msg/u:%s/p:%s/oms", uid, platform)
}

// 离线消息内容
func ommKey(seq string) string {
	return fmt.Sprintf("msg/om:%s/m", seq)
}

// 离线消息引用计数
func omnKey(seq string) string {
	return fmt.Sprintf("msg/om:%s/n", seq)
}

func (s *Store) Delete(ctx context.Context, uid string, platform string, seqs []string) error {
	if len(seqs) <= 0 {
		return errors.Wrapf("seqs is empty")
	}

	conn, err := s.redisPool.GetContext(ctx)
	if err != nil {
		return errors.Wrap(err)
	}
	defer conn.Close()

	for _, seq := range seqs {
		if err := deleteLua.SendHash(conn, upomsKey(uid, platform), ommKey(seq), omnKey(seq), seq); err != nil {
			return errors.Wrap(err)
		}
	}

	if err := conn.Flush(); err != nil {
		return errors.Wrap(err)
	}

	for _ = range seqs {
		if _, err := conn.Receive(); err != nil {
			return errors.Wrap(err)
		}
	}

	return nil
}

func (s *Store) Clean(ctx context.Context, uid string, expire time.Duration, platformToMaxOMCount map[string]int) error {
	if len(platformToMaxOMCount) <= 0 {
		return errors.Wrapf("platformToMaxOMCount is empty")
	}

	conn, err := s.redisPool.GetContext(ctx)
	if err != nil {
		return errors.Wrap(err)
	}
	defer conn.Close()

	expTs := time.Now().Add(-expire).Unix()

	for platform, maxOMCount := range platformToMaxOMCount {
		if err := cleanLua.SendHash(conn, upomsKey(uid, platform), expTs, maxOMCount); err != nil {
			return errors.Wrap(err)
		}
	}

	if err := conn.Flush(); err != nil {
		return errors.Wrap(err)
	}

	// 只需要关心执行次数即可
	for _ = range platformToMaxOMCount {
		if _, err := conn.Receive(); err != nil {
			return errors.Wrap(err)
		}
	}

	return nil
}

func (s *Store) Write(ctx context.Context,
	uid string, platform string,
	msg *msgpb.Message,
	sendTime time.Time, expire time.Duration,
) error {
	conn, err := s.redisPool.GetContext(ctx)
	if err != nil {
		return errors.Wrap(err)
	}
	defer conn.Close()

	/*
		KEYS : msg/u:uid1/p:platform1/oms(某用户离线映射记录) msg/om:seq1/m(消息内容) msg/om:seq1/n(消息引用计数)
		ARGV : seq1(消息标识) xxxx(消息内容) ts(消息发出时间) expire(多久过期) expireat(到期时间) expirets(已过期时间戳) max_offline_msg_count(最大离线映射数目)
	*/
	m, err := proto.Marshal(msg)
	if err != nil {
		return errors.Wrap(err)
	}

	seq := msg.GetSeq()
	ts := sendTime.Unix()
	exp := int64(expire / time.Second)
	expAt := ts + exp

	if _, err := writeLua.Do(conn,
		upomsKey(uid, platform), ommKey(seq), omnKey(seq),
		seq, m, ts, exp, expAt); err != nil {
		return errors.Wrap(err)
	}

	return nil
}

// 返回 seq:content deleteFunc error
func (s *Store) Read(ctx context.Context, uid string, platform string, expire time.Duration, readCount int64) ([]*msgpb.Message, func(context.Context) error, error) {
	conn, err := s.redisPool.GetContext(ctx)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}
	defer conn.Close()

	/*
		- 根据当前时间算出未过期消息时间戳 `expirets = now-expire`
		- - `ZRANGEBYSCORE msg/u:uid1/p:platform1/oms expirets +inf LIMIT 0 50` 获取头部50个未过期消息
		- - 执行`MGET msg/om:seq1/m msg/om:seq2/m`拿到所有消息内容
	*/
	expTs := time.Now().Add(-expire).Unix()

	seqs, err := redis.Strings(
		conn.Do("ZRANGEBYSCORE", upomsKey(uid, platform), expTs, "+inf", "LIMIT", "0", readCount),
	)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}

	if len(seqs) <= 0 {
		return nil, nil, nil
	}

	ommKeys := make([]interface{}, len(seqs))
	for i, seq := range seqs {
		ommKeys[i] = ommKey(seq)
	}

	ms, err := redis.ByteSlices(
		conn.Do("MGET", ommKeys...),
	)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}

	msgs := []*msgpb.Message{}
	for _, m := range ms {
		if m != nil {
			pb := &msgpb.Message{}
			if err := proto.Unmarshal(m, pb); err != nil {
				return nil, nil, errors.Wrap(err)
			}

			msgs = append(msgs, pb)
		}
	}

	delete := func(ctx context.Context) error {
		return s.Delete(ctx, uid, platform, seqs)
	}
	return msgs, delete, nil
}
