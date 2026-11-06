# 插件动态注入官方 Logo 到 CPA 自带面板 —— 方案规划（未落实）

> 状态：仅规划，不改代码、不改面板。
> 日期：2026-11-07 · 工作区：/workspace/89bt

## 1. 现状与约束（已实测）

- CPA 自带管理面板（CPAMC）是**运行时下载的静态单文件**：
  - 容器内路径 `/CLIProxyAPI/static/management.html`（约 3.78 MB），由 `management.panel-github-repository` 指向的仓库自动更新。
  - 插件无法修改它；手工补丁会被面板自动更新覆盖（本机已回滚补丁，md5 与原始备份一致）。
- 额度卡图标逻辑（面板前端）：
  - `QuotaCard` 用 `x = RO(provider, theme)`；
  - `RO` 查硬编码映射 `DO`：`antigravity / aistudio / claude / codex / meta / devin / gemini / xai / iflow / kimi / qwen / vertex`；
  - 没有 `commandcode` → 返回 `null` → 退化成首字母灰色徽标 "C"。
- 分类逻辑：`ProviderTabs` 是固定类型集合 + 通用 `plugin` 桶；所有插件额度统一显示为 **Plugin**。
- 插件注册元数据里已有 `pluginapi.Metadata.Logo` 字段，但当前面板只用于「插件」页/侧边栏，**不用于额度卡/分类 tab**。
- 结论：这是**管理面板前端**的限制，不是插件配额接口问题；插件返回的额度数据正确。

## 2. 目标

不手工改面板文件，让插件（或插件 + 上游小改动）**动态提供**官方 Logo，显示在额度卡/分类 tab；面板自动更新后仍生效。

## 3. 方案对比

### 方案 A（推荐，需上游 CPAMC 小改）：插件元数据 Logo + 前端回退
- 插件侧：`registrationEnvelope.Metadata.Logo` 填官方 Logo（data URI 或 URL）；`DescribeQuota.DisplayName` 已是 `CommandCode`。
- 上游面板：`RO(provider)` 在 `DO` 未命中时，回退到 `GET /v8/management/plugins` 中该 provider 的 `logo`；`ProviderTabs` 的 `plugin` 分类可用插件 `display_name`（或按 quota provider 拆 tab）。
- 优点：一次上游改动，所有插件受益；插件只提供标准元数据；面板更新后自动生效。
- 缺点：依赖上游发版。
- 工作量：插件侧约 1 个常量；上游前端小改。

### 方案 B（需上游）：插件资源路由托管 Logo
- 插件注册管理资源（如 `/v0/resource/plugins/<id>/logo.svg`）返回官方 SVG。
- 上游面板在额度卡读取插件 logo URL（`/plugins` 的 `logo` 字段或新增字段）。
- 本质与 A 相同（前端支持），差别是 Logo 由插件托管、无需外链/CORS。

### 方案 C（插件单方面运行时 DOM 注入；不推荐）
- 插件页面（iframe，同源）加载时向 `window.parent` 注入 `<style>`/脚本，或直接改 React 渲染出的卡片 DOM 补图标。
- 缺点：只在插件页面打开时生效；面板重渲染即丢失；依赖 DOM 结构，易碎；可能触发 CSP；不是稳定方案。

### 方案 D（运维侧，非插件）：持久化面板补丁
- 把改好的 `management.html` 放到宿主目录并 bind mount 到 `/CLIProxyAPI/static`，同时固定/禁用 `panel-github-repository` 自动更新。
- 优点：立即生效、稳定。
- 缺点：维护面板分叉；CPA 升级需同步；不属于插件能力。

### 方案 E（上游产品化）：声明式图标/分类字段
- 在 `pluginapi.Metadata` 或 `QuotaDescribeResponse` 增加 `icon` / `category` 字段；CPAMC 按此渲染。
- 最干净，但需要 CPA SDK + 面板协同改动。

## 4. 推荐路线

1. **短期（插件侧，零风险）**：设置 `Metadata.Logo` 为官方 Logo（data URI），至少让「插件」页/侧边栏显示官方 Logo，并为方案 A 做准备。
2. **中期（上游 PR）**：CPAMC 的 `RO` 增加「插件 logo 回退」；`ProviderTabs` 的 plugin 分类使用插件 `display_name`。插件侧无需再改。
3. **备选（运维）**：若急需额度卡 Logo，用方案 D 的 bind mount 补丁，但需记录升级维护成本。
4. **不建议**方案 C。

## 5. 验收标准（若实施）

- 额度卡显示官方 CommandCode 图标；分类 tab 显示 `CommandCode`（或至少 Plugin tab 用官方图标）。
- 面板自动更新后仍生效（方案 A/B/E）。
- 不改 CPA 二进制、不手工改面板（方案 A/B/E）。

## 6. 本次范围

- 仅规划；未改插件代码、未改面板。
- 本机临时面板补丁已回滚（原始 `management.html` 已恢复，`commandcode` 计数 0）。

