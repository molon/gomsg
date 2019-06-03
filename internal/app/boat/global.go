package boat

import (
	"context"
	"sync"

	"github.com/molon/gomsg/internal/pb/stationpb"
	"github.com/sirupsen/logrus"
)

var global *globalCtx
var plog *logrus.Entry

type globalCtx struct {
	mu sync.RWMutex

	ctx           context.Context
	applicationId string
	sessionStore  *SessionStore
	stationCli    stationpb.StationClient
}

func Init(
	applicationId string,
	logger *logrus.Logger,
	stationCli stationpb.StationClient,
) {
	plog = logrus.NewEntry(logger)
	global = &globalCtx{
		applicationId: applicationId,
		sessionStore:  NewSessionStore(),
		stationCli:    stationCli,
	}
}
