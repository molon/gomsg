module github.com/molon/gomsg

require (
	github.com/Shopify/sarama v1.21.0
	github.com/coreos/etcd v3.3.12+incompatible
	github.com/golang/protobuf v1.3.1
	github.com/gomodule/redigo v2.0.0+incompatible
	github.com/grpc-ecosystem/go-grpc-middleware v1.0.0
	github.com/grpc-ecosystem/grpc-gateway v1.8.5
	github.com/molon/gochat v0.0.0-20190603132342-6b4ddc4b2fbc
	github.com/molon/pkg v0.0.0-20190603080514-c9a7129fb70b
	github.com/prometheus/client_golang v0.9.3 // indirect
	github.com/rs/xid v1.2.1
	github.com/sirupsen/logrus v1.4.1
	github.com/spf13/pflag v1.0.3
	github.com/spf13/viper v1.3.2
	github.com/uber-go/kafka-client v0.2.1
	github.com/uber-go/tally v3.3.8+incompatible
	go.uber.org/atomic v1.4.0 // indirect
	go.uber.org/zap v1.9.1
	golang.org/x/net v0.0.0-20190328230028-74de082e2cca
	google.golang.org/genproto v0.0.0-20190401181712-f467c93bbac2
	google.golang.org/grpc v1.19.1
)

// replace github.com/molon/pkg => /Users/molon/go/src/github.com/molon/pkg
// replace github.com/molon/gochat => /Users/molon/go/src/github.com/molon/gochat
