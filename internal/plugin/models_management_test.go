package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestModelsManagementEndpoint(t *testing.T) {
	m := &Manager{}
	resp, err := m.HandleManagement(context.Background(), pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/management/plugins/commandcode-go-cliproxyapi/models",
	})
	if err != nil {
		t.Fatalf("HandleManagement: %v", err)
	}
	if resp.StatusCode != 0 && resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Counts struct {
			Total     int `json:"total"`
			Published int `json:"published"`
			Excluded  int `json:"excluded"`
		} `json:"counts"`
		Raw       []json.RawMessage `json:"raw"`
		Published []json.RawMessage `json:"published"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, string(resp.Body))
	}
	if body.Counts.Total != 0 || len(body.Raw) != 0 || len(body.Published) != 0 {
		t.Fatalf("empty manager should report no models: %+v", body.Counts)
	}
}

func TestRegisterManagementAdvertisesModelsRoute(t *testing.T) {
	m := &Manager{}
	raw, err := m.registerManagement([]byte("{}"))
	if err != nil {
		t.Fatalf("registerManagement: %v", err)
	}
	if !strings.Contains(string(raw), "/plugins/commandcode-go-cliproxyapi/models") {
		t.Fatalf("management registration does not advertise the models route: %s", raw)
	}
}
