package carrier

import (
	"context"
	"time"

	"github.com/molon/gomsg/pb/errorpb"
	"github.com/molon/pkg/errors"

	"github.com/golang/protobuf/ptypes"
	"github.com/molon/gomsg/internal/pb/boatpb"

	"github.com/molon/gomsg/internal/pb/mqpb"
	"github.com/molon/gomsg/internal/pkg/sessionstore"
)

func sendOfflineToSession(ctx context.Context, in *mqpb.SendOfflineToSession) error {
	sessions, err := global.sstore.GetSidToSession(ctx, in.GetUid(), sessionstore.WithSids([]string{in.GetSid()}))
	if err != nil {
		return err
	}

	sess, ok := sessions[in.GetSid()]
	if !ok {
		return nil
	}

	needClean := false
	defer func() {
		if needClean {
			if err := global.sstore.DeleteSessions(ctx, sess.Uid, []string{sess.Sid}); err != nil {
				plog.Warnf("DeleteSessions failed: %+v", err)
			}
		}
	}()

	cli, ok, err := boatClient(sess.Bid)
	if err != nil {
		return err
	}
	if !ok {
		// 对应boat服务不存在，对应会话也认为不存在了，做下数据清除
		needClean = true
		return nil
	}

	// 开始执行消息下发，直到无离线消息了或者出错就返回
	ackWait := ptypes.DurationProto(global.config.Boat.AckWait)

	// 简单2分钟超时，防止异常后一直绷着，然后等下次重试
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	for {
		msgs, deleteFunc, err := global.offstore.Read(ctx, sess.Uid, sess.Platform, global.config.Offline.Expire, global.config.Offline.BatchCount)
		if err != nil {
			return err
		}

		if len(msgs) < 1 {
			// 下发完毕，执行一下clean返回
			if err := global.offstore.Clean(ctx, sess.Uid, global.config.Offline.Expire, map[string]int{sess.Platform: -1}); err != nil {
				plog.Warnf("Clean failed: %+v", err)
			}

			return nil
		} else {
			if _, err := cli.PushMessages(ctx, &boatpb.PushMessagesRequest{
				Sid:     sess.Sid,
				AckWait: ackWait,
				Msgs:    msgs,
			}); err != nil {
				if equalErrCode(err, errorpb.Code_SESSION_NOT_FOUND) {
					needClean = true // 会话不存在，清理返回
					return nil
				}

				// 返回错误，等待重试
				return errors.Wrap(err)
			}

			// 清理已读取的
			if err := deleteFunc(ctx); err != nil {
				return err
			}
		}
	}
}
