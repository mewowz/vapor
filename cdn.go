package vapor

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	steamproto "github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

type HTTPSSupport string

const (
	HTTPSMandatory   HTTPSSupport = "mandatory"
	HTTPSUnavailable HTTPSSupport = "unavailable"
	HTTPSOptional    HTTPSSupport = "optional"
)

type CDNServersResponse struct {
	Response struct {
		Servers []CDNServer `json:"servers"`
	} `json:"response"`
}

type CDNServer struct {
	Type                   string `json:"type"`
	SourceID               int    `json:"source_id"`
	Load                   int    `json:"load"`
	WeightedLoad           int    `json:"weighted_load"`
	NumEntriesInClientList int    `json:"num_entries_in_client_list"`
	Host                   string `json:"host"`
	VHost                  string `json:"vhost"`
	HTTPSSupport           string `json:"https_support"`
	PriorityClass          int    `json:"priority_class"`
}

func GetDepotDecryptionKey(
	depotID,
	appID uint32,
	conn CMMessenger,
	logger *slog.Logger,
) ([]byte, error) {
	decryptionKeyListener, err := conn.GetListenerForEMsg(EMsgClientGetDepotDecryptionKeyResponse)
	if err != nil {
		return nil, err
	}

	err = submitDecryptionKeyRequest(
		depotID,
		appID,
		conn,
		logger,
	)
	if err != nil {
		return nil, err
	}

	decryptionKeyResponse, err := decryptionKeyListener.Read()
	if err != nil {
		return nil, err
	}

	decryptionKey, err := handleDecryptionKeyResponse(
		decryptionKeyResponse,
		logger,
	)

	return decryptionKey, err
}

func GetServersForSteamPipe(cellID uint32, httpTimeout time.Duration) (CDNServersResponse, error) {
	url := fmt.Sprintf(
		"https://api.steampowered.com/IContentServerDirectoryService/GetServersForSteamPipe/v1/?cellid=%d",
		cellID,
	)
	client := http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return CDNServersResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return CDNServersResponse{}, fmt.Errorf(
			"bad status code from '%s': %s",
			url, resp.Status,
		)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CDNServersResponse{}, err
	}

	var response CDNServersResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return CDNServersResponse{}, nil
	}
	return response, nil
}

func submitDecryptionKeyRequest(
	depotID,
	appID uint32,
	conn CMMessenger,
	logger *slog.Logger,
) error {
	decryptionKeyRequest := &steamproto.CMsgClientGetDepotDecryptionKey{
		DepotId: proto.Uint32(depotID),
		AppId:   proto.Uint32(appID),
	}

	decryptionKeyRequestHeader, err := NewMsgHeaderPB(
		EMsgClientGetDepotDecryptionKey,
		decryptionKeyRequest,
	)
	if err != nil {
		return err
	}

	err = conn.SubmitCMMsg(decryptionKeyRequestHeader)
	return err
}

func handleDecryptionKeyResponse(
	message Message,
	logger *slog.Logger,
) ([]byte, error) {
	decryptionKeyResponseProto := message.Proto().(*steamproto.CMsgClientGetDepotDecryptionKeyResponse)
	if decryptionKeyResponseProto.GetEresult() != 1 {
		logger.Debug("bad EResult", "EResult", decryptionKeyResponseProto.GetEresult())
		return nil, ErrBadEResult
	}

	return decryptionKeyResponseProto.GetDepotEncryptionKey(), nil
}
