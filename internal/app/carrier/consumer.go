package carrier

import (
	"context"
	"time"

	"github.com/Shopify/sarama"
	"github.com/molon/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	"github.com/molon/gomsg/internal/pb/mqpb"
	"github.com/molon/pkg/util"
	"github.com/uber-go/kafka-client/kafka"
)

type consumer struct {
	kp      sarama.SyncProducer
	kc      kafka.Consumer
	retryKc kafka.Consumer

	tomb   *util.LoopTomb
	ctx    context.Context
	cancel context.CancelFunc
}

func newConsumer(
	ctx context.Context,
	kp sarama.SyncProducer,
	kc kafka.Consumer,
	retryKc kafka.Consumer,
) *consumer {
	ctx, cancel := context.WithCancel(ctx)

	c := &consumer{
		kp:      kp,
		kc:      kc,
		retryKc: retryKc,

		ctx:    ctx,
		cancel: cancel,
		tomb:   util.NewLoopTomb(),
	}

	return c
}

func (c *consumer) start() {
	// 两种topic的消费分开是为了不互相占用吞吐量，重试那块里的消费要服从于retry-delay

	// 常规topic消费
	for index := 0; index < global.config.Consumer.Concurrency; index++ {
		c.tomb.Go(func(stopC <-chan struct{}) {
			c.messageLoop(c.kc, stopC)
		})
	}

	// 重试topic的消费
	for index := 0; index < global.config.Consumer.RetryConcurrency; index++ {
		c.tomb.Go(func(stopC <-chan struct{}) {
			c.messageLoop(c.retryKc, stopC)
		})
	}
}

func (c *consumer) stop() {
	c.cancel()
	c.tomb.Close() // stop and wait
}

func (c *consumer) messageLoop(kafkaConsumer kafka.Consumer, stopC <-chan struct{}) {
	logger := global.logger.WithFields(logrus.Fields{
		"method": "messageLoop",
	})

	for {
		select {
		case m, ok := <-kafkaConsumer.Messages():
			if !ok {
				logger.Infoln("Consumer is closed")
				return
			}

			pb := &mqpb.Payload{}
			if err := proto.Unmarshal(m.Value(), pb); err != nil {
				logger.Fatalf("messageLoop: %+v", err)
				return
			}

			// debug下才打印
			// logger.Debugf("\n%v:%v Retry:%d Topic:%s\n%v",
			// 	ptypes.TimestampString(pb.GetTimestamp()),
			// 	string(m.Key()),
			// 	pb.GetRetryCount(),
			// 	m.Topic(),
			// 	util.ProtoToJSONString(pb))

			// 判断是否应该delay重试的消息
			if pb.GetRetryCount() > 0 && global.config.Consumer.RetryDelay > 0 {
				lastAttemptAt, _ := util.FromTimestampProto(pb.GetLastAttemptAt())
				if !lastAttemptAt.IsZero() {
					sleepDuration := global.config.Consumer.RetryDelay + lastAttemptAt.Sub(time.Now())
					if sleepDuration > 0 {
						logger.Debugf("delayMsg: delay %v", sleepDuration)
						timer := time.NewTimer(sleepDuration)
						select {
						case <-timer.C:
						case <-stopC:
							timer.Stop()
							return
						case <-c.ctx.Done():
							timer.Stop()
							return
						}
					}
				} else {
					logger.Warnf("lastAttemptAt is zero")
				}
			}

			// 执行消费
			ret, err := process(c.ctx, pb)
			if err != nil {
				// 若返回错误，则直接让mq去重试了
				logger.Errorf("process: %+v", err)
				continue
			}

			if ret != nil { // 如果返回不为nil，则说明返回消息体未消费成功，需要重试或者标为死信
				var topic string
				// 已达到最大重试次数，说明已经没有重试的必要了，丢进死信队列
				if ret.RetryCount >= global.config.Consumer.MaxRetries {
					topic = global.config.Consumer.DLQTopic
					logger.Errorf("已经达到最大重试次数，丢进死信队列: %s", ret.GetSeq())
				} else {
					logger.Errorf("当前重试次数: %d，丢进重试队列: %s", ret.GetRetryCount(), ret.GetSeq())
					// 重试次数+1 丢进重试队列
					ret.RetryCount++
					topic = global.config.Consumer.RetryTopic
				}

				// 设置最后尝试时间
				ret.LastAttemptAt = ptypes.TimestampNow()

				// 构造ProducerMessage
				b, err := proto.Marshal(ret)
				if err != nil {
					logger.WithError(err).Fatalf("proto.Marshal")
					return
				}

				pm := &sarama.ProducerMessage{
					Topic: topic,
					Key:   sarama.StringEncoder(ret.Seq), // 主要是为了kafka分区而已，此key不用
					Value: sarama.ByteEncoder(b),
				}

				// 执行发送，若返回错误，只能让mq去重试了
				if _, _, err := c.kp.SendMessage(pm); err != nil {
					logger.WithError(err).Errorf("SendMessage")
					continue
				}
			}

			// 一般都会ack掉，除非上面continue了
			m.Ack()
		case <-stopC:
			return
		case <-c.ctx.Done():
			return
		}
	}
}

// 返回的payload为需要重新投递出去的玩意
func process(ctx context.Context, pb *mqpb.Payload) (out *mqpb.Payload, rerr error) {
	defer func() {
		// 发现panic，直接让mq重试即可
		if r := recover(); r != nil {
			rerr = errors.Wrap(r.(error))
		}
	}()

	switch t := pb.Body.(type) {
	case *mqpb.Payload_SendOfflineToSession:
		if err := sendOfflineToSession(ctx, t.SendOfflineToSession); err != nil {
			plog.Errorf("%+v", err)
			return pb, nil // 重试
		}
	case *mqpb.Payload_KickoutSession:
		if err := kickoutSession(ctx, t.KickoutSession); err != nil {
			plog.Errorf("%+v", err)
			return pb, nil // 重试
		}
	case *mqpb.Payload_ToUid:
		toUid := sendToUid(ctx, pb, t.ToUid)
		if toUid != nil {
			// 改写内容，重新投递
			t.ToUid = toUid
			return pb, nil
		}
	default:
		return nil, errors.Wrapf("Unknown payload type: %T", t)
	}

	return nil, nil
}
