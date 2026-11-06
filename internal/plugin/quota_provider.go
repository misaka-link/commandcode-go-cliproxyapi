package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// quotaProvider implements the host's native QuotaProvider capability so
// CommandCode credentials surface quota on CPA auth-file cards and the quota
// management page, instead of only on the plugin's own page.
type quotaProvider struct{ m *Manager }

func (q quotaProvider) Identifier() string { return ProviderID }

func (q quotaProvider) DescribeQuota(_ context.Context, _ pluginapi.QuotaDescribeRequest) (pluginapi.QuotaDescribeResponse, error) {
	return pluginapi.QuotaDescribeResponse{
		SupportedProviders: []string{ProviderID},
		DisplayName:        "CommandCode",
		SupportsReset:      false,
	}, nil
}

func (q quotaProvider) FetchQuota(ctx context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
	var doer httpDoer
	switch {
	case req.HTTPClient != nil:
		doer = req.HTTPClient
	case q.m != nil:
		doer = q.m.bridge
	}
	return q.m.fetchQuotaResponse(ctx, req, doer)
}

func (q quotaProvider) ResetQuota(_ context.Context, _ pluginapi.QuotaResetRequest) (pluginapi.QuotaResetResponse, error) {
	return pluginapi.QuotaResetResponse{Success: false, Message: "CommandCode 不提供重置额度接口"}, nil
}

// handleQuotaFetch is the ABI entry (quota.fetch). Over the C ABI the host
// sends host_callback_id instead of a live HTTPClient, so bind the bridge to
// that callback so host.http.do uses the credential-scoped transport.
func (m *Manager) handleQuotaFetch(ctx context.Context, req pluginapi.QuotaFetchRequest, callbackID string) (pluginapi.QuotaFetchResponse, error) {
	var doer httpDoer
	switch {
	case req.HTTPClient != nil:
		doer = req.HTTPClient
	case m != nil && m.bridge != nil && strings.TrimSpace(callbackID) != "":
		doer = callbackDoer{b: m.bridge, id: callbackID}
	case m != nil:
		doer = m.bridge
	}
	return m.fetchQuotaResponse(ctx, req, doer)
}

type callbackDoer struct {
	b  *HostBridge
	id string
}

func (c callbackDoer) Do(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	return c.b.DoWithCallback(ctx, req, c.id)
}

// fetchQuotaResponse resolves the credential key and base URL, fetches the
// account snapshot, and normalizes it for the host.
func (m *Manager) fetchQuotaResponse(ctx context.Context, req pluginapi.QuotaFetchRequest, doer httpDoer) (pluginapi.QuotaFetchResponse, error) {
	if m == nil {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("quota provider unavailable")
	}
	key := selectedAuthKey(req.Attributes, req.Metadata)
	if key == "" {
		key = apiKeyFromStorageJSON(req.StorageJSON)
	}
	if key == "" {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("认证文件缺少 API key")
	}
	m.mu.RLock()
	baseURL, timeout := m.cfg.BaseURL, m.cfg.RequestTimeout
	m.mu.RUnlock()
	if doer == nil {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("host bridge unavailable")
	}
	usage, _, _, err := fetchQuota(ctx, doer, baseURL, timeout, key)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	return quotaResponseFromUsage(usage), nil
}

func apiKeyFromStorageJSON(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var v struct {
		APIKey string `json:"api_key"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return strings.TrimSpace(v.APIKey)
}

// quotaResponseFromUsage maps the plugin's account snapshot onto the host's
// normalized quota shape: a subscription, bounded numeric summary metrics, and
// groups of buckets (monthly plan allowance, 5-hour and weekly rate windows).
func quotaResponseFromUsage(u quotaUsage) pluginapi.QuotaFetchResponse {
	out := pluginapi.QuotaFetchResponse{}
	if strings.TrimSpace(u.Plan) != "" || strings.TrimSpace(u.PlanID) != "" {
		out.Subscription = &pluginapi.QuotaSubscription{
			Plan:     strings.TrimSpace(u.Plan),
			TierName: strings.TrimSpace(u.Plan),
			TierID:   strings.TrimSpace(u.PlanID),
		}
	}
	out.Summary = append(out.Summary, pluginapi.QuotaMetric{
		Key: "plan_credits_remaining", Label: "剩余套餐额度", Value: round6(u.CreditsLeft), Format: "number",
	})
	if u.PurchasedCredits > 0 {
		out.Summary = append(out.Summary, pluginapi.QuotaMetric{
			Key: "purchased_credits", Label: "加油包余额", Value: round6(u.PurchasedCredits), Format: "number",
		})
	}
	if u.Month != nil && u.Month.Cap > 0 {
		remaining := math.Max(u.Month.Cap-u.Month.Used, 0)
		out.Groups = append(out.Groups, pluginapi.QuotaGroup{
			DisplayName: "套餐额度",
			Buckets: []pluginapi.QuotaBucket{{
				Window:            "month",
				RemainingFraction: fraction(remaining, u.Month.Cap),
				ResetTime:         u.Month.ResetsAt,
				Description:       fmt.Sprintf("剩余 %s / 共 %s", num(remaining), num(u.Month.Cap)),
			}},
		})
	}
	out.Groups = append(out.Groups, pluginapi.QuotaGroup{
		DisplayName: "速率限制",
		Buckets: []pluginapi.QuotaBucket{
			windowBucket("5 小时", u.FiveHour),
			windowBucket("每周", u.Weekly),
		},
	})
	return out
}

func windowBucket(label string, w quotaWindow) pluginapi.QuotaBucket {
	b := pluginapi.QuotaBucket{Window: label, ResetTime: w.ResetsAt}
	if w.Cap > 0 {
		remaining := math.Max(w.Cap-w.Used, 0)
		b.RemainingFraction = fraction(remaining, w.Cap)
		b.Description = fmt.Sprintf("%s / %s", num(w.Used), num(w.Cap))
	} else {
		b.RemainingFraction = 1
		b.Description = "不限"
	}
	return b
}

func fraction(remaining, total float64) float64 {
	if total <= 0 {
		return 1
	}
	f := remaining / total
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// num renders a credit amount without trailing zeros.
func num(v float64) string {
	return strconv.FormatFloat(math.Round(v*1e6)/1e6, 'f', -1, 64)
}
