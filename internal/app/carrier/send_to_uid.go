package carrier

import (
	"context"

	"github.com/molon/gomsg/pb/msgpb"
	"github.com/molon/pkg/util"

	"github.com/molon/gomsg/internal/pb/boatpb"
	"github.com/molon/gomsg/internal/pb/mqpb"
	"github.com/molon/gomsg/pb/errorpb"
	"github.com/molon/gomsg/pb/pushpb"

	"github.com/golang/protobuf/ptypes"
	"github.com/sirupsen/logrus"
)

func tidyPlatformConfigs(pushPcfg *pushpb.PlatformConfig) map[string]platformConfig {
	ret := map[string]platformConfig{}

	// 若没配置，说明是全平台
	if pushPcfg == nil ||
		(len(pushPcfg.Platforms) <= 0 && len(pushPcfg.WithoutPlatforms) <= 0) {
		for k, v := range global.config.pcfgs {
			ret[k] = v
		}
		return ret
	}

	// 若指定，则筛出有效的，无效的忽略
	if len(pushPcfg.Platforms) > 0 {
		for _, name := range pushPcfg.Platforms {
			if val, ok := global.config.pcfgs[name]; ok {
				ret[name] = val
			}
		}

		return ret
	}

	// 若指定忽略，则筛出没指定的返回
	for k, v := range global.config.pcfgs {
		exists := false
		for _, name := range pushPcfg.WithoutPlatforms {
			if name == k {
				exists = true
				break
			}
		}
		if !exists {
			ret[k] = v
		}
	}

	return ret
}

