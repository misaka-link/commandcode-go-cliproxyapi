# CommandCode 插件：适配 CPA 原生配额管理 + 官方 Logo —— 实施规划

> 状态：规划稿（待确认后实施）
> 目标仓库：https://github.com/mczhoucn/commandcode-go-cliproxyapi
> 本地检出：/workspace/89bt/cpa-plugin （HEAD 533972b, v0.1.4）
> 编写时间：2026-11-06

## 1. 背景与目标

### 现状
- 插件在 CPA 管理后台自带一个独立页面 **CommandCode → CommandCode 账户与额度**（`resources/quota_page.html`），由插件自己的 `/quota-usage` 接口返回配额。
- README/CHANGELOG 明确写明：**尚未接入 CPA 认证文件卡片的标准额度能力（`QuotaProvider`）**，因此 CPA 的「认证文件」卡片与「配额管理」页对 commandcode 凭据不显示任何额度。

### 目标
- **目标 A（原生配额）**：注册 CPA 原生 `QuotaProvider` 插件能力，使每个 commandcode 认证文件的额度直接显示在 CPA 的**认证文件卡片**和**配额管理页**（支持「刷新额度 / 全部获取」），不再依赖插件自建页面。
- **目标 B（Logo）**：插件管理后台页面（账户/额度页、OAuth 引导页）改用 CommandCode 官网品牌 Logo。

## 2. 关键调研结论（已验证）

1. **CPA 原生配额能力自 v7.3.x 起提供**（当前最新 v7.3.20），包含：
   - SDK：`sdk/pluginapi.QuotaProvider` 接口（`Identifier / DescribeQuota / FetchQuota / ResetQuota`）与 `Capabilities.QuotaProvider` 字段。
   - ABI 方法：`quota.identifier / quota.describe / quota.fetch / quota.reset`。
   - Host 实现：`internal/pluginhost/quota_provider.go`（含两轮 provider→plugin 匹配、`cachedQuotaSupportedProviders`）。
   - 管理路由（v7.3.20 `internal/api/server_management*.go`）：
     - `GET  /v0/management/quota/providers`
     - `POST /v0/management/quota/fetch`
     - `POST /v0/management/quota/reset`
     - `GET|POST|DELETE /v0/management/plugins/:id/quota`
   - 认证文件列表：`internal/api/handlers/management/auth_files.go` 在 `host.HasQuotaProvider(auth.Provider)` 为真时返回 `supports_quota=true`、`quota_provider=<provider>`。
   - C-ABI 注册：`internal/pluginhost/rpc_schema.go` 的能力结构体含 `QuotaProvider bool \`json:"quota_provider"\``；`rpc_client.go` 在 `quota_provider=true` 时会调用 `quota.identifier` 取得 provider key 并挂载 `rpcQuotaProvider`。

2. **依赖升级零风险**：现插件依赖 `github.com/router-for-me/CLIProxyAPI/v7 v7.2.138`。已实测将其升级到 **v7.3.20 后 `go build ./...` 直接通过，无需改动任何源码**（仅版本号变化）。

3. **配额数据契约**（`pluginapi.QuotaFetchResponse`）：
   ```
   {
     subscription: { plan, tierName, tierId },
     summary: [ { key, label, value, unit, format, currency } ],
     groups: [ { displayName, buckets: [ { window, remainingFraction, resetTime, description } ] } ],
     serverTimeOffsetMs
   }
   ```
   - 前端按 `remainingFraction`（剩余比例 0..1）渲染进度条，颜色阈值 70%/30%。
   - `resetTime` 为 RFC3339，用于重置倒计时。

4. **现有配额逻辑可直接复用**：`internal/plugin/quota.go` 的 `fetchQuota()` 已实现 CommandCode 账户 API：
   - `GET {authority}/alpha/billing/credits`（剩余套餐额度、5 小时/每周窗口）
   - `GET {authority}/alpha/billing/subscriptions`（planId、status、currentPeriodEnd）
   - `GET {authority}/alpha/whoami?limits=1`（邮箱/组织）
   - `AccountAPIBase()` 已能从 `base-url` 推导 `{authority}`；`planFor()` 已维护套餐名与月度额度表。
   - 只需把 `quotaUsage` 映射成 `QuotaFetchResponse`，无需重写网络层。

