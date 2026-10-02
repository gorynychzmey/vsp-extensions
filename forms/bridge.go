package forms

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// wsBridge sends form service actions over ZADT_VSP's WebSocket (domain
// "form"), the one the server or the command keeps.
type wsBridge struct {
	ws *adt.DebugWebSocketClient
}

// FormRequest sends one action to the form service and returns its data. A
// system without the service answers UNKNOWN_DOMAIN, which comes back as an
// error saying what to install.
func (b wsBridge) FormRequest(ctx context.Context, action string, params map[string]any) (json.RawMessage, error) {
	if !b.ws.IsConnected() {
		return nil, fmt.Errorf("not connected")
	}
	id := b.ws.GenerateID("form")
	// Replacing an Adobe form deletes, recreates and saves it; a large form
	// takes its time.
	rawMsg := map[string]any{
		"id":      id,
		"domain":  "form",
		"action":  action,
		"params":  params,
		"timeout": 300000,
	}
	resp, err := b.ws.SendRawRequest(ctx, id, rawMsg, 305*time.Second)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		if resp.Error == nil {
			return nil, fmt.Errorf("form service: %s failed", action)
		}
		if strings.EqualFold(resp.Error.Code, "UNKNOWN_DOMAIN") {
			return nil, fmt.Errorf("this system's ZADT_VSP has no form service: install ZCL_VSP_FORM_SERVICE "+
				"from vsp-extensions (abap/) and register it in ZCL_VSP_APC_HANDLER: %s", resp.Error.Message)
		}
		return nil, fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Data, nil
}
