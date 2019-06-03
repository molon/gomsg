package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/molon/gomsg/internal/pkg/resource"
	"github.com/molon/pkg/grpc/timeout"
	"github.com/molon/pkg/registry"
	"github.com/molon/pkg/server"
	"github.com/molon/pkg/tracing/otgrpc"

	"github.com/molon/gomsg/internal/app/boat"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/molon/gomsg/internal/pb/stationpb"
	"github.com/rs/xid"

	etcd "github.com/coreos/etcd/clientv3"
	etcdnaming "github.com/coreos/etcd/clientv3/naming"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
)

var dialOptions = []grpc.DialOption{
	grpc.WithInsecure(),
	grpc.WithInitialWindowSize(1 << 24),
	grpc.WithInitialConnWindowSize(1 << 24),
	grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1 << 24)),
	grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(1 << 24)),
	grpc.WithBackoffMaxDelay(time.Second * 3),
	grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                time.Second * 10,
		Timeout:             time.Second * 3,
		PermitWithoutStream: true,
	}),
	grpc.WithUnaryInterceptor(
		grpc_middleware.ChainUnaryClient(
			timeout.UnaryClientInterceptorWhenCall(time.Second*10),
			otgrpc.UnaryClientInterceptor(
				otgrpc.WithRequstBody(true),
				otgrpc.WithResponseBody(true),
			),
		),
	),
}

var serverKeepaliveOptions = []grpc.ServerOption{
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
			PermitWithoutStream: true,            // 一般要和调用者保持一致
		},
	),
}

var loopServerKeepaliveOptions = []grpc.ServerOption{
	grpc.KeepaliveParams(keepalive.ServerParameters{
		MaxConnectionIdle:     0, // infinity
		MaxConnectionAge:      0, // infinity
		MaxConnectionAgeGrace: 0, // infinity
		Time:                  time.Duration(time.Second * 300),
		Timeout:               time.Duration(time.Second * 20),
	}),
	grpc.KeepaliveEnforcementPolicy(
		keepalive.EnforcementPolicy{
			MinTime:             time.Second * 200, // 必须要小于调用者的ClientParameters.Time配置
			PermitWithoutStream: true,              // 为true表示即使客户端在idle时候发送心跳也不会被踢出
		},
	),
}

func GRPCServer(logger *logrus.Logger) (*server.Server, net.Listener) {
	grpcServer, err := boat.NewGRPCServer(serverKeepaliveOptions...)
	if err != nil {
		logger.Fatalln(err)
	}

	s, err := server.NewServer(
		server.WithGRPCServer(grpcServer),
	)
	if err != nil {
		logger.Fatalln(err)
	}

	grpcL, err := net.Listen("tcp", fmt.Sprintf("%s:%d", viper.GetString("grpc.address"), viper.GetInt("grpc.port")))
	if err != nil {
		logger.Fatalln(err)
	}

	logger.Infof("Serving boat gRPC at %v", grpcL.Addr())

	return s, grpcL
}

func LoopServer(ctx context.Context, logger *logrus.Logger) (*server.Server, io.Closer, net.Listener) {
	grpcServer, closer, err := boat.NewLoopServer(ctx, loopServerKeepaliveOptions...)
	if err != nil {
		logger.Fatalln(err)
	}

	s, err := server.NewServer(
		server.WithGRPCServer(grpcServer),
	)
	if err != nil {
		logger.Fatalln(err)
	}

	grpcL, err := net.Listen("tcp", fmt.Sprintf("%s:%d", viper.GetString("loop.address"), viper.GetInt("loop.port")))
	if err != nil {
		logger.Fatalln(err)
	}

	logger.Infof("Serving loop gRPC at %v", grpcL.Addr())

	return s, closer, grpcL
}

func NewStationClient(ctx context.Context, logger *logrus.Logger, etcdCli *etcd.Client) (stationpb.StationClient, *grpc.ClientConn) {

	r := &etcdnaming.GRPCResolver{Client: etcdCli}
	b := grpc.RoundRobin(r)

	conn, err := grpc.DialContext(ctx,
		viper.GetString("station.name"),
		append(dialOptions, grpc.WithBalancer(b))...,
	)
	if err != nil {
		logger.Fatalln("Dial station gRPC failed:", err)
	}

	logger.Infof("Dial station gRPC at %s", viper.GetString("station.name"))

	return stationpb.NewStationClient(conn), conn
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	logger := resource.NewLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 服务唯一标识
	applicationId := xid.New().String()
	logger.Infof("Application ID: %s", applicationId)

	// 连接etcd
	etcdCli := resource.NewEtcdClient(ctx, logger)
	defer etcdCli.Close()

	// 连接jaeger
	tracer := resource.NewJaegerTracer(logger)
	defer tracer.Close()

	// 连接gRPC服务
	stationCli, stationConn := NewStationClient(ctx, logger, etcdCli)
	defer stationConn.Close()

	// 初始化boat
	boat.Init(applicationId, logger, stationCli)

	// 启动server
	sigC := make(chan os.Signal, 1)
	doneC := make(chan error, 4)

	grpcS, grpcL := GRPCServer(logger)

	loopS, loopCloser, loopL := LoopServer(ctx, logger)
	go func() { doneC <- grpcS.Serve(grpcL, nil) }()
	go func() { doneC <- loopS.Serve(loopL, nil) }()
	defer server.GracefulStop(grpcS, loopS)
	defer loopCloser.Close() // 这个是为了主动要求stream立即关闭，否则grpc.GracefulStop会一直等待

	// 服务注册
	register := registry.NewRegister(logger, etcdCli, fmt.Sprintf("%s%s", viper.GetString("registry.name-prefix"), applicationId), grpcL.Addr().String(), viper.GetInt("registry.ttl"))
	defer register.Close()

	// 健康检查
	healthS, healthL := resource.NewHealthChecker(logger, nil)
	go func() { doneC <- healthS.Serve(nil, healthL) }()
	defer server.GracefulStop(healthS)

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
