package station

import (
	"github.com/Shopify/sarama"
	"github.com/gomodule/redigo/redis"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	"github.com/molon/gomsg/internal/pb/stationpb"
	"github.com/molon/gomsg/internal/pkg/sessionstore"
	"github.com/molon/gomsg/pb/authpb"
	"github.com/molon/gomsg/pb/pushpb"
	"github.com/molon/pkg/errors"
	"github.com/molon/pkg/tracing/otgrpc"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var global *globalCtx
var plog *logrus.Entry

type globalCtx struct {
	config    Config
	authCli   authpb.AuthClient
	redisPool *redis.Pool
	producer  sarama.SyncProducer

	sstore *sessionstore.Store
}

func Init(
	config Config,
	logger *logrus.Logger,
	authCli authpb.AuthClient,
	redisPool *redis.Pool,
	producer sarama.SyncProducer,
) error {
	if err := config.Valid(); err != nil {
		return err
	}

	plog = logrus.NewEntry(logger)
	global = &globalCtx{
		config:   config,
		authCli:  authCli,
		producer: producer,
		sstore:   sessionstore.NewStore(logger, redisPool),
	}

	return nil
}

func NewGRPCServer(opts ...grpc.ServerOption) (*grpc.Server, error) {
	opts = append(opts, grpc.UnaryInterceptor(
		grpc_middleware.ChainUnaryServer(
			// 打印日志
			grpc_logrus.UnaryServerInterceptor(plog),
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
						plog.Errorf("panic\n%+v", err)
						return err
					},
				),
			),
		),
	))

	s := grpc.NewServer(opts...)
	stationpb.RegisterStationServer(s, &grpcServer{})
	pushpb.RegisterPushServer(s, &pushGrpcServer{})
	return s, nil
}
