package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/molon/pkg/grpc/timeout"

	"github.com/molon/gomsg/pb/pushpb"

	"github.com/golang/protobuf/proto"

	"github.com/molon/gomsg/internal/pkg/resource"
	"github.com/molon/gomsg/pb/authpb"
	"github.com/molon/pkg/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/molon/gomsg/internal/app/station"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	gateway_runtime "github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/molon/pkg/server/gateway"

	"github.com/Shopify/sarama"

	etcd "github.com/coreos/etcd/clientv3"
	etcdnaming "github.com/coreos/etcd/clientv3/naming"
	"github.com/molon/pkg/registry"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/molon/pkg/tracing/otgrpc"
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
			PermitWithoutStream: true,            // 要和调用者保持一致
		},
	),
}

func Server(logger *logrus.Logger) (*server.Server, net.Listener, net.Listener) {
	grpcServer, err := station.NewGRPCServer(serverKeepaliveOptions...)
	if err != nil {
		logger.Fatalln(err)
	}

	grpcL, err := net.Listen("tcp", fmt.Sprintf("%s:%d", viper.GetString("grpc.address"), viper.GetInt("grpc.port")))
	if err != nil {
		logger.Fatalln(err)
	}

	s, err := server.NewServer(
		server.WithGRPCServer(grpcServer),
		server.WithGateway(
			gateway.WithGatewayOptions(
				gateway_runtime.WithForwardResponseOption(
					func(ctx context.Context, w http.ResponseWriter, resp proto.Message) error {
						w.Header().Set("Cache-Control", "no-cache, no-store, max-age=0, must-revalidate")
						return nil
					},
				),
				gateway_runtime.WithMarshalerOption("application/json", &gateway_runtime.JSONPb{
					OrigName:     true,
					EnumsAsInts:  true,
					EmitDefaults: true,
				}),
			),
			gateway.WithEndpointRegistration("/v1/", pushpb.RegisterPushHandlerFromEndpoint),
			gateway.WithServerAddress(grpcL.Addr().String()),
		),
	)
	if err != nil {
		logger.Fatalln(err)
	}

	httpAddr := fmt.Sprintf("%s:%d", viper.GetString("http.address"), viper.GetInt("http.port"))
	httpL, err := net.Listen("tcp", httpAddr)
	if err != nil {
		logger.Fatalln(err)
	}

	logger.Infof("Serving station gRPC at %v", grpcL.Addr())
	logger.Infof("Serving station http at %v", httpL.Addr())

	return s, grpcL, httpL
}

func NewAuthClient(ctx context.Context, logger *logrus.Logger, etcdCli *etcd.Client) (authpb.AuthClient, *grpc.ClientConn) {
	r := &etcdnaming.GRPCResolver{Client: etcdCli}
	b := grpc.RoundRobin(r)

	conn, err := grpc.DialContext(ctx,
		viper.GetString("auth.name"),
		append(dialOptions, grpc.WithBalancer(b))...,
	)
	if err != nil {
		logger.Fatalln("Dial auth gRPC failed:", err)
	}

	logger.Infof("Dial auth gRPC at %s", viper.GetString("auth.name"))

	return authpb.NewAuthClient(conn), conn
}

func NewKafkaProducer(logger *logrus.Logger) sarama.SyncProducer {
	kc := sarama.NewConfig()
	kc.Producer.RequiredAcks = sarama.WaitForAll
	kc.Producer.Retry.Max = 10
	kc.Producer.Return.Successes = true
	pub, err := sarama.NewSyncProducer(viper.GetStringSlice("kafka.brokers"), kc)
	if err != nil {
		logger.Fatalln("Create kafka producer failed:", err)
	}

	logger.Infof("Create producer at kafka brokers %v", viper.GetStringSlice("kafka.brokers"))

	return pub
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	logger := resource.NewLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 连接etcd
	etcdCli := resource.NewEtcdClient(ctx, logger)
	defer etcdCli.Close()

	// 连接jaeger
	tracer := resource.NewJaegerTracer(logger)
	defer tracer.Close()

	// 初始化redis
	redisPool := resource.NewRedisPool(logger)
	defer redisPool.Close()

	// 初始化kafka生产者
	kp := NewKafkaProducer(logger)
	defer kp.Close()

	// 连接gRPC服务
	authCli, authConn := NewAuthClient(ctx, logger, etcdCli)
	defer authConn.Close()

	// 初始化 内部config 可以直接unmarshal进来
	cfg := station.Config{}
	if err := viper.Unmarshal(&cfg); err != nil {
		logger.Fatalln("Unmarshal viper to config failed:", err)
	}
	if err := station.Init(cfg, logger, authCli, redisPool, kp); err != nil {
		logger.Fatalln("Init station failed:", err)
	}

	// 启动服务
	doneC := make(chan error, 3)
	sigC := make(chan os.Signal, 1)

	srv, grpcL, httpL := Server(logger)
	go func() { doneC <- srv.Serve(grpcL, httpL) }()
	defer server.GracefulStop(srv)

	// 服务注册
	register := registry.NewRegister(logger, etcdCli, viper.GetString("registry.name"), grpcL.Addr().String(), viper.GetInt("registry.ttl"))
	defer register.Close()

	// 健康检查
	// TODO: 这里应该加readiness关于redis的回调
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
