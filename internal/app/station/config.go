package station

import "github.com/molon/pkg/errors"

type Config struct {
	Producer struct {
		Topic string
	}
}

func (cfg *Config) Valid() error {
	if len(cfg.Producer.Topic) < 1 {
		return errors.Errorf("producer.topic must be non-empty")
	}

	return nil
}
