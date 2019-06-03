package carrier

import (
	"context"

	"github.com/gomodule/redigo/redis"
	"github.com/molon/gomsg/internal/pkg/offline"
	"github.com/molon/gomsg/internal/pkg/sessionstore"
	"github.com/molon/pkg/clientstore"
	"github.com/sirupsen/logrus"

	"github.com/uber-go/kafka-client/kafka"

	"github.com/Shopify/sarama"
)

var global *globalCtx
var plog *logrus.Logger

type globalCtx struct {
	config    Config
	logger    *logrus.Logger
	boatStore *clientstore.Store
	producer  sarama.SyncProducer
	redisPool *redis.Pool
	sstore    *sessionstore.Store
	offstore  *offline.Store

	c *consumer
}

func Start(
	ctx context.Context,
	logger *logrus.Logger,
	config Config,
	boatStore *clientstore.Store,
	producer sarama.SyncProducer,
	kc kafka.Consumer,
	retryKc kafka.Consumer,
	redisPool *redis.Pool,
) {
	if err := config.Validate(); err != nil {
		logger.Fatalf("Start carrier failed: %+v", err)
	}

	offstore, err := offline.InitStore(ctx, redisPool)
	if err != nil {
		logger.Fatalf("Start carrier failed: %+v", err)
	}

	plog = logger

	global = &globalCtx{
		config:    config,
		logger:    logger,
		boatStore: boatStore,
		producer:  producer,
		redisPool: redisPool,

		sstore:   sessionstore.NewStore(logger, redisPool),
		offstore: offstore,
		c:        newConsumer(ctx, producer, kc, retryKc),
	}

	global.c.start()
}

func Stop() {
	global.c.stop()
}
