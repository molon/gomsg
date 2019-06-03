/*
 *
 * Copyright 2019 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

// Binary client is an example client.
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/molon/pkg/grpc/timeout"
	"github.com/molon/pkg/util"

	"github.com/molon/gomsg/internal/pkg/resource"
	"github.com/molon/gomsg/pb/msgpb"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	_ "github.com/golang/protobuf/ptypes"
	_ "github.com/golang/protobuf/ptypes/wrappers"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	_ "github.com/molon/gochat/pb/chatpb"
)

var addr = flag.String("addr", "localhost:9999", "the address to connect to")

var dialOptions = []grpc.DialOption{
	grpc.WithInsecure(),
	grpc.WithInitialWindowSize(1 << 24),
	grpc.WithInitialConnWindowSize(1 << 24),
	grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1 << 24)),
	grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(1 << 24)),
	grpc.WithBackoffMaxDelay(time.Second * 3),
	grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                time.Second * 300,
		Timeout:             time.Second * 20,
		PermitWithoutStream: true,
	}),
	grpc.WithUnaryInterceptor(
		grpc_middleware.ChainUnaryClient(
			timeout.UnaryClientInterceptorWhenCall(time.Second * 10),
		),
	),
}

func main() {
	flag.Parse()

	conn, err := grpc.Dial(*addr, dialOptions...)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	viper.Set("logging.level", "debug")
	logger := resource.NewLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopCli := msgpb.NewMsgClient(conn)

	stream, err := loopCli.LoopV1(ctx)
	if err != nil {
		logger.Fatalf("loop: %v", err)
	}

	errC := make(chan error, 1)
	sendC := make(chan *msgpb.ClientPayload, 10)
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigC
		stream.CloseSend()
		select {
		case errC <- nil:
		default:
		}
	}()

	go func() {
		// 去重使用
		msgSeqs := map[string]bool{}

		for {
			m, err := stream.Recv()
			if err == io.EOF {
				// 如果服务端return nil的话会触发
				select {
				case errC <- nil:
				default:
				}
				return
			}
			if err != nil {
				select {
				case errC <- err:
				default:
				}
				return
			}

			switch t := m.Body.(type) {
			case *msgpb.ServerPayload_MsgsWrapper:
				for _, msg := range t.MsgsWrapper.GetMsgs() {
					duplicate := ""
					if msgSeqs[msg.GetSeq()] {
						duplicate = "(重复获取)"
					}

					// foo := &chatpb.Message{}
					// if err := ptypes.UnmarshalAny(msg.Body, foo); err != nil {
					// 	logger.Infof("UnmarshalAny failed: %+v", err)
					// } else {
					// 	logger.Infof("%+v", foo)
					// }

					logger.Infof("seq:%s%s\n%v", msg.GetSeq(), duplicate, util.ProtoToJSONStringForPrint(msg.Body))
					msgSeqs[msg.GetSeq()] = true
				}
			case *msgpb.ServerPayload_SubResp:
				logger.Infof("recv ServerPayload_SubResp:%v", t.SubResp)
			case *msgpb.ServerPayload_Pong:
				logger.Infof("recv ServerPayload_Pong:%v", t.Pong)
			default:
				logger.Errorf("unknown server msg body")
			}

			if m.GetNeedAck() {
				go func(seq string) {
					sendC <- &msgpb.ClientPayload{
						Body: &msgpb.ClientPayload_Ack{
							Ack: &msgpb.Ack{
								Seq: seq,
							},
						},
					}
					logger.Infof("Ack(%s)", seq)
				}(m.Seq)
			}
		}
	}()

	logger.Infoln("Running...")

	for {
		select {
		case m := <-sendC:
			stream.Send(m)
		// 不建议等待这个，有可能比recv loop那里先捕获到，但其暴露的信息有限
		// case <-stream.Context().Done():
		// 	logger.Fatalf("err: %v", stream.Context().Err())
		case err := <-errC:
			logger.Fatalf("errC: %v", err)
		}
	}
}