func sendToUid(ctx context.Context, payload *mqpb.Payload, pb *mqpb.ToUid) *mqpb.ToUid {
	logger := global.logger.WithFields(logrus.Fields{
		"method": "sendToUid",
		"uid":    pb.GetUid(),
	})

	var (
		// 整理出此次要发送平台的所有配置信息
		allPcfgs = tidyPlatformConfigs(pb.GetPlatformConfig())
		// 需要重试的平台名称列表
		needRetryPlats []string
		// 需要离线的平台名称列表
		needOfflinePlats []string
		// 发现失效的会话ID列表
		invalidSids []string
	)

	// 找到目标已存储的所有会话，此时可能会包含一些已经无效的会话
	plat2Sesses, err := global.sstore.GetPlatformToSessions(ctx, pb.GetUid())
	if err != nil {
		logger.WithError(err).Errorf("GetSessionsWithUid")
		// TODO: 由于session和离线消息都是用的一个redis，所以即使到达最大重试消息，也没必要做离线处理尝试了，几乎不会成功
		return pb // 出错直接重试全部
	}

	// 移除此次不需要关心的会话列表
	for plat := range plat2Sesses {
		if _, ok := allPcfgs[plat]; !ok {
			delete(plat2Sesses, plat)
		}
	}

	// 已经确认非在线的平台名称要塞入needOfflinePlats里
	if len(plat2Sesses) != len(allPcfgs) {
		for plat, _ := range allPcfgs {
			if _, ok := plat2Sesses[plat]; !ok {
				needOfflinePlats = append(needOfflinePlats, plat)
			}
		}
	}

	// 执行投递
	ackWait := ptypes.DurationProto(global.config.Boat.AckWait)
	for plat, sesses := range plat2Sesses {
		// 每个会话都会去投递
		// 但只有每个平台的第一个有效会话投递成功才可认定 此消息对此用户在此平台 已经确认完毕
		validSessCount := 0
		firstValidSuccess := false
		for _, sess := range sesses {
			ll := logger.WithFields(logrus.Fields{
				"plat": sess.Platform,
				"bid":  sess.Bid,
				"sid":  sess.Sid,
			})

			cli, ok, err := boatClient(sess.Bid)
			if err != nil {
				ll.WithError(err).Debugf("boatClient")
				// 这里一般理解是网络异常，姑且认为会话还是有效的
				validSessCount++
				continue
			}

			if !ok { // 对应boat服务不存在，对应会话也认为无效
				invalidSids = append(invalidSids, sess.Sid)
				ll.Debugf("boat is not exists, so session is invalid")
				continue
			}

			if _, err := cli.PushMessages(ctx, &boatpb.PushMessagesRequest{
				Sid:     sess.Sid,
				AckWait: ackWait,
				Msgs:    pb.GetMsgs(),
			}); err == nil {
				validSessCount++
				if validSessCount == 1 {
					firstValidSuccess = true
				}
			} else {
				if equalErrCode(err, errorpb.Code_SESSION_NOT_FOUND) {
					// boat告知会话不存在，则会话无效
					invalidSids = append(invalidSids, sess.Sid)
					ll.Debugf("boat returns Code_SESSION_NOT_FOUND, so session is invalid")
					continue
				}
				// TODO: 如果返回NOT_ACK是否要踢出会话呢？

				ll.WithError(err).Errorf("PushMessages")
				// 只要没发现是Code_SESSION_NOT_FOUND，依然认定会话是有效的
				validSessCount++
			}
		}

		// 发现此平台其实没有有效会话，丢进离线处理
		if validSessCount <= 0 {
			needOfflinePlats = append(needOfflinePlats, plat)
		} else {
			// 第一个有效会话没投递成功，则认为 此消息对此用户在此平台 未处理完毕，记录retry
			if !firstValidSuccess {
				needRetryPlats = append(needRetryPlats, plat)
			}
		}
	}

	logger.Debugf("After trying to send-> \n allPcfgs: %+v \n needRetryPlats:%+v \n needOfflinePlats:%+v \n invalidSids:%+v", allPcfgs, needRetryPlats, needOfflinePlats, invalidSids)

	// 清除invalidSids，这些玩意早发现早清理
	if len(invalidSids) > 0 {
		if err := global.sstore.DeleteSessions(ctx, pb.GetUid(), invalidSids); err != nil {
			logger.WithError(err).Warnf("DeleteSessions") // 对执行结果不需要强制care
		}
	}

	// 检测是否达到最大重试次数，如果是 则应该将 needRetryPlats 合并到 needOfflinePlats 里
	// 因为此时已经不能相信客户端能完成反馈了，此消息留给离线处理去保证不丢失吧
	if payload.GetRetryCount() >= global.config.Consumer.MaxRetries {
		needOfflinePlats = append(needOfflinePlats, needRetryPlats...)
		needRetryPlats = []string{} // 清空

		logger.Debugf("retryCount>=%d, so merge needRetryPlats to needOfflinePlats", global.config.Consumer.MaxRetries)
	}

	// 尝试做离线处理
	// - 离线处理失败的plat要记录到needRetryPlats里，但是由于离线处理是最后一道关卡，在触及最大重试次数之后，consumer那边估计就会直接将其丢进死信队列了，只能后续手动处理了，这也是木有办法的办法了
	if len(needOfflinePlats) > 0 {
		sendTime, _ := util.FromTimestampProto(payload.GetTimestamp())
		platformToMaxOMCount := map[string]int{}
		for _, plat := range needOfflinePlats {
			for _, msg := range pb.GetMsgs() {
				if msg.GetOptions()&msgpb.MessageOption_NEED_OFFLINE > 0 {
					if err := global.offstore.Write(ctx, pb.GetUid(),
						plat, msg, sendTime, global.config.Offline.Expire); err != nil {
						logger.WithError(err).Errorf("offstore.Write")
						// 错了就直接放弃这个plat吧
						needRetryPlats = append(needRetryPlats, plat)
						break
					}
					// 只要有成功写入就记录
					platformToMaxOMCount[plat] = allPcfgs[plat].maxOfflineCount
				}

				if msg.GetOptions()&msgpb.MessageOption_NEED_NOTIFICATION > 0 {
					// TODO: 如果需要通知，则投递通知mq消息
				}
			}
		}
		if len(platformToMaxOMCount) > 0 {
			if err := global.offstore.Clean(ctx, pb.GetUid(), global.config.Offline.Expire, platformToMaxOMCount); err != nil {
				logger.WithError(err).Errorf("offstore.Clean")
				// 这里返回错误打印一下即可
			}
		}
	}

	// 若需重试，则返回那些平台
	if len(needRetryPlats) > 0 {
		logger.Debugf("needRetryPlats: %+v", needRetryPlats)
		pb.PlatformConfig = &pushpb.PlatformConfig{
			Platforms: needRetryPlats,
		}
		return pb
	} else {
		logger.Debugf("消费完毕")
	}
	// 完全处理OK返回空即可
	return nil
}
