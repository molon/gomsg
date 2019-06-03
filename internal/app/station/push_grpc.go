package station

import (
	"context"

	"github.com/golang/protobuf/ptypes"

	"github.com/Shopify/sarama"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/molon/gomsg/internal/pb/mqpb"
	"github.com/molon/gomsg/pb/msgpb"
	"github.com/molon/gomsg/pb/pushpb"
	"github.com/molon/pkg/errors"
	"github.com/rs/xid"
)

type pushGrpcServer struct{}

func (s *pushGrpcServer) Push(ctx context.Context, in *pushpb.PushRequest) (*empty.Empty, error) {
	msgCount := len(in.GetMsgBodies())
	if msgCount <= 0 {
		return &empty.Empty{}, nil
	}

	// 根据request构造出一堆mq消息，以uid为粒度分发
	now := ptypes.TimestampNow()

	// 先给消息挨个生成seq
	seqs := make([]string, msgCount)
	for i := 0; i < msgCount; i++ {
		seqs[i] = xid.New().String()
	}

	pms := []*sarama.ProducerMessage{}
	for _, uid := range in.GetUids() {
		opts, ok := in.GetExclusiveMsgOptions()[uid]
		if !ok {
			opts = in.GetMsgOptions()
		}

		pcfg, ok := in.GetExclusivePlatformConfig()[uid]
		if !ok {
			pcfg = in.GetPlatformConfig()
		}

		msgs := []*msgpb.Message{}
		for i, body := range in.GetMsgBodies() {
			msgs = append(msgs, &msgpb.Message{
				Seq:     seqs[i],
				Options: opts,
				Body:    body,
			})
		}

		pb := &mqpb.Payload{
			Seq:        xid.New().String(),
			Timestamp:  now,
			RetryCount: 0,
			Body: &mqpb.Payload_ToUid{
				ToUid: &mqpb.ToUid{
					Uid:            uid,
					Msgs:           msgs,
					PlatformConfig: pcfg,
					Reserve:        in.GetReserve(),
				},
			},
		}

		b, err := proto.Marshal(pb)
		if err != nil {
			return nil, errors.Wrap(err)
		}

		pm := &sarama.ProducerMessage{
			Key:   sarama.StringEncoder(pb.Seq), // 仅仅为了kafka分区而已
			Topic: global.config.Producer.Topic,
			Value: sarama.ByteEncoder(b),
		}

		pms = append(pms, pm)
	}

	// 投递至mq
	if err := global.producer.SendMessages(pms); err != nil {
		return nil, errors.Wrap(err)
	}

	return &empty.Empty{}, nil
}