5. **官方 Logo 资产**（https://commandcode.ai/brand）：
   - 文字标志：`/logov2/logo-bg-transparent-fg-dark.svg`（浅底用，深色字）、`/logov2/logo-bg-transparent-fg-light.svg`（深底用，浅色字）。
   - 图形标志：`.../symbols/symbol.svg`、`black-symbol-commandcode.svg`、`bg-black-symbol-commandcode.png`。
   - 品牌主色 Burple `#8C4EDD`（可选对齐）。
   - 已下载至 `cpa-plugin/assets/brand/`（wordmark-dark.svg / wordmark-light.svg / symbol.svg / apple-touch-icon.png）。

## 3. 实施方案（分阶段）

### Phase 0 — 准备（已完成）
- 克隆仓库、下载官方 Logo、跑通本地 Go 工具链（`GOPATH/GOCACHE` 置于工作区，CGO/gcc 可用）。
- 基线构建 v7.2.138 通过；v7.3.20 零改动构建通过。

### Phase 1 — 依赖升级
- `go.mod`：`github.com/router-for-me/CLIProxyAPI/v7 v7.2.138 → v7.3.20`（`go get` + `go mod tidy`）。
- `go.sum` 同步。
- 验收：`go build ./...`、`go test ./...` 全绿。

### Phase 2 — 实现 QuotaProvider 能力
改动文件：
- `internal/plugin/plugin.go`
  - `capabilities` 结构体新增 `QuotaProvider bool \`json:"quota_provider"\``，注册时置 `true`。
  - `HandleCall` 新增四个分发：
    - `pluginabi.MethodQuotaIdentifier` → `okEnvelope(map[string]string{"identifier": ProviderID})`
    - `pluginabi.MethodQuotaDescribe` → `okEnvelope(QuotaDescribeResponse{SupportedProviders:["commandcode"], DisplayName:"CommandCode", SupportsReset:false})`
    - `pluginabi.MethodQuotaFetch` → 解码 `rpcQuotaFetchRequest{QuotaFetchRequest, HostCallbackID}`，调用新逻辑
    - `pluginabi.MethodQuotaReset` → 返回 `{Success:false, Message:"CommandCode 不支持重置额度"}`
- **新增** `internal/plugin/quota_provider.go`：
  - `quotaProvider` 结构（持有 `*Manager`）实现四个方法；`Identifier()=="commandcode"`。
  - `DescribeQuota`：SupportedProviders=[commandcode]，DisplayName=CommandCode，SupportsReset=false。
  - `FetchQuota(ctx, req)`：
    1. 取 key：`selectedAuthKey(req.Attributes, req.Metadata)`（已有 helper，兼容 host.auth.save 早期 Metadata 与目录 watcher 后的 Attributes）；兜底从 `req.StorageJSON` 解析 `api_key`。
    2. 取 base-url：`m.cfg.BaseURL`。
    3. 复用 `fetchQuota`（抽象为接受 `httpDoer` 接口：`*HostBridge` 与 `pluginapi.HostHTTPClient` 均满足 `Do`）。
    4. 映射：
       - `subscription{plan: 显示名, tierName: 显示名, tierId: 原始 planId}`（需让 `fetchQuota` 额外保留 planId）。
       - `summary`：`plan_credits_remaining`（剩余套餐额度）、`purchased_credits`（加油包，>0 才输出）。
       - `groups`：
         - 「套餐额度」→ 1 个 bucket：`window="month"`, `remainingFraction=(creditsLeft+purchased)/pool`, `resetTime=currentPeriodEnd`, `description="剩余 x / 共 y"`。
         - 「速率限制」→ 2 个 bucket：`5h`、`weekly`，`remainingFraction=cap>0?(cap-used)/cap:1`, `resetTime=resetAt`, `description="used/cap"`；`unlimited` 时 description 标注不限。
    5. 错误：返回 `okEnvelope(QuotaFetchResponse{})` 之外的错误应以 envelope error 呈现，前端显示「刷新失败」。
  - `ResetQuota`：`{Success:false, Message:"CommandCode 不提供重置额度接口"}`。
- **回调上下文（重要）**：C-ABI 下 `QuotaFetchRequest.HTTPClient` 不参与序列化，host 改为下发 `host_callback_id`。为让配额查询走 host 的凭据绑定传输策略，扩展 `HostBridge`：
  - 增加可选 `callbackID` 字段与 `WithCallback(id) *HostBridge`（浅拷贝），`hostHTTPReq` 增加 `host_callback_id`/`operation_id`。
  - `MethodQuotaFetch` 处理时用 `m.bridge.WithCallback(req.HostCallbackID)` 发起 `host.http.do`。
  - 兜底：`req.HTTPClient` 非空（进程内测试）时优先使用。
