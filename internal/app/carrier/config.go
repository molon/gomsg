package carrier

import (
	"time"

	"github.com/molon/pkg/errors"
)

type platformConfig struct {
	name            string
	maxOfflineCount int
	// 暂时就这一个配置
}

type Config struct {
	Boat struct {
		NamePrefix string        `mapstructure:"name-prefix"`
		AckWait    time.Duration `mapstructure:"ack-wait"`
	}
	Consumer struct {
		Concurrency      int
		Topic            string
		RetryTopic       string        `mapstructure:"retry-topic"`
		RetryConcurrency int           `mapstructure:"retry-concurrency"`
		RetryDelay       time.Duration `mapstructure:"retry-delay"`
		MaxRetries       int64         `mapstructure:"max-retries"`
		DLQTopic         string        `mapstructure:"dlq-topic"`
	}
	Platform struct {
		Names            []string
		MaxOfflineCounts map[string]int `mapstructure:"max-offline-counts"`
	}
	Offline struct {
		BatchCount int64 `mapstructure:"batch-count"`
		Expire     time.Duration
	}

	pcfgs map[string]platformConfig
}

func (cfg *Config) Validate() error {
	if cfg.Boat.AckWait < 250*time.Millisecond {
		return errors.Wrapf("boat.ack-wait must >= 250ms")
	}

	if cfg.Consumer.Concurrency <= 0 {
		return errors.Wrapf("consumer.concurrency must > 0")
	}

	if cfg.Consumer.Topic == cfg.Consumer.RetryTopic {
		return errors.Wrapf("consumer.topic cant equal to consumer.retry-topic")
	}

	if cfg.Consumer.RetryConcurrency <= 0 {
		return errors.Wrapf("consumer.retry-concurrency must > 0")
	}

	if cfg.Consumer.MaxRetries <= 0 {
		return errors.Wrapf("consumer.max-retries must > 0")
	}

	if len(cfg.Platform.Names) <= 0 {
		return errors.Wrapf("platform.names is empty")
	}

	if cfg.Offline.Expire <= 0 {
		return errors.Wrapf("offline.expire must > 0")
	}

	if cfg.Offline.BatchCount <= 10 {
		return errors.Wrapf("offline.batch-count must > 10")
	}

	// 整理平台配置为便利版本
	cfg.pcfgs = map[string]platformConfig{}
	for _, name := range cfg.Platform.Names {
		maxOfflineCount, ok := cfg.Platform.MaxOfflineCounts[name]
		if !ok {
			return errors.Wrapf("max-offline-count of %s is not set", name)
		}

		cfg.pcfgs[name] = platformConfig{
			name:            name,
			maxOfflineCount: maxOfflineCount,
		}
	}
	return nil
}
