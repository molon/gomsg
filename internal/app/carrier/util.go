package carrier

import (
	"fmt"

	"github.com/molon/gomsg/internal/pb/boatpb"
	"github.com/molon/pkg/errors"
	"github.com/molon/gomsg/pb/errorpb"
	"google.golang.org/grpc/status"
)

func boatClient(boatId string) (boatpb.BoatClient, bool, error) {
	target := fmt.Sprintf("%s%s", global.config.Boat.NamePrefix, boatId)

	cli, ok := global.boatStore.Get(target)
	if !ok {
		plog.Debugf("Boat(%s) is not exist", boatId)
		return nil, false, nil
	}

	if cli != nil {
		return cli.(boatpb.BoatClient), true, nil
	}

	return nil, ok, errors.Wrapf("Boat(%s) has not connected yet", boatId)
}

func equalErrCode(err error, code errorpb.Code) bool {
	details := status.Convert(err).Details()
	if len(details) > 0 {
		if errdetail, ok := details[0].(*errorpb.Detail); ok {
			if errdetail.Code == code {
				return true
			}
		}
	}
	return false
}