- 兼容性：保留现有 `/quota-usage`、`/accounts` 管理接口与页面（账户新增仍需要）；原生配额与自建页面可并存。

### Phase 3 — WebUI 使用官方 Logo
改动文件：
- `resources/quota_page.html`、`resources/oauth_page.html`
  - header 增加官方文字标志。做法：将 `wordmark-dark.svg`（浅底）与 `wordmark-light.svg`（深底）内联为两个 `<svg>`，用 CSS 按 `data-theme`/`prefers-color-scheme` 切换；避免额外网络请求与主题错配。
  - 保留现有标题文字作为 `aria-label`/副标题，Logo 作为品牌主视觉。
  - （可选）把主色由米灰 `#8b8680` 调整为品牌 Burple `#8C4EDD`，使整体与官方品牌一致；如不需要可跳过。
- 资产留存：`resources/brand/`（或 `assets/brand/`）保留 SVG 源文件与来源说明（品牌页要求使用官方原样资产，不改色）。

### Phase 4 — 测试
- 新增 `internal/plugin/quota_provider_test.go`：
  - `Identifier`/`DescribeQuota` 字段断言。
  - `FetchQuota`：mock `httpDoer` 返回 credits/subscriptions/whoami 样本 → 断言 subscription/summary/groups 映射（含 unlimited、无 plan 额度时省略 month）。
  - key 缺失、上游 4xx、JSON 损坏等错误路径。
  - `ResetQuota` 不支持语义。
- 更新 `plugin_test.go` / `integration_test.go` 的 capabilities 断言，加入 `QuotaProvider`。
- `go test ./...`、`go vet ./...`、可选 `go test -race ./...`。
- 若条件允许，用本地 mock host 跑一次原生 `quota.fetch` 端到端（参考现有 `tests/oauth_host.cjs` 思路）。

### Phase 5 — 文档与发布
- README（英/中）「Quota page」章节改写：说明已注册 `QuotaProvider`，额度显示在认证文件卡片与配额管理页；保留自建页面用于账户新增。
- CHANGELOG / RELEASE_NOTES 增补条目；`pluginVersion` 常量升级（建议 **v0.2.0**，属新能力）。
- 构建三平台产物：`commandcode-go-cliproxyapi_0.2.0_{linux/amd64,darwin/arm64,windows/amd64}.zip` + `checksums.txt`，保持插件 ID/仓库地址不变。

## 4. 验收标准
1. `go build ./...`、`go test ./...`、`go vet ./...` 全绿。
2. 安装到 CPA v7.3.20+ 后：认证文件卡片出现「刷新额度」，能显示套餐名、月度剩余、5 小时/每周窗口与重置时间；「配额管理」页可「全部获取」。
3. 插件注册能力包含 `quota_provider=true`；`quota.identifier` 返回 `commandcode`。
4. 账户新增/校验/保存流程不受影响；禁用/删除仍在认证文件页。
5. 账户页与 OAuth 页显示官方 Logo，浅/深主题均清晰可见。
6. 不改变模型路由、协议转换、推理保留等既有能力。

## 5. 风险与回滚
- **兼容性**：原生配额要求 host ≥ v7.3.x 且管理前端支持插件配额卡；旧版 CPA 上插件仍可工作（能力字段被忽略，自建页面仍可用）。需在 README 标注最低版本。
- **重复展示**：自建页面与原生卡片同时显示额度属可接受冗余；如不希望重复，可后续精简页面。
- **回调上下文**：若 `host.http.do` 未带 callback id 也能工作（现有配额页即如此），可作为兜底；带上 callback id 更符合 host 传输策略。
- **回滚**：改动集中在少数文件；保留 v0.1.4 分支/tag，必要时回退。

## 6. 待确认决策
1. **目标 host 版本**：采用 **v7.3.20（推荐，已验证零改动升级）**，还是直接上 v8？
2. **自建页面去留**：保留「账户与额度」页（推荐，用于账户新增，额度与原生并存），还是精简为纯「账户管理」并把额度完全交给原生？
3. **品牌配色**：是否顺带把页面主色对齐官方 Burple `#8C4EDD`（推荐，视觉更统一），还是仅替换 Logo、配色不动？
