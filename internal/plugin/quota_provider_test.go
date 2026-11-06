package plugin

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"

	"commandcode-go-cliproxyapi/internal/config"
)

const quotaProviderTestKey = "quota-provider-secret"

func quotaProviderTestManager(t *testing.T, f *fakeCaller) *Manager {
	t.Helper()
	m := NewManager(NewHostBridge(f.call))
	m.cfg = config.Config{BaseURL: "https://quota.test/provider/v1", RequestTimeout: config.DefaultRequestTimeout}
	return m
}

// quotaProviderResponder serves the three CommandCode account endpoints and
// records the host_callback_id carried on each host.http.do call.
func quotaProviderResponder(t *testing.T, seenCallbacks *[]string) func(string, []byte) ([]byte, error) {
	return func(method string, payload []byte) ([]byte, error) {
		if method != pluginabi.MethodHostHTTPDo {
			return hostOK(map[string]any{}), nil
		}
		var wire struct {
			Method         string      `json:"method"`
			URL            string      `json:"url"`
			Headers        http.Header `json:"headers"`
			HostCallbackID string      `json:"host_callback_id"`
		}
		if err := json.Unmarshal(payload, &wire); err != nil {
			t.Fatalf("bad quota request: %s", payload)
		}
		if seenCallbacks != nil {
			*seenCallbacks = append(*seenCallbacks, wire.HostCallbackID)
		}
		if wire.Method != http.MethodGet || wire.Headers.Get("Authorization") != "Bearer "+quotaProviderTestKey {
			t.Fatalf("bad auth/method: %+v", wire)
		}
		switch wire.URL {
		case "https://quota.test/alpha/billing/credits":
			return hostOK(pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(`{"credits":{"monthlyCredits":51.5,"purchasedCredits":2},"windowLimits":{"limited":true,"fiveHour":{"used":0.5,"cap":14,"exceeded":false,"resetAt":1789657174807},"weekly":{"used":18.5,"cap":35,"exceeded":false,"resetAt":1790136440498}}}`)}), nil
		case "https://quota.test/alpha/billing/subscriptions":
			return hostOK(pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(`{"success":true,"data":{"planId":"individual-goat","status":"active","currentPeriodEnd":"2026-10-16T04:03:37.000Z"}}`)}), nil
		case "https://quota.test/alpha/whoami?limits=1":
			return hostOK(pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(`{"success":true,"user":{"email":"dev@example.test"},"org":null}`)}), nil
		}
		t.Fatalf("unexpected quota URL %q", wire.URL)
		return nil, nil
	}
}

func findQuotaGroup(resp pluginapi.QuotaFetchResponse, name string) (pluginapi.QuotaGroup, bool) {
	for _, g := range resp.Groups {
		if g.DisplayName == name {
			return g, true
		}
	}
	return pluginapi.QuotaGroup{}, false
}

func TestQuotaProviderDescribeAndIdentifier(t *testing.T) {
	q := quotaProvider{m: NewManager(nil)}
	if q.Identifier() != ProviderID {
		t.Fatalf("identifier = %q", q.Identifier())
	}
	desc, err := q.DescribeQuota(context.Background(), pluginapi.QuotaDescribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(desc.SupportedProviders) != 1 || desc.SupportedProviders[0] != ProviderID {
		t.Fatalf("supported = %+v", desc.SupportedProviders)
	}
	if desc.DisplayName != "CommandCode" || desc.SupportsReset {
		t.Fatalf("describe = %+v", desc)
	}
}

func TestQuotaProviderFetchMapsNativeShape(t *testing.T) {
	f := &fakeCaller{responder: quotaProviderResponder(t, nil)}
	m := quotaProviderTestManager(t, f)
	resp, err := (quotaProvider{m: m}).FetchQuota(context.Background(), pluginapi.QuotaFetchRequest{Attributes: map[string]string{"api_key": quotaProviderTestKey}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Subscription == nil || resp.Subscription.Plan != "GOAT" || resp.Subscription.TierName != "GOAT" || resp.Subscription.TierID != "individual-goat" {
		t.Fatalf("subscription = %+v", resp.Subscription)
	}
	credits := -1.0
	for _, mtr := range resp.Summary {
		if mtr.Key == "plan_credits_remaining" {
			credits = mtr.Value
		}
	}
	if credits != 51.5 {
		t.Fatalf("plan_credits_remaining = %v", credits)
	}
	plan, ok := findQuotaGroup(resp, "套餐额度")
	if !ok || len(plan.Buckets) != 1 || plan.Buckets[0].Window != "month" {
		t.Fatalf("plan group = %+v ok=%v", plan, ok)
	}
	if math.Abs(plan.Buckets[0].RemainingFraction-53.5/72.0) > 1e-9 {
		t.Fatalf("month fraction = %v", plan.Buckets[0].RemainingFraction)
	}
	if plan.Buckets[0].ResetTime != "2026-10-16T04:03:37Z" {
		t.Fatalf("month reset = %q", plan.Buckets[0].ResetTime)
	}
	rate, ok := findQuotaGroup(resp, "速率限制")
	if !ok || len(rate.Buckets) != 2 {
		t.Fatalf("rate group = %+v", rate)
	}
	if rate.Buckets[0].Window != "5 小时" || math.Abs(rate.Buckets[0].RemainingFraction-13.5/14.0) > 1e-9 {
		t.Fatalf("5h bucket = %+v", rate.Buckets[0])
	}
	if rate.Buckets[1].Window != "每周" || math.Abs(rate.Buckets[1].RemainingFraction-16.5/35.0) > 1e-9 {
		t.Fatalf("weekly bucket = %+v", rate.Buckets[1])
	}
}

func TestQuotaProviderFetchThreadsCallbackID(t *testing.T) {
	var cbs []string
	f := &fakeCaller{responder: quotaProviderResponder(t, &cbs)}
	m := quotaProviderTestManager(t, f)
	if _, err := m.handleQuotaFetch(context.Background(), pluginapi.QuotaFetchRequest{Attributes: map[string]string{"api_key": quotaProviderTestKey}}, "cb-xyz"); err != nil {
		t.Fatal(err)
	}
	if len(cbs) != 3 {
		t.Fatalf("calls = %d", len(cbs))
	}
	for _, id := range cbs {
		if id != "cb-xyz" {
			t.Fatalf("host_callback_id = %q, want cb-xyz", id)
		}
	}
}

func TestQuotaProviderFetchMissingKey(t *testing.T) {
	if _, err := (quotaProvider{m: NewManager(nil)}).FetchQuota(context.Background(), pluginapi.QuotaFetchRequest{}); err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestQuotaProviderStorageJSONKey(t *testing.T) {
	if got := apiKeyFromStorageJSON([]byte(`{"type":"commandcode","api_key":"abc"}`)); got != "abc" {
		t.Fatalf("key = %q", got)
	}
	if got := apiKeyFromStorageJSON(nil); got != "" {
		t.Fatalf("nil key = %q", got)
	}
}

func TestQuotaProviderResetUnsupported(t *testing.T) {
	resp, err := (quotaProvider{m: NewManager(nil)}).ResetQuota(context.Background(), pluginapi.QuotaResetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Fatal("reset must report unsupported")
	}
}
