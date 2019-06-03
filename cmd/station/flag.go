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

	// Jaeger
	_ = pflag.String("jaeger.service-name", "gomsg_station", "")
	_ = pflag.String("jaeger.collector-endpoint", "http://localhost:24268", "endpoint of Jaeger collector")

	// gRPC
	_ = pflag.String("grpc.address", "0.0.0.0", "adress of gRPC server")
	_ = pflag.Int("grpc.port", 0, "port of gRPC server")

	// http
	_ = pflag.String("http.address", "0.0.0.0", "adress of http server, if empty then no http serves")
	_ = pflag.Int("http.port", 8080, "port of http server")

	// redis
	_ = pflag.String("redis.address", "127.0.0.1", "")
	_ = pflag.String("redis.port", "9379", "")

	// kafka and producer
	_ = pflag.StringSlice("kafka.brokers", []string{"127.0.0.1:9092"}, "")
	_ = pflag.String("producer.topic", "molon-msg", "")

	// gRPC servers
	_ = pflag.String("auth.name", "example://auth", "name of auth server")

	// etcd
	_ = pflag.StringSlice("etcd.endpoints", []string{"http://127.0.0.1:8379"}, "")
	_ = pflag.Duration("etcd.dial-timeout", 5*time.Second, "")

	// registry
	_ = pflag.String("registry.name", "gomsg://station", "")
	_ = pflag.Int("registry.ttl", 10, "")
)

func init() {
	pflag.Parse()
	viper.BindPFlags(pflag.CommandLine)

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
