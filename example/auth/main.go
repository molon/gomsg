package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"

	"github.com/golang/protobuf/ptypes/empty"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	"github.com/molon/gomsg/internal/pkg/resource"
	"github.com/molon/gomsg/pb/authpb"
	"github.com/molon/pkg/errors"
	"github.com/molon/pkg/server"
	"github.com/molon/pkg/tracing/otgrpc"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/molon/pkg/registry"
)

func GRPCServer(logger *logrus.Logger) (*server.Server, net.Listener) {
	srv := grpc.NewServer(
		grpc.UnaryInterceptor(
			grpc_middleware.ChainUnaryServer(
				// 打印日志
				grpc_logrus.UnaryServerInterceptor(logrus.NewEntry(logger)),
				// 只是用来褪去堆栈信息
				errors.UnaryServerInterceptor(),
				// 一定要在褪去堆栈信息之前调用，这样jaeger会记录详细一些
				otgrpc.UnaryServerInterceptor(
					otgrpc.WithRequstBody(true),
					otgrpc.WithResponseBody(true),
				),
				// recovery住，返回带有堆栈信息的错误
				grpc_recovery.UnaryServerInterceptor(
					grpc_recovery.WithRecoveryHandler(
						func(p interface{}) error {
							err := errors.Statusf(codes.Internal, "%s", p)
							logger.Errorf("panic\n%+v", err)
							return err
						},
					),
				),
			),
		),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     0, // infinity
			MaxConnectionAge:      0, // infinity
			MaxConnectionAgeGrace: 0, // infinity
			Time:                  time.Duration(time.Second * 10),
			Timeout:               time.Duration(time.Second * 3),
		}),
		grpc.KeepaliveEnforcementPolicy(
			keepalive.EnforcementPolicy{
				MinTime:             time.Second * 5, // 必须要小于调用者的ClientParameters.Time配置
				PermitWithoutStream: true,            // 要和调用者保持一致
			},
		),
	)

	authpb.RegisterAuthServer(srv, &grpcServer{})

	s, err := server.NewServer(
		server.WithGRPCServer(srv),
	)
	if err != nil {
		logger.Fatalln(err)
	}

	grpcL, err := net.Listen("tcp", fmt.Sprintf("%s:%s", viper.GetString("grpc.address"), viper.GetString("grpc.port")))
	if err != nil {
		logger.Fatalln(err)
	}

	logger.Infof("Serving auth gRPC at %v", grpcL.Addr())

	return s, grpcL
}

func NewEtcdClient(ctx context.Context, logger *logrus.Logger) *etcd.Client {
	cli, err := etcd.New(etcd.Config{
		Context:     ctx,
		Endpoints:   viper.GetStringSlice("etcd.endpoints"),
		DialTimeout: viper.GetDuration("etcd.dial-timeout"),
	})
	if err != nil {
		logger.Fatalln("Dial etcd failed:", err)
	}

	logger.Infof("Dial etcd at %v", viper.GetStringSlice("etcd.endpoints"))

	return cli
}

type grpcServer struct{}

func (s *grpcServer) Auth(ctx context.Context, in *empty.Empty) (*authpb.AuthResponse, error) {
	// 其实应该是解jwt的逻辑，这里先偷懒
	return &authpb.AuthResponse{
		Uid:      "molon",
		Platform: "mobile",
	}, nil
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	doneC := make(chan error, 1)
	sigC := make(chan os.Signal, 1)

	logger := resource.NewLogger()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 连接etcd
	etcdCli := NewEtcdClient(ctx, logger)
	defer etcdCli.Close()

	// 启动server
	grpcS, grpcL := GRPCServer(logger)
	go func() { doneC <- grpcS.Serve(grpcL, nil) }()
	defer server.GracefulStop(grpcS)

	// 服务注册
	register := registry.NewRegister(logger, etcdCli, viper.GetString("registry.name"), grpcL.Addr().String(), viper.GetInt("registry.ttl"))
	defer register.Close()

	// 结束清理
	signal.Notify(sigC, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigC
		doneC <- nil
	}()
	if err := <-doneC; err != nil {
		logger.Errorln(err)
	}
}
