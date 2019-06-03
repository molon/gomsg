package resource

import (
	"io"

	"github.com/molon/pkg/tracing"
	"github.com/molon/pkg/util"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func NewJaegerTracer(logger *logrus.Logger) io.Closer {
	collectorEndpoint := viper.GetString("jaeger.collector-endpoint")
	if len(collectorEndpoint) < 1 {
		return util.NoopCloser{}
	}

	cfg := tracing.DefaultJaegerConfiguration(
		viper.GetString("jaeger.service-name"),
		collectorEndpoint,
	)

	t, err := tracing.NewJaegerTracer(logger, cfg)
	if err != nil {
		logger.Fatalln("Init jaeger tracer failed:", err)
	}

	logger.Infof("Init jaeger tracer with %q at %v", cfg.ServiceName, collectorEndpoint)

	return t
}