## 7. 补充：分类（tab）到底能不能改？（2026-11-07 实测前端代码）

结论：**插件侧改不了分类；必须改管理面板前端（CPAMC）或上游。**

面板里的关键逻辑：

- 固定分类列表：`yO=[meta,vertex,aistudio,antigravity,xai,claude,codex,devin,kimi]`
- 排除集合：`bO=new Set([all,unknown,empty])`
- 动态 tab 构造：`jO(types)` 会把不在 `yO`/`bO` 里的类型追加为 tab。所以**理论上**只要某个额度条目类型是 `commandcode`，就会出现独立 tab。
- 但插件额度统一走一个通用描述符：
  ```
  TA = { type: "plugin", i18nPrefix: "plugin_quota", filterFn: e => eh(e) && !th(e), ... }
  ```
  所有插件 `QuotaProvider` 的凭据都归到这个描述符，因此 tab 类型恒为 `plugin`。
- tab 标签：`IO(l, "plugin")` 查 i18n `auth_files.filter_plugin`，不存在 → 回退为首字母大写 **"Plugin"**。
- 卡片图标/标题则不同：
  ```
  b = t.type === "plugin" ? String(d.quotaProvider ?? d.quota_provider ?? d.provider ?? d.type ?? "plugin") : t.type
  x = RO(b, theme)   // b = "commandcode"
  S = IO(l, b)       // "Commandcode"（i18n 缺失时的回退）
  ```
  也就是说**额度卡片其实已经用 `commandcode` 去查图标了**，只是 `DO` 映射里没有 `commandcode` → `RO` 返回 null → 首字母兜底；tab 仍用描述符类型 `plugin`。

由此：

- **分类 tab（Plugin → CommandCode）**：插件无法影响。需要上游 CPAMC 做以下之一：
  1. 插件额度 tab 用插件 `display_name` 命名（例如只有一个插件额度 provider 时直接显示 `CommandCode`）；
  2. 按 `quotaProvider` 为插件额度生成独立描述符/tab（复用 `jO` 的动态 tab 能力）；
  3. 新增 SDK/面板字段（如 `QuotaDescribeResponse.Category` / `Metadata.Category`）由插件声明。
- **卡片图标**：只差一个映射。上游 CPAMC 只要：
  1. 在 `DO` 表加 `commandcode` 图标；或
  2. 让 `RO` 在 `DO` 未命中时回退到插件 `Metadata.Logo`（或 `/plugins` 的 `logo` 字段）。
  插件侧无需改（或仅设置 `Metadata.Logo`）。

因此「分类」不是插件能力问题，是面板前端的产品设计；要么接受 Plugin，要么走上游 PR / 运维补丁（方案 D）。


## 8. 更优雅的实现：用 CPA 官方面板仓库配置（推荐替代手改）

CPA 原生支持自定义管理面板来源，不需要改容器文件：

- `management.panel-github-repository`：可填 `https://github.com/<owner>/<repo>` 或 `https://api.github.com/repos/<owner>/<repo>/releases/latest`。
- 下载逻辑（`internal/managementasset/updater.go`）：解析为 **latest release**，从中找名为 `management.html` 的 asset，校验 digest 后原子写入 `/CLIProxyAPI/static/management.html`。
- `management.disable-auto-update-panel: true` 可关闭自动更新（仅首次缺失时下载），用于固定版本。

### 推荐落地形态

1. **上游 PR（最终形态，最优雅）**
   - CPAMC 的 `RO(provider, theme)` 在 `DO` 未命中时，回退到插件 `Metadata.Logo`（或 `/v8/management/plugins` 的 `logo`）。
   - 插件额度 tab 用插件 `display_name`（或按 `quotaProvider` 拆 tab）。
   - 合并后：插件只需设置 `Metadata.Logo`；CPA 自动更新面板即可，无需 fork/补丁。

2. **Fork + Release（不改上游、可自控）**
   - Fork `Cli-Proxy-API-Management-Center`，在 `DO` 表加 `commandcode`（或实现插件 logo 回退）+ tab 命名。
   - 构建并在 GitHub Release 上传 `management.html`。
   - 配置：`management.panel-github-repository: https://github.com/<you>/<panel-fork>`（可选 `disable-auto-update-panel: true` 固定）。
   - CPA 自动下载/更新；容器重建也不丢；回滚只需把配置改回上游 URL。
   - 代价：需跟随上游面板更新 rebase。

3. **插件侧标准元数据（无论哪种都该做）**
   - 在注册 `Metadata.Logo` 填官方 logo（data URI 或 URL）。
   - 当前面板会用它显示「插件」页/侧边栏图标；也是方案 1 的前置条件。

### 与手改方案的对比
- 手改 `management.html` / bind mount / DOM 注入：不可重复、易被覆盖、需维护分叉文件。
- 官方仓库配置：配置化、可回滚、可自动更新，属于 CPA 支持的正式能力。

