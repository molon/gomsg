package station

import (
	"github.com/golang/protobuf/ptypes"
	"github.com/molon/pkg/errors"

	"github.com/Shopify/sarama"
	"github.com/golang/protobuf/proto"
	"github.com/molon/gomsg/internal/pb/mqpb"
	"github.com/molon/gomsg/pb/errorpb"
	"github.com/rs/xid"
)

func pubUidSids(uid string, sids []string, fillBody func(payload *mqpb.Payload, uid string, sid string)) error {
	if len(uid) < 1 {
		return errors.Wrapf("uid is empty")
	}
	if len(sids) <= 0 {
		return errors.Wrapf("sids is empty")
	}

	now := ptypes.TimestampNow()
	msgs := []*sarama.ProducerMessage{}

	for _, sid := range sids {
		mw := &mqpb.Payload{
			Seq:        xid.New().String(),
			Timestamp:  now,
			RetryCount: 0,
		}
		fillBody(mw, uid, sid)

		b, err := proto.Marshal(mw)
		if err != nil {
			return errors.Wrap(err)
		}

		m := &sarama.ProducerMessage{
			Key:   sarama.StringEncoder(mw.Seq), // 主要是为了kafka分区而已，此key不用
			Topic: global.config.Producer.Topic,
			Value: sarama.ByteEncoder(b),
		}

		msgs = append(msgs, m)
	}

	if err := global.producer.SendMessages(msgs); err != nil {
		return errors.Wrap(err)
	}

	return nil
}

func pubKickoutSessions(uid string, sids []string, code errorpb.Code) error {
	if err := pubUidSids(uid, sids,
		func(payload *mqpb.Payload, uid string, sid string) {
			payload.Body = &mqpb.Payload_KickoutSession{
				KickoutSession: &mqpb.KickoutSession{
					Uid:  uid,
					Sid:  sid,
					Code: code,
				},
			}
		},
	); err != nil {
		return err
	}

	plog.Debugf("KickoutSessions %v(%v)", uid, sids)
	return nil
}

func pubSendOfflineToSessions(uid string, sids []string) error {
	if err := pubUidSids(uid, sids,
		func(payload *mqpb.Payload, uid string, sid string) {
			payload.Body = &mqpb.Payload_SendOfflineToSession{
				SendOfflineToSession: &mqpb.SendOfflineToSession{
					Uid: uid,
					Sid: sid,
				},
			}
		},
	); err != nil {
		return err
	}

	plog.Debugf("SendOfflineToSessions %v(%v)", uid, sids)
	return nil
}
