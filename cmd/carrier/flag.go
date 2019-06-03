package main

import (
	"log"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	// Config
	_ = pflag.String("config.file", "", "path of the configuration file")

	// Logging
	_ = pflag.String("logging.level", "debug", "log level of application")

	// Health
	_ = pflag.String("health.address", "0.0.0.0", "address of health http server")
	_ = pflag.Int("health.port", 0, "port of health http server")
	_ = pflag.String("health.liveness", "/healthz", "endpoint for liveness checks")
	_ = pflag.String("health.readiness", "/ready", "endpoint for readiness checks")

	// etcd
	_ = pflag.StringSlice("etcd.endpoints", []string{"http://127.0.0.1:8379"}, "")
	_ = pflag.Duration("etcd.dial-timeout", 5*time.Second, "")

	// Jaeger
	_ = pflag.String("jaeger.service-name", "gomsg_carrier", "")
	_ = pflag.String("jaeger.collector-endpoint", "http://localhost:14268", "endpoint of Jaeger collector")

	// platform
	_                            = pflag.StringSlice("platform.names", []string{"mobile", "desktop"}, "")
	flagPlatformMaxOfflineCounts = pflag.StringToInt("platform.max-offline-counts", map[string]int{
		"mobile":  -1,
		"desktop": 80,
	}, "-1 means infinity")

	_ = pflag.Duration("offline.expire", 2160*time.Hour, "90 days")
	_ = pflag.Int64("offline.batch-count", 80, "")

	// kafka and consumer
	_ = pflag.StringSlice("kafka.brokers", []string{"127.0.0.1:9092"}, "")
	_ = pflag.String("consumer.group", "molon-msg-group", "")
	_ = pflag.Int("consumer.concurrency", 100, "") // 消费topic的协程数
	_ = pflag.String("consumer.topic", "molon-msg", "")
	_ = pflag.Int("consumer.retry-concurrency", 10, "") // 消费retry topic的协程数
	_ = pflag.String("consumer.retry-topic", "molon-msg-retry", "")
	_ = pflag.Duration("consumer.retry-delay", 10*time.Second, "")
	_ = pflag.Int64("consumer.max-retries", 6, "")
	_ = pflag.String("consumer.dlq-topic", "molon-msg-dlq", "dead letter queue")

	// redis
	_ = pflag.String("redis.address", "0.0.0.0", "")
	_ = pflag.Int("redis.port", 9379, "")

	// gRPC servers
	_ = pflag.String("boat.name-prefix", "gomsg://boat-", "name-prefix of boat server")
	_ = pflag.Duration("boat.ack-wait", 2*time.Second, "ack-wait from boat server")
)

func init() {
	pflag.Parse()
	viper.BindPFlags(pflag.CommandLine)

	// BindPFlags 不能很好地支持map
	viper.Set("platform.max-offline-counts", *flagPlatformMaxOfflineCounts)

	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if viper.GetString("config.file") != "" {
		// log.Printf("Serving from configuration file: %s", viper.GetString("config.file"))
		viper.SetConfigFile(viper.GetString("config.file"))
		if err := viper.ReadInConfig(); err != nil {
			log.Fatal(err)
		}
	}
	// else {
	// 	log.Printf("Serving from default values, environment variables, and/or _")
	// }
}
