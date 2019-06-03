package resource

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/molon/pkg/server"
	"github.com/molon/pkg/server/health"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// NewHealthChecker builds the server that listens on health.address
func NewHealthChecker(logger *logrus.Logger, checks map[string]health.Check) (*server.Server, net.Listener) {
	healthChecker := health.NewChecksHandler(
		viper.GetString("health.liveness"),
		viper.GetString("health.readiness"),
	)

	for name, check := range checks {
		healthChecker.AddReadiness(name, check)
	}

	s, err := server.NewServer(
		server.WithHealthChecker(healthChecker),
		server.WithHTTPHandler("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.Write([]byte("pong"))
		})),
	)
	if err != nil {
		logger.Fatalln(err)
	}

	l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", viper.GetString("health.address"), viper.GetInt("health.port")))
	if err != nil {
		logger.Fatalln(err)
	}

	// 简单检测，严格来说，应该检测当前server暴露的外部服务的可用性
	healthChecker.AddLiveness("ping", health.HTTPGetCheck(
		fmt.Sprint("http://", l.Addr(), "/ping"), time.Minute),
	)
	logger.Infof("Serving health checker at %v", l.Addr())

	return s, l
}
