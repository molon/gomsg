package boat

import (
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/molon/gomsg/pb/errorpb"
	"github.com/molon/gomsg/pb/msgpb"
	"github.com/molon/pkg/errors"
	"github.com/rs/xid"
)

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: map[string]*Session{},
	}
}

func (ss *SessionStore) NewSession() *Session {
	sess := &Session{
		sid:      xid.New().String(),
		kickoutC: make(chan struct{}, 1),
		doneC:    make(chan struct{}, 1),
		sendC:    make(chan *msgpb.ServerPayload, 256),
		ackCs:    make(map[string]chan struct{}),
	}

	ss.mu.Lock()
	ss.sessions[sess.sid] = sess
	ss.mu.Unlock()

	return sess
}

func (ss *SessionStore) Get(sid string) *Session {
	ss.mu.RLock()
	sess := ss.sessions[sid]
	ss.mu.RUnlock()
	return sess
}

func (ss *SessionStore) Delete(sid string) {
	ss.mu.Lock()
	delete(ss.sessions, sid)
	ss.mu.Unlock()
}

type Session struct {
	mu sync.RWMutex

	sid      string
	uid      string
	platform string

	doneC       chan struct{}
	kickoutC    chan struct{}
	kickoutOnce sync.Once
	loopErr     error

	sendC chan *msgpb.ServerPayload
	ackCs map[string]chan struct{}
}

func (sess *Session) Send(m *msgpb.ServerPayload, ackWait time.Duration) error {
	// 执行发送，最多50微秒超时吧，此时肯定消息堆积严重了
	t := time.NewTimer(50 * time.Microsecond)
	select {
	case sess.sendC <- m:
		t.Stop() // for gc
	case <-t.C:
		st, _ := status.
			Newf(codes.Unavailable, "too many msgs sent to the client").
			WithDetails(&errorpb.Detail{
				Code: errorpb.Code_TOO_MANY_MSGS_TO_BE_SENT,
			})
		return errors.Wrap(st.Err())
	}

	if !m.GetNeedAck() {
		return nil
	}

	// 若需则等待ack
	seq := m.Seq
	ackC := make(chan struct{}, 1)
	sess.mu.Lock()
	sess.ackCs[seq] = ackC
	sess.mu.Unlock()
	defer func(seq string) {
		sess.mu.Lock()
		delete(sess.ackCs, seq)
		sess.mu.Unlock()
	}(seq)

	t = time.NewTimer(ackWait)
	select {
	case <-ackC:
		t.Stop() // for gc
		return nil
	case <-t.C:
		// 这里返回一个特别的错误码，告知调用者是未ack，然后调用者要决定是否要踢除连接或者其他
		st, _ := status.
			Newf(codes.Internal, "client ack timeout").
			WithDetails(&errorpb.Detail{
				Code: errorpb.Code_NO_ACK,
			})
		return errors.Wrap(st.Err())
	}
}

func (sess *Session) recv(m *msgpb.ClientPayload) error {
	switch t := m.Body.(type) {
	case *msgpb.ClientPayload_Ack:
		sess.mu.RLock()
		ackC, ok := sess.ackCs[t.Ack.GetSeq()]
		sess.mu.RUnlock()

		if ok {
			ackC <- struct{}{}
		}
	case *msgpb.ClientPayload_Sub:
		// TODO: 还没实现这个玩意
		return errors.Statusf(codes.Unimplemented, "unimplemented")
	default:
		return errors.Statusf(codes.InvalidArgument, "unknown client msg body")
	}

	return nil
}

func (sess *Session) Update(uid, platform string) {
	sess.mu.Lock()
	sess.uid = uid
	sess.platform = platform
	sess.mu.Unlock()
}

func (sess *Session) Kickout(err error) {
	sess.kickoutOnce.Do(func() {
		sess.writeLoopError(err)
		close(sess.kickoutC)
	})

	// 等待清理完毕
	<-sess.doneC
}

func (sess *Session) writeLoopError(err error) {
	sess.mu.Lock()
	sess.loopErr = err
	sess.mu.Unlock()
}

func (sess *Session) loopError() error {
	sess.mu.RLock()
	loopErr := sess.loopErr
	sess.mu.RUnlock()
	return loopErr
}
