package carrier

// func notify(ctx context.Context, noti *mqpb.Notification) error {
// 	pb := &mqpb.Payload{
// 		Seq:       xid.New().String(),
// 		Timestamp: ptypes.TimestampNow(),
// 		Body: &mqpb.Payload_Notification{
// 			Notification: noti,
// 		},
// 	}

// 	b, err := proto.Marshal(pb)
// 	if err != nil {
// 		global.logger.WithError(err).Fatalf("proto.Marshal")
// 		return errors.Wrap(err)
// 	}

// 	pm := &sarama.ProducerMessage{
// 		Topic: topic,
// 		Key:   sarama.StringEncoder(pb.Seq), // 主要是为了kafka分区而已，此key不用
// 		Value: sarama.ByteEncoder(b),
// 	}

// 	// 执行发送，若返回错误，只能让mq去重试了
// 	if _, _, err := c.kp.SendMessage(pm); err != nil {
// 		logger.WithError(err).Errorf("SendMessage")
// 		continue
// 	}
// }
