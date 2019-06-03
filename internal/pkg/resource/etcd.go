package resource

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	etcd "github.com/coreos/etcd/clientv3"
)

func NewEtcdClient(ctx context.Context, logger *logrus.Logger) *etcd.Client {
	cli, err := etcd.New(etcd.Config{
		Context:     ctx,
		Endpoints:   viper.GetStringSlice("etcd.endpoints"),
		DialTimeout: viper.GetDuration("etcd.dial-timeout"),
	})
	if err != nil {
		logger.Fatalln("Dial etcd failed:", err)
	}

	logger.Infof("Dial etcd at %v", viper.GetStringSlice("etcd.endpoints"))

	return cli
}
