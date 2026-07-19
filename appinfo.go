package vapor

import (
	"bytes"
	"log/slog"
	"maps"
	"strconv"

	vdfbinary "github.com/Jleagle/steam-go/steamvdf"
	"github.com/andygrunwald/vdf"
	steamproto "github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

type (
	AppInfoRequest     = steamproto.CMsgClientPICSProductInfoRequest_AppInfo
	AppInfo            = steamproto.CMsgClientPICSProductInfoResponse_AppInfo
	PackageInfoRequest = steamproto.CMsgClientPICSProductInfoRequest_PackageInfo
	PackageInfo        = steamproto.CMsgClientPICSProductInfoResponse_PackageInfo
)

type AppEntry struct {
	Info         map[string]interface{}
	ChangeNumber uint32
	SHA          []byte
	MissingToken bool
	Size         uint32
	OnlyPublic   bool
}

type PackageEntry struct {
	Info         map[string]interface{}
	ChangeNumber uint32
	SHA          []byte
	MissingToken bool
	Size         uint32
}

type productResponseLoopReturn struct {
	appEntries     map[string]AppEntry
	packageEntries map[string]PackageEntry
	pending        bool
}

func RequestProductInfo(
	appids []uint32,
	packageids []uint32,
	conn CMMessenger,
	logger *slog.Logger,
	authInfo authConnectionInfo,
) (map[string]AppEntry, map[string]PackageEntry, error) {
	appInfoRequests, packageInfoRequests, err := getInfoRequests(
		appids,
		packageids,
		conn,
		logger,
	)
	if err != nil {
		return nil, nil, err
	}
	logger.Debug(
		"successfully obtained ClientPICSAccessTokenResponse",
		"len(appInfoRequests)", len(appInfoRequests), "len(packageInfoRequests)", len(packageInfoRequests),
	)

	productResponseListener, err := conn.GetListenerForEMsg(EMsgClientPICSProductInfoResponse)
	if err != nil {
		return nil, nil, err
	}
	logger.Debug("obtained listener", "name", "ClientPICSProductInfoResponse", "EMsg", EMsgClientPICSProductInfoResponse)
	productResponseListener.retainInMap.Store(true)

	err = submitPICSProductInfoRequest(
		appInfoRequests,
		packageInfoRequests,
		conn,
		logger,
	)
	if err != nil {
		return nil, nil, err
	}
	logger.Debug("successfully submitted ClientPICSProductInfoRequest")

	apps := make(map[string]AppEntry)
	packages := make(map[string]PackageEntry)

	for {
		loopValue, err := productResponseLoop(productResponseListener, logger)
		if err != nil {
			return nil, nil, err
		}

		maps.Copy(apps, loopValue.appEntries)
		maps.Copy(packages, loopValue.packageEntries)

		if !loopValue.pending {
			break
		}
	}

	return apps, packages, nil
}

func productResponseLoop(
	listener *EMsgListener,
	logger *slog.Logger,
) (*productResponseLoopReturn, error) {
	productResponse, err := listener.Read()
	if err != nil {
		return &productResponseLoopReturn{
			nil, nil, false,
		}, err
	}

	logger.Debug("successfully obtained ClientPICSProductInfoResponse")
	appEntries, packageEntries, err := handlePICSProductInfoResponse(productResponse)
	if err != nil {
		return &productResponseLoopReturn{
			nil, nil, false,
		}, err
	}
	productResponseProto := productResponse.Proto().(*steamproto.CMsgClientPICSProductInfoResponse)
	pending := productResponseProto.GetResponsePending()

	return &productResponseLoopReturn{
		appEntries,
		packageEntries,
		pending,
	}, nil
}

func getInfoRequests(
	appids []uint32,
	packageids []uint32,
	conn CMMessenger,
	logger *slog.Logger,
) ([]*AppInfoRequest, []*PackageInfoRequest, error) {
	tokenResponseListener, err := conn.GetListenerForEMsg(EMsgClientPICSAccessTokenResponse)
	if err != nil {
		return nil, nil, err
	}
	logger.Debug("obtained listener", "name", "ClientPICSAccessTokenResponse", "EMsg", EMsgClientPICSAccessTokenResponse)

	err = submitPICSAccessTokenRequest(
		appids, packageids, conn, logger,
	)
	if err != nil {
		return nil, nil, err
	}
	logger.Debug("submitted ClientPICSAccessTokenRequest")

	tokenResponse, err := tokenResponseListener.Read()
	if err != nil {
		return nil, nil, err
	}
	appInfoRequests, packageInfoRequests := handlePICSAccessTokenResponse(tokenResponse)
	logger.Debug(
		"successfully obtained ClientPICSAccessTokenResponse",
		"len(appInfoRequests)", len(appInfoRequests), "len(packageInfoRequests)", len(packageInfoRequests),
	)

	return appInfoRequests, packageInfoRequests, nil
}

func handlePICSProductInfoResponse(
	message Message,
) (map[string]AppEntry, map[string]PackageEntry, error) {
	productResponseProto := message.Proto().(*steamproto.CMsgClientPICSProductInfoResponse)
	apps := map[string]AppEntry{}
	packages := map[string]PackageEntry{}

	for _, app := range productResponseProto.GetApps() {
		buffer := app.GetBuffer()
		buffer = buffer[:len(buffer)-1]
		parsed, err := vdf.NewParser(bytes.NewReader(buffer)).Parse()
		if err != nil {
			return nil, nil, err
		}

		key := strconv.FormatUint(uint64(app.GetAppid()), 10)
		apps[key] = AppEntry{
			Info:         parsed,
			ChangeNumber: app.GetChangeNumber(),
			SHA:          app.GetSha(),
			MissingToken: app.GetMissingToken(),
			Size:         app.GetSize(),
			OnlyPublic:   app.GetOnlyPublic(),
		}
	}

	for _, pkg := range productResponseProto.GetPackages() {
		parsedKV, err := vdfbinary.ReadBytes(pkg.GetBuffer())
		if err != nil {
			return nil, nil, err
		}
		key := strconv.FormatUint(uint64(pkg.GetPackageid()), 10)
		packages[key] = PackageEntry{
			Info:         parsedKV.ToMapInner(),
			ChangeNumber: pkg.GetChangeNumber(),
			SHA:          pkg.GetSha(),
			MissingToken: pkg.GetMissingToken(),
			Size:         pkg.GetSize(),
		}
	}

	return apps, packages, nil
}

func submitPICSProductInfoRequest(
	appInfoRequests []*AppInfoRequest,
	packageInfoRequests []*PackageInfoRequest,
	conn CMMessenger,
	logger *slog.Logger,
) error {
	productRequest := &steamproto.CMsgClientPICSProductInfoRequest{
		Apps:          appInfoRequests,
		Packages:      packageInfoRequests,
		MetaDataOnly:  proto.Bool(false),
		NumPrevFailed: proto.Uint32(0),
	}

	productRequestHeader, err := NewMsgHeaderPB(EMsgClientPICSProductInfoRequest, productRequest)
	if err != nil {
		return err
	}

	logger.Debug("submitting ClientPICSProductInfoRequest", "EMsg", EMsgClientPICSProductInfoRequest)

	err = conn.SubmitCMMsg(productRequestHeader)
	return err
}

func handlePICSAccessTokenResponse(
	message Message,
) ([]*AppInfoRequest, []*PackageInfoRequest) {
	tokenResponseMsg := message.(*msgHeaderPB)
	tokenResponse := tokenResponseMsg.Proto().(*steamproto.CMsgClientPICSAccessTokenResponse)
	appinfos := []*AppInfoRequest{}
	packageinfos := []*PackageInfoRequest{}

	for _, appinfo := range tokenResponse.GetAppAccessTokens() {
		appinfos = append(
			appinfos,
			&AppInfoRequest{
				Appid:       proto.Uint32(appinfo.GetAppid()),
				AccessToken: proto.Uint64(appinfo.GetAccessToken()),
			},
		)
	}
	for _, packageinfo := range tokenResponse.GetPackageAccessTokens() {
		packageinfos = append(
			packageinfos,
			&PackageInfoRequest{
				Packageid:   proto.Uint32(packageinfo.GetPackageid()),
				AccessToken: proto.Uint64(packageinfo.GetAccessToken()),
			},
		)
	}

	return appinfos, packageinfos
}

func submitPICSAccessTokenRequest(
	appids []uint32,
	packageids []uint32,
	conn CMMessenger,
	logger *slog.Logger,
) error {
	tokenRequest := &steamproto.CMsgClientPICSAccessTokenRequest{
		Packageids: packageids,
		Appids:     appids,
	}

	tokenRequestHeader, err := NewMsgHeaderPB(
		EMsgClientPICSAccessTokenRequest, tokenRequest,
	)
	if err != nil {
		return err
	}

	logger.Debug(
		"submitting ClientPICSAccessTokenRequest",
		"packageids", packageids,
		"appids", appids,
	)

	err = conn.SubmitCMMsg(tokenRequestHeader)
	return err
}
