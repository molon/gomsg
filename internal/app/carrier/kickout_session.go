package carrier

import (
	"context"

	"github.com/molon/gomsg/internal/pb/boatpb"
	"github.com/molon/gomsg/internal/pb/mqpb"
	"github.com/molon/pkg/errors"
	"github.com/molon/gomsg/internal/pkg/sessionstore"
	"github.com/molon/gomsg/pb/errorpb"
)

func kickoutSession(ctx context.Context, in *mqpb.KickoutSession) error {
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

	if _, err := cli.Kickout(ctx, &boatpb.KickoutRequest{
		Sid:  in.GetSid(),
		Code: in.GetCode(),
	}); err != nil {
		if equalErrCode(err, errorpb.Code_SESSION_NOT_FOUND) {
			needClean = true // 会话不存在，清理返回
			return nil
		}

		return errors.Wrap(err)
	}

	return nil
}
