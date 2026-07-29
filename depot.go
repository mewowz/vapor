package vapor

import (
	"log/slog"

	steamproto "github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

func GetManifestRequestCode(
	depotID,
	appID uint32,
	manifestID uint64,
	branch,
	branchPasswdHash string,
	conn CMMessenger,
	logger *slog.Logger,
) (uint64, error) {
	manifestListener, err := conn.GetListenerForEMsg(EMsgServiceMethodResponse)
	if err != nil {
		return 0, err
	}
	logger.Debug("obtained manifestListener")

	err = submitManifestRequestCodeRequest(
		depotID,
		appID,
		manifestID,
		branch,
		branchPasswdHash,
		conn,
		logger,
	)
	if err != nil {
		return 0, err
	}
	logger.Debug("submitted manifest code request")

	manifestRequestCodeResponse, err := manifestListener.Read()
	if err != nil {
		return 0, err
	}
	logger.Debug("got manifest code response")

	manifestRequestCode, err := handleManifestRequestCodeResponse(
		manifestRequestCodeResponse,
		logger,
	)
	logger.Debug("handled manifest code response")

	return manifestRequestCode, err
}

func submitManifestRequestCodeRequest(
	depotID,
	appID uint32,
	manifestID uint64,
	branch,
	branchPasswdHash string,
	conn CMMessenger,
	logger *slog.Logger,
) error {
	manifestCodeRequest := &steamproto.CContentServerDirectory_GetManifestRequestCode_Request{
		DepotId:    proto.Uint32(depotID),
		AppId:      proto.Uint32(appID),
		ManifestId: proto.Uint64(manifestID),
	}
	if branch != "public" {
		manifestCodeRequest.AppBranch = proto.String(branch)
		if branchPasswdHash != "" {
			manifestCodeRequest.BranchPasswordHash = proto.String(branchPasswdHash)
		}
	}

	manifestCodeRequestHeader, err := NewMsgHeaderPB(
		EMsgServiceMethodCallFromClient,
		manifestCodeRequest,
	)
	if err != nil {
		return err
	}
	manifestCodeRequestHeader.header.TargetJobName = proto.String("ContentServerDirectory.GetManifestRequestCode#1")

	err = conn.SubmitCMMsg(manifestCodeRequestHeader)
	return err
}

func handleManifestRequestCodeResponse(
	message Message,
	logger *slog.Logger,
) (uint64, error) {
	manifestCodeResponseProto := message.Proto().(*steamproto.CContentServerDirectory_GetManifestRequestCode_Response)
	manifestCodeResponse := message.(*msgHeaderPB)
	if manifestCodeResponse.header.GetEresult() != 1 {
		logger.Debug("bad EResult", "EResult", manifestCodeResponse.header.GetEresult())
		return 0, ErrBadEResult
	}
	return manifestCodeResponseProto.GetManifestRequestCode(), nil
}
