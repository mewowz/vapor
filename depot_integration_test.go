//go:build integration

package vapor

import (
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"
)

func getNested(m map[string]interface{}, path ...string) (interface{}, bool) {
	var cur interface{} = m
	for _, key := range path {
		asMap, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur, ok = asMap[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func TestGetManifestRequestCode(t *testing.T) {
	// For this test, we'll use a well-known dedicated server distribution
	// for Project Zomboid since the anonymous login can download it
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)
	client, err := NewSteamClient(WithLogger(logger), WithTimeout(10*time.Second))

	if err = client.Start(); err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	if err = client.LogonAnonymous(); err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}

	appInfos, _, err := RequestProductInfo(
		[]uint32{380870},
		[]uint32{},
		client.connection,
		logger,
		*client.connection.authInfo,
	)
	if err != nil {
		t.Fatalf("RequestProductInfo failed: %v", err)
	}

	var manifestID uint64
	v, ok := getNested(
		appInfos["380870"].Info,
		"appinfo", "depots", "380871", "manifests", "public", "gid",
	)
	if ok {
		raw, ok := v.(string)
		if ok {
			manifestID, err = strconv.ParseUint(raw, 10, 64)
			if err != nil {
				t.Fatalf("could not parse manifestID into uint64")
			}
		} else {
			t.Fatalf("error type-asserting appInfo data to string")
		}
	}

	logger.Debug(
		"Getting manifest codes",
		"appid", 380870,
		"depotid", 380871,
		"manifestid", manifestID,
	)
	code, err := GetManifestRequestCode(
		380871,
		380870,
		manifestID,
		"",
		"",
		client.connection,
		logger,
	)
	if err != nil {
		t.Fatalf("GetManifestRequestCode failed: %v", err)
	}
	if code == 0 {
		t.Fatalf("GetManifestRequestCode returned: 0, expected non-zero")
	}
}
