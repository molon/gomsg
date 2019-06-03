package boat

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/rs/xid"
	"google.golang.org/grpc"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"

	"github.com/golang/protobuf/ptypes"
	"github.com/molon/gomsg/internal/pb/boatpb"
	"github.com/molon/gomsg/pb/errorpb"
	"github.com/molon/gomsg/pb/msgpb"
	"github.com/molon/pkg/errors"
)

func NewGRPCServer(opts ...grpc.ServerOption) (*grpc.Server, error) {
	opts = append(opts, grpc.UnaryInterceptor(
		grpc_middleware.ChainUnaryServer(
			// 打印日志
			grpc_logrus.UnaryServerInterceptor(plog),
			// 只是用来褪去堆栈信息
			errors.UnaryServerInterceptor(),
			// errors.UnaryServerInterceptor(errors.WithErrorHandler(func(err error) error {
			// 	if err != nil {
			// 		// TEST: 为了测试才打印出来
			// 		plog.Errorf("errors:\n%+v", err)
			// 	}

			// 	return err
			// })),
			// recovery住，返回带有堆栈信息的错误
			grpc_recovery.UnaryServerInterceptor(
				grpc_recovery.WithRecoveryHandler(
					func(p interface{}) error {
						err := errors.Statusf(codes.Internal, "%s", p)
						plog.Errorf("panic\n%+v", err)
						return err
					},
				),
			),
		),
	))

	s := grpc.NewServer(opts...)
	boatpb.RegisterBoatServer(s, &grpcServer{})
	return s, nil
}

type grpcServer struct{}

// 下发消息
func (s *grpcServer) PushMessages(ctx context.Context, in *boatpb.PushMessagesRequest) (*empty.Empty, error) {
	sess := global.sessionStore.Get(in.GetSid())
	if sess == nil {
		return nil, notFoundErr(in.GetSid())
	}

	if len(in.GetMsgs()) <= 0 {
		return &empty.Empty{}, nil
	}

	needAck := false
	for _, m := range in.GetMsgs() {
		if m.GetOptions()&msgpb.MessageOption_NEED_ACK > 0 {
			needAck = true
			break
		}
	}

	sm := &msgpb.ServerPayload{
		Seq:     xid.New().String(),
		NeedAck: needAck,
		Body: &msgpb.ServerPayload_MsgsWrapper{
			MsgsWrapper: &msgpb.MessagesWrapper{
				Msgs: in.GetMsgs(),
			},
		},
	}

	dur, err := ptypes.Duration(in.GetAckWait())
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if err := sess.Send(sm, dur); err != nil {
		return nil, err
	}

	return &empty.Empty{}, nil
}

// 踢出会话
func (s *grpcServer) Kickout(ctx context.Context, in *boatpb.KickoutRequest) (*empty.Empty, error) {
	sess := global.sessionStore.Get(in.GetSid())
	if sess == nil {
		return nil, notFoundErr(in.GetSid())
	}

	// 应该需要先透出一个踢出原因给客户端
	st, _ := status.
		Newf(codes.Internal, in.Code.String()).
		WithDetails(&errorpb.Detail{
			Code: in.Code,
		})
	sess.Kickout(errors.WithStack(st.Err()))

	return &empty.Empty{}, nil
}

// 根据房间名称广播消息
func (s *grpcServer) BoardcastRoom(context.Context, *boatpb.BoardcastRoomRequest) (*empty.Empty, error) {
	return nil, errors.Statusf(codes.Unimplemented, "unimplemented")
}

func notFoundErr(sid string) error {
	st, _ := status.
		Newf(codes.Internal, "No session with id: %s", sid).
		WithDetails(&errorpb.Detail{
			Code: errorpb.Code_SESSION_NOT_FOUND,
		})
	return errors.WithStack(st.Err())
}
