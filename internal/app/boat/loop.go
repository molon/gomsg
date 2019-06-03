package boat

import (
	"context"
	"io"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"

	"github.com/molon/gomsg/internal/pb/stationpb"
	"github.com/molon/gomsg/pb/msgpb"
	"github.com/molon/pkg/errors"
)

func NewLoopServer(ctx context.Context, opts ...grpc.ServerOption) (*grpc.Server, io.Closer, error) {
	opts = append(opts,
		grpc.UnaryInterceptor(
			grpc_middleware.ChainUnaryServer(
				// 打印日志
				grpc_logrus.UnaryServerInterceptor(plog),
				// 只是用来褪去堆栈信息
				errors.UnaryServerInterceptor(),
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
		),
		grpc.StreamInterceptor(
			grpc_middleware.ChainStreamServer(
				// 打印日志
				grpc_logrus.StreamServerInterceptor(plog),
				// 只是用来褪去堆栈信息
				errors.StreamServerInterceptor(),
				// errors.StreamServerInterceptor(errors.WithErrorHandler(func(err error) error {
				// 	if err != nil {
				// 		// TEST: 为了测试才打印出来
				// 		plog.Errorf("loop errors:\n%+v", err)
				// 	}

				// 	return err
				// })),
				// recovery住，返回带有堆栈信息的错误
				grpc_recovery.StreamServerInterceptor(
					grpc_recovery.WithRecoveryHandler(
						func(p interface{}) error {
							err := errors.Statusf(codes.Internal, "%s", p)
							plog.Errorf("panic\n%+v", err)
							return err
						},
					),
				),
			),
		),
	)

	s := grpc.NewServer(opts...)

	ctx, cancel := context.WithCancel(ctx)
	ls := &loopServer{
		ctx:    ctx,
		cancel: cancel,
	}

	msgpb.RegisterMsgServer(s, ls)

	return s, ls, nil
}

type loopServer struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (s *loopServer) Close() error {
	// 这里是通知所有loop赶紧disconnect
	s.cancel()

	// 原则上在程序关闭之前一定要Disconnect所有session的话，就要Wait，同时要注意不能有新连接进入
	// 一般在执行此方法之前服务注册也已经取消了，部署环境要配合服务注册来支持隔离外界连接
	s.wg.Wait()
	return nil
}

// 消息传递通道
func (s *loopServer) LoopV1(stream msgpb.Msg_LoopV1Server) error {
	s.wg.Add(1)

	ctx, cancel := context.WithCancel(s.ctx)
	defer func() {
		cancel()
		s.wg.Done()
	}()

	// 服务内会话管理
	sess := global.sessionStore.NewSession()
	sid := sess.sid
	plog.Debugf("New session: %s", sid)
	defer func() {
		global.sessionStore.Delete(sid)
		plog.Debugf("Delete session: %s", sid)
	}()

	// 登记会话，要传出stream.Context()以便于传递鉴权信息等
	out, err := global.stationCli.Connect(stream.Context(), &stationpb.ConnectRequest{
		BoatId: global.applicationId,
		Sid:    sid,
	})
	if err != nil {
		return errors.WithStack(err)
	}
	defer func(uid string) {
		// 删除会话登记，因为很可能是因为ctx触发的，但是disconnect是一定要执行的，所以这里不用ctx
		_, err := global.stationCli.Disconnect(context.Background(), &stationpb.DisconnectRequest{
			BoatId: global.applicationId,
			Sid:    sid,
			Uid:    uid,
		})
		if err != nil {
			plog.Errorf("Disconnect failed: %v", err)
		}
	}(out.GetUid())

	// 更新会话详细信息以备用
	sess.Update(out.GetUid(), out.GetPlatform())

	// 执行最终loop
	return grpcLoop(ctx, sess, stream)
}

func grpcLoop(ctx context.Context, sess *Session, stream msgpb.Msg_LoopV1Server) error {
	defer close(sess.doneC)

	// recv loop
	recvClosedC := make(chan struct{}, 1)
	go func() {
		defer close(recvClosedC)

		for {
			select {
			// 一般也不会走到这，写这两句只是顺便
			case <-sess.doneC:
				return
			case <-sess.kickoutC:
				return
			default:
				m, err := stream.Recv()
				if err == io.EOF {
					// 如果客户端CloseSend()的话会触发，以此为正常结束stream的依据
					return
				}
				if err != nil {
					// 客户端的ctx的结束也会通过此处反馈
					sess.writeLoopError(errors.WithStack(err))
					return
				}

				if m.Body == nil {
					sess.writeLoopError(errors.Statusf(codes.Internal, "uknown error"))
					return
				}

				// 心跳反馈，本来gRPC自带keepalive机制的
				// 但是由于dart库不支持，所以自己实现简单的反馈吧
				if _, ok := m.Body.(*msgpb.ClientPayload_Ping); ok {
					sess.sendC <- &msgpb.ServerPayload{
						Body: &msgpb.ServerPayload_Pong{
							Pong: &msgpb.Pong{},
						},
					}

					continue
				}

				if err := sess.recv(m); err != nil {
					sess.writeLoopError(err)
					return
				}
			}
		}
	}()

	// wait send or dying
	for {
		select {
		// 发消息
		case m := <-sess.sendC:
			stream.Send(m)
		// recv loop关闭后
		case <-recvClosedC:
			return sess.loopError()
		// 外部主动关闭loop
		case <-sess.kickoutC:
			return sess.loopError()
		// stream流ctx完毕
		// 不需要再写这个，在recv loop里也会捕获到同样的错误，且被grpc官方包内部包裹了Status的
		// case <-stream.Context().Done():
		// 	sess.writeLoopError(errors.Wrap(stream.Context().Err()))
		// 	return sess.loopError()
		// 全局ctx完毕
		case <-ctx.Done():
			// 这个在程序关闭时触发，不应该算错
			sess.writeLoopError(nil)
			// sess.writeLoopError(errors.Wrap(ctx.Err()))
			return sess.loopError()
		}
	}
}
