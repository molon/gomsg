package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/molon/gomsg/internal/app/carrier"
	"github.com/molon/gomsg/internal/pb/boatpb"
	"github.com/molon/gomsg/internal/pkg/resource"
	"github.com/molon/pkg/clientstore"
	"github.com/molon/pkg/errors"
	"github.com/molon/pkg/grpc/timeout"
	"github.com/molon/pkg/server"
	"github.com/molon/pkg/tracing/otgrpc"

	kafkaclient "github.com/uber-go/kafka-client"
	"github.com/uber-go/kafka-client/kafka"
	"github.com/uber-go/tally"
	"go.uber.org/zap"

	"github.com/Shopify/sarama"
	etcd "github.com/coreos/etcd/clientv3"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
)

const (
	kafkaCluserName = "msg_cluster"
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

func StartBoatClientStore(ctx context.Context, logger *logrus.Logger, etcdCli *etcd.Client) *clientstore.Store {
	cs := clientstore.NewStore(
		logger,
		etcdCli,
		viper.GetString("boat.name-prefix"),
		func(target string, opts ...grpc.DialOption) (interface{}, io.Closer, error) {
			conn, err := grpc.DialContext(ctx, target, append(dialOptions, opts...)...)
			if err != nil {
				return nil, nil, errors.Wrap(err)
			}

			return boatpb.NewBoatClient(conn), conn, nil
		},
	)

	if err := cs.Start(); err != nil {
		logger.Fatalf("Start boat client store with name-prefix %s failed: \"%v\"", viper.GetString("boat.name-prefix"), err)
	}

	logger.Infof("Start boat client store with name-prefix \"%v\"", viper.GetString("boat.name-prefix"))

	return cs
}

func NewKafkaConsumeClient() kafkaclient.Client {
	brokers := map[string][]string{
		kafkaCluserName: viper.GetStringSlice("kafka.brokers"),
	}

	zapLogger := zap.NewNop()

	// sarama.Logger = logger
	// zapLogger, _ = zap.NewDevelopment()

	client := kafkaclient.New(
		kafka.NewStaticNameResolver(nil, brokers),
		zapLogger,
		tally.NoopScope,
	)
	return client
}

func StartKafkaConsumer(ctx context.Context, logger *logrus.Logger, client kafkaclient.Client) kafka.Consumer {
	config := kafka.NewConsumerConfig(
		viper.GetString("consumer.group"),
		kafka.ConsumerTopicList{
			kafka.ConsumerTopic{
				Topic: kafka.Topic{
					Name:    viper.GetString("consumer.topic"),
					Cluster: kafkaCluserName,
				},
			},
		},
	)
	config.Offsets.Initial.Offset = kafka.OffsetOldest
	config.Concurrency = viper.GetInt("consumer.concurrency")

	consumer, err := client.NewConsumer(config)
	if err != nil {
		logger.Fatalln("Create consumer failed:", err)
	}

	if err := consumer.Start(); err != nil {
		logger.Fatalln("Start consumer failed:", err)
	}

	logger.Infof("Start consume [%s] at kafka brokers %v",
		viper.GetString("consumer.topic"),
		viper.GetStringSlice("kafka.brokers"),
	)

	return consumer
}

func StartKafkaConsumerForRetryTopic(ctx context.Context, logger *logrus.Logger, client kafkaclient.Client) kafka.Consumer {
	config := kafka.NewConsumerConfig(
		viper.GetString("consumer.group"),
		kafka.ConsumerTopicList{
			kafka.ConsumerTopic{
				Topic: kafka.Topic{
					Name:    viper.GetString("consumer.retry-topic"),
					Cluster: kafkaCluserName,
				},
			},
		},
	)
	config.Offsets.Initial.Offset = kafka.OffsetOldest
	config.Concurrency = viper.GetInt("consumer.retry-concurrency")

	consumer, err := client.NewConsumer(config)
	if err != nil {
		logger.Fatalln("Create consumer failed:", err)
	}

	if err := consumer.Start(); err != nil {
		logger.Fatalln("Start consumer failed:", err)
	}

	logger.Infof("Start consume [%s] at kafka brokers %v",
		viper.GetString("consumer.retry-topic"),
		viper.GetStringSlice("kafka.brokers"),
	)

	return consumer
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

	// 连接gRPC服务
	boatStore := StartBoatClientStore(ctx, logger, etcdCli)
	defer boatStore.Stop()

	// 初始化kafka生产者
	producer := NewKafkaProducer(logger)
	defer producer.Close()

	// 启动kafka consumer
	kcli := NewKafkaConsumeClient()
	consumer := StartKafkaConsumer(ctx, logger, kcli)
	defer func() {
		consumer.Stop()
		<-consumer.Closed()
	}()
	retryConsumer := StartKafkaConsumerForRetryTopic(ctx, logger, kcli)
	defer func() {
		consumer.Stop()
		<-consumer.Closed()
	}()

	// 开启主程 内部config 可以直接unmarshal进来
	cfg := carrier.Config{}
	if err := viper.Unmarshal(&cfg); err != nil {
		logger.Fatalln("Unmarshal viper to config failed:", err)
	}
	carrier.Start(ctx, logger, cfg, boatStore, producer, consumer, retryConsumer, redisPool)
	defer carrier.Stop()

	// 启动服务
	doneC := make(chan error, 2)
	sigC := make(chan os.Signal, 1)

	// 自身健康检查
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
