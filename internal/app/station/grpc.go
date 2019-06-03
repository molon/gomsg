package station

import (
	"context"

	"google.golang.org/grpc/codes"

	"github.com/molon/gomsg/pb/errorpb"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/molon/gomsg/internal/pb/stationpb"
	"github.com/molon/gomsg/internal/pkg/sessionstore"
	"github.com/molon/pkg/errors"
)

type grpcServer struct{}

// boat服务在新会话进入后应该调用此方法
// 内部根据鉴权信息得到身份信息且记录会话信息，最终返回身份信息
func (s *grpcServer) Connect(ctx context.Context, in *stationpb.ConnectRequest) (*stationpb.ConnectResponse, error) {
	// 尝试auth先
	out, err := global.authCli.Auth(ctx, &empty.Empty{})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if len(out.GetUid()) < 1 {
		return nil, errors.Statusf(codes.Internal, "Auth returns empty uid")
	}

	if len(out.GetPlatform()) < 1 {
		return nil, errors.Statusf(codes.Internal, "Auth returns empty platform")
	}

	// 需要检测是否有同平台登录，若有则需要踢出老的，同平台尽量只有一个会话存在
	platformToSessions, err := global.sstore.GetPlatformToSessions(ctx, out.GetUid())
	if err != nil {
		return nil, err
	}

	if len(platformToSessions[out.GetPlatform()]) > 0 {
		kickSids := []string{}
		for _, sess := range platformToSessions[out.GetPlatform()] {
			kickSids = append(kickSids, sess.Sid)
		}
		// 要通知踢出这些会话
		pubKickoutSessions(out.GetUid(), kickSids, errorpb.Code_NEW_SESSION_ON_SAME_PLATFORM)
	}

	// 记录新会话信息
	if err := global.sstore.SetSession(ctx, sessionstore.Session{
		Bid:      in.GetBoatId(),
		Sid:      in.GetSid(),
		Uid:      out.GetUid(),
		Platform: out.GetPlatform(),
	}); err != nil {
		return nil, err
	}

	// 尝试下发离线消息
	if err := pubSendOfflineToSessions(out.GetUid(), []string{in.GetSid()}); err != nil {
		plog.Warnf("pubSendOfflineToSessions failed: %+v", err)
	}

	return &stationpb.ConnectResponse{
		Uid:      out.Uid,
		Platform: out.Platform,
	}, nil
}

// boat服务在新会话断开之后应该调用此方法
// 内部会删除对应会话信息
func (s *grpcServer) Disconnect(ctx context.Context, in *stationpb.DisconnectRequest) (*empty.Empty, error) {
	if err := global.sstore.DeleteSessions(ctx, in.GetUid(), []string{in.GetSid()}); err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil
}
