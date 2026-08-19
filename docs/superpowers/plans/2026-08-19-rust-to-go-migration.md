# Rust → Go 迁移实施计划（停机方式，GUI 不变）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `crates/core` + `apps/cli`（约 8.6k 行 Rust）整体移植为 Go 实现，前端 `frontends/app/`、LuCI 包、安装脚本全部不动，API 契约逐字节对齐，最后归档 Rust 代码完成切换。

**Architecture:** 停机方式——Rust 代码立即冻结（只修致命 bug），Go 版在新目录 `cmd/` + `internal/` 下自底向上重建：config → ikuai 客户端 → 更新流程 → runtime → app 服务 → Web 服务 → CLI。全程以移植测试验证功能对等；达成后一次性切换（Rust 归档至 `rust_archive/`，Go 成为根实现）。桌面 GUI（Tauri 壳）改为启动 Go 二进制 sidecar 并加载其 Web 界面，前端代码零改动。

**Tech Stack:** Go 1.25（标准库 `net/http` + `embed`）、`gopkg.in/yaml.v3`、`github.com/robfig/cron/v3`；无其他第三方依赖（归档 `golang_archive/pkg/ikuai_api4` 可直接搬运参考）。

## Global Constraints

- **二进制名保持 `ikuai-bypass`**（install.sh、LuCI libexec、fileuni/ipkg 清单、CI 资产名全部引用它，不改）。
- **前端零改动**：`frontends/app/` 目录在本计划中不允许修改（唯一例外：Task 11 之后如需调整 Tauri 构建命令，只动 `package.json` scripts 之外的构建配置）。
- **API 契约冻结**：15 个 HTTP 端点的方法/路径/请求/响应字段（见 Task 8 表格）与 16 个 Tauri 命令名（见 Task 11）必须逐字段一致，包括 `yaml_text` vs `yamlText`、`clean_tag` vs `cleanTag` 的命名差异。
- **配置 schema 冻结**：yaml 键名（kebab-case 与 `AddErrRetryWait`/`MaxNumberOfOneRecords` 的混合大小写）与默认值不变，见 Task 1。
- **规则托管标记不变**：`IKB` 前缀、`IkuaiBypass` 新备注、`joyanhui/ikuai-bypass` 与 `IKUAI_BYPASS` 旧标记兼容识别（`crates/core/src/ikuai/types.rs:6-17` 的四个常量原样移植）。
- **Safe-Before 结构性规则**：每个更新函数的第一步必须是下载，下载失败时在任何查询/编辑/新增/删除 API 调用之前 return。
- **日志面向用户为中文，API 与内部错误信息为英文**（沿用现状）。
- Go module 名：`github.com/FelixJI/iKuai-Toolbox`；所有代码注释双语法（中文 + English）。
- 测试框架：标准 `testing` + `net/http/httptest`，不用 testify。
- 行为基准：一切以当前 Rust 实现为规格（文件:行号在任务中给出），`golang_archive` 仅作参考底稿（它有编译错误且缺 protocol/proxy 等新功能）。

## 目标文件结构（迁移完成后）

```text
ikuai-bypass(仓库根)/
├── go.mod / go.sum                  # module github.com/FelixJI/iKuai-Toolbox
├── cmd/ikuai-bypass/main.go         # CLI 入口（对应 apps/cli/src/main.rs + lib.rs）
├── internal/
│   ├── config/                      # 对应 crates/core/src/config.rs
│   │   ├── config.go  duration.go  save.go  paths.go   # paths.rs 并入
│   ├── ikuai/                       # 对应 crates/core/src/ikuai/*
│   │   ├── client.go  types.go  utils.go  tag_name.go  clean.go
│   │   ├── custom_isp.go  ip_group.go  ipv6_group.go
│   │   ├── stream_domain.go  stream_ipport.go
│   ├── update/                      # 对应 crates/core/src/update.rs + runner.rs + session.rs + router.rs
│   │   ├── update.go  modules.go  session.go  router.go
│   ├── runtime/                     # 对应 crates/core/src/runtime.rs
│   │   ├── runtime.go  cron_norm.go  broker.go
│   ├── logger/                      # 对应 crates/core/src/logger.rs
│   ├── netx/                        # 对应 crates/core/src/net.rs（避开标准库 net 名）
│   │   └── plan.go
│   ├── app/                         # 对应 crates/core/src/app/*
│   │   ├── github.go  diagnostics.go  clean.go  config_meta.go  fetch.go  url.go
│   └── webserver/                   # 对应 apps/cli/src/web.rs + embedded.rs
│       ├── server.go  routes.go  auth.go  sse.go  static.go
├── apps/gui/                        # Tauri 壳保留，lib.rs 重写为 sidecar 启动器（Task 11）
├── apps/integration-tests-go/       # Go 版集成测试 + 模拟器（Task 10）
├── rust_archive/                    # 原 crates/ + apps/cli + apps/gui 旧 Rust 代码（Task 13 移入）
├── frontends/app/                   # 不动
├── packaging/  docs/  .github/      # 除 Task 12 指定处外不动
└── config.yml                       # 不动（作为内嵌默认配置的来源）
```

## 停机方式约定

- Task 0 完成后，`crates/`、`apps/cli/`、`apps/gui/src/` 冻结：迁移期间发现的 Rust bug 只记录到 `docs/go-migration-notes.md`，在 Go 版修复，不回改 Rust（致命安全漏洞除外）。
- 全程在分支 `go-migration` 上开发；Task 13 才合回 main 并发版。
- 前端联调方式：`cd frontends/app && bun run build` 产出 `dist/`，Go 二进制 embed 该目录后 `GET /` 即整站。

---

### Task 0: 冻结与脚手架

**Files:**
- Create: `go.mod`、`cmd/ikuai-bypass/main.go`（最小可编译）、`docs/go-migration-notes.md`
- Branch: `go-migration`

**Interfaces:**
- Produces: module `github.com/FelixJI/iKuai-Toolbox`；`main.go` 仅打印版本并退出（后续任务填充）。

- [ ] **Step 1: 建分支与记录文件**

```bash
git checkout -b go-migration
printf '# Go 迁移期间的 Rust 侧缺陷记录 / Rust-side defects found during migration\n\n| 日期 | Rust 位置 | 问题 | Go 侧处理 |\n|---|---|---|---|\n' > docs/go-migration-notes.md
```

- [ ] **Step 2: 初始化 module 与最小入口**

`go.mod`：
```
module github.com/FelixJI/iKuai-Toolbox

go 1.25
```

`cmd/ikuai-bypass/main.go`：
```go
// iKuai-Toolbox CLI 入口 / CLI entry point
package main

import "fmt"

const Version = "5.0.0-go.1"

func main() { fmt.Println("ikuai-bypass", Version) }
```

- [ ] **Step 3: 验证编译与测试基建**

Run: `go build ./... && go vet ./...`
Expected: 无输出，退出码 0

- [ ] **Step 4: Commit**

```bash
git add go.mod cmd docs/go-migration-notes.md && git commit -m "chore(go): 初始化 Go module 脚手架"
```

---

### Task 1: 配置层（config + paths + duration 兼容）

**Files:**
- Create: `internal/config/config.go`、`internal/config/duration.go`、`internal/config/save.go`、`internal/config/paths.go`
- Test: `internal/config/config_test.go`、`internal/config/duration_test.go`、`internal/config/save_test.go`
- 规格：`crates/core/src/config.rs`（523 行）、`crates/core/src/paths.rs`（112 行）

**Interfaces:**
- Produces（后续所有任务依赖）:

```go
type ProxyMode string // "custom" | "system" | "smart"

type ProxyConfig struct {
    Mode ProxyMode `yaml:"mode" json:"mode"`
    URL  string    `yaml:"url"  json:"url"`
    User string    `yaml:"user" json:"user"`
    Pass string    `yaml:"pass" json:"pass"`
}

type CustomIspItem    struct{ Tag, URL string }             // yaml: tag / url
type StreamDomainItem struct{ Interface, SrcAddr, SrcAddrOptIpGroup, URL, Tag string }
// yaml: interface / src-addr / src-addr-opt-ipgroup / url / tag
type IpGroupItem      struct{ Tag, URL string }
type Ipv6GroupItem    struct{ Tag, URL string }
type StreamIpPortItem struct {
    OptTagName string `yaml:"opt-tagname"`; Type string `yaml:"type"`
    Interface  string `yaml:"interface"`;  Nexthop string `yaml:"nexthop"`
    SrcAddr    string `yaml:"src-addr"`;   SrcAddrOptIpGroup string `yaml:"src-addr-opt-ipgroup"`
    SrcAddrInv int64  `yaml:"src-addr-inv"`; IPGroup string `yaml:"ip-group"`
    DstAddrInv int64  `yaml:"dst-addr-inv"`; Prio int64 `yaml:"prio"`
    Mode int64 `yaml:"mode"`; IfaceBand int64 `yaml:"ifaceband"`; Protocol string `yaml:"protocol"`
}
type WebUiConfig struct{ Port, User, Pass string; Enable bool; CdnPrefix string } // port/user/pass/enable/cdn-prefix
type MaxNumberOfOneRecordsConfig struct{ Isp, Ipv4, Ipv6, Domain int }            // Isp/Ipv4/Ipv6/Domain，默认 5000/1000/1000/5000

type Config struct {
    IkuaiURL string `yaml:"ikuai-url"`; Username string `yaml:"username"`; Password string `yaml:"password"`
    Cron string `yaml:"cron"`
    AddErrRetryWait Duration `yaml:"AddErrRetryWait"`; AddWait Duration `yaml:"AddWait"`
    RunMode string `yaml:"run-mode"`; Module string `yaml:"mode"` // 默认 cronAft / ispdomain
    GithubProxy string `yaml:"github-proxy"`; Proxy ProxyConfig `yaml:"proxy"`
    CustomIsp []CustomIspItem `yaml:"custom-isp"`; StreamDomain []StreamDomainItem `yaml:"stream-domain"`
    IpGroup []IpGroupItem `yaml:"ip-group"`; Ipv6Group []Ipv6GroupItem `yaml:"ipv6-group"`
    StreamIpPort []StreamIpPortItem `yaml:"stream-ipport"`
    WebUI WebUiConfig `yaml:"webui"`; MaxNumberOfOneRecords MaxNumberOfOneRecordsConfig `yaml:"MaxNumberOfOneRecords"`
}

// Duration：humantime 双格式兼容（"30s" 字符串 或 纳秒整数），见 config.rs duration_compat L12-74
type Duration time.Duration

func LoadFromYAMLString(s string) (*Config, error)
func LoadFromPath(path string) (*Config, error)
func EmbeddedDefaultYAML() string                    // go:embed 根目录 config.yml
func WriteEmbeddedDefaultToPath(path string) error
func (c *Config) ApplyDefaults()                     // 对应 config.rs L296-386：webui 端口 19001、cdn 前缀、stream_ipport inv 归一化 0/1、proxy 默认 smart 等
func (c *Config) SaveToPath(path string) error
func ValidateAndSaveRawYAML(raw string, path string) (*Config, error) // 解析校验后按原文写盘
func YAMLHasExplicitMode(raw string) bool
func ValidateSavePath(path string) error             // 仅 .yml/.yaml、拒绝软链接
func DefaultConfigPath() string                      // 对应 paths.rs：Linux ~/.config/ikuai-bypass/config.yml 等
```

实现要点（逐条对齐 config.rs）：
1. `ProxyMode` 别名：`disabled`→system；`onlyGithubApi`/`only-github-api`/`only_github_api`→smart（自定义 `UnmarshalYAML` + JSON UnmarshalJSON 同步做，前端两种命名都会发）。
2. `Custom 且 URL 为空 → "http://127.0.0.1:7890"`。
3. `apply_defaults` 中 stream_ipport 的 inv 字段非 0/1 值归一化（对应 config.rs L296-386）。
4. 安全写盘：`os.OpenFile(path, O_WRONLY|O_CREATE|O_TRUNC, 0o600)`（Unix）；拒绝 symlink 用 `os.Lstat` 判 `mode&os.ModeSymlink != 0`。
5. `Duration.UnmarshalYAML`：先试整数（纳秒），再试 `time.ParseDuration`。

- [ ] **Step 1: 写 duration 失败测试**

```go
// internal/config/duration_test.go
package config

import "testing"

func TestDurationDualFormat(t *testing.T) {
    cases := []struct{ in string; wantSec int }{
        {"30s", 30}, {"1m30s", 90}, {"5000000000", 5}, {"0", 0},
    }
    for _, c := range cases {
        var d Duration
        if err := yamlUnmarshalStr(c.in, &d); err != nil { t.Fatalf("%s: %v", c.in, err) }
        if got := int(time.Duration(d) / time.Second); got != c.wantSec {
            t.Errorf("%s => %ds, want %ds", c.in, got, c.wantSec)
        }
    }
}
```
（`yamlUnmarshalStr` 是测试助手：`yaml.Unmarshal([]byte(s), v)`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestDuration -v`
Expected: FAIL（Duration 未实现）

- [ ] **Step 3: 实现 duration.go（UnmarshalYAML/MarshalYAML/JSON 双格式）**

- [ ] **Step 4: 跑测试确认通过；Commit `feat(go): 配置 Duration 双格式兼容`**

- [ ] **Step 5: 写 config 解析测试（用真实默认配置做夹具）**

```go
// internal/config/config_test.go 关键用例
func TestParseEmbeddedDefault(t *testing.T)          // EmbeddedDefaultYAML() 能解析，Module=="ispdomain"，RunMode=="cronAft"
func TestProxyModeAliases(t *testing.T)              // onlyGithubApi/only-github-api/only_github_api/disabled 四个输入归一化
func TestCustomProxyDefaultURL(t *testing.T)         // mode=custom 且 url 空 => 127.0.0.1:7890
func TestApplyDefaultsFillsWebUI(t *testing.T)       // 空 webui => port 19001, cdn jsdelivr
func TestStreamIpPortInvNormalize(t *testing.T)      // src-addr-inv: 5 => 1（对齐 config.rs L296-386）
func TestConfigJSONKeysMatchContract(t *testing.T)   // 序列化 JSON 顶层键必须包含 "ikuai-url","run-mode","AddErrRetryWait","MaxNumberOfOneRecords","custom-isp","stream-ipport","webui"（前端 fromBackendMeta 依赖）
```

- [ ] **Step 6: 实现 config.go + ApplyDefaults；跑测试；Commit `feat(go): 配置结构与默认值`**

- [ ] **Step 7: 写 save/paths 测试（ValidateSavePath 拒绝非 yaml 后缀；tmp 目录写入后 0600 权限；DefaultConfigPath 在各 GOOS 下的路径形状——用 GOOS 变量注入法测）**

- [ ] **Step 8: 实现 save.go + paths.go；跑 `go test ./internal/config/`；Commit `feat(go): 配置安全写盘与默认路径`**

---

### Task 2: 爱快客户端 + 标签工具（登录/call/tag_name/utils）

**Files:**
- Create: `internal/ikuai/client.go`、`internal/ikuai/types.go`、`internal/ikuai/tag_name.go`、`internal/ikuai/utils.go`
- Test: `internal/ikuai/client_test.go`、`internal/ikuai/tag_name_test.go`
- 规格：`crates/core/src/ikuai/types.rs`、`tag_name.rs`（171 行）、`utils.rs`；参考 `golang_archive/pkg/ikuai_api4/ikuai.go`（可直接搬骨架）

**Interfaces:**
- Produces:

```go
// types.go —— 四个常量 + 客户端
const (
    NamePrefixIKB      = "IKB"
    CommentIkuaiBypass = "IKUAI_BYPASS"
    LegacyRepoComment  = "joyanhui/ikuai-bypass"
    NewComment         = "IkuaiBypass"
    CleanModeAll       = "cleanAll"
)
func ManagedCommentMarkers() [3]string // {NewComment, LegacyRepoComment, CommentIkuaiBypass}

type IKuaiError struct{ Kind string; Msg string } // Kind: "http"|"api"|"invalid_response"

type IKuaiClient struct{ /* baseUrl string; hc *http.Client（共享 cookiejar）*/ }
func NewIKuaiClient(baseUrl string) (*IKuaiClient, error) // connect 5s / 总 30s / 强制直连（Transport.Proxy=nil）
func (c *IKuaiClient) Login(username, password string) error
// POST {base}/Action/login，body {passwd: md5hex(pwd), pass: base64("salt_11"+pwd), remember_password:"", username}
// 响应 code!=0 => IKuaiError{api}
func (c *IKuaiClient) Call(funcName, action string, param map[string]any, out *CallResp) error
// POST {base}/Action/call，body CallReq{func_name, action, param}；CallResp{Code, Message, Results, RowID}；
// Results.Data 为 json.RawMessage 由各模块自行解码；code!=0 => IKuaiError{api, Message}

// tag_name.go —— 字节级对齐 tag_name.rs
func SanitizeTagName(raw string) string
func BuildTagName(raw string) string                          // "IKB"+token，UTF-8 安全截到 15 字节（不切多字节字符）
func BuildIndexedTagName(raw string, index int64) string      // 尾缀 strconv(index+1)
func BuildIndexedIpGroupTagName(raw string, index int64) string // +"R"+2位确定性字母（md5%26，避开'R'）
func MatchTagNameFilter(filterTag, currentName, legacyComment string) bool // tag_name.rs L114-139

// utils.go
func MD5Hex(s string) string
func ToStringList(v json.RawMessage) []string // object 取 gp_name/name/ip/ipv6
func CategorizeAddrs(addrs []string) (custom []string, objects []string) // IKB 前缀或无点冒横线 => object
```

- [ ] **Step 1: 写 tag_name 行为测试（这是全系统身份识别的地基，必须字节级对齐）**

```go
// internal/ikuai/tag_name_test.go 关键用例（数值来自 tag_name.rs 行为）
func TestBuildTagNameTruncation(t *testing.T) {
    // 15 字节 UTF-8 安全截断
    if got := BuildTagName("运营商自定义超长名称"); len(got) <= 16 && !strings.HasPrefix(got, "IKB") {} else {
        t.Fatalf("got %q", got) }
    // 精确断言：对 "abcd" => "IKBabcd"；对 20 个 ASCII 字符 => "IKB"+12 字符
    if BuildTagName("abcd") != "IKBabcd" { t.Fatal() }
    if got := BuildTagName(strings.Repeat("x", 20)); len(got) != 15 { t.Fatalf("len=%d", len(got)) }
}
func TestBuildIndexedTagName(t *testing.T)      // index 0 => 尾缀 "1"；index 9 => "10"
func TestIpGroupHashSuffixStable(t *testing.T)  // 同输入同输出、不含 'R'、恰好 2 字母
func TestMatchTagNameFilter(t *testing.T)       // 覆盖 tag_name.rs L114-139：IKB 前缀命中、截断兜底、legacy comment 命中
```

- [ ] **Step 2: 跑测试失败 → 实现 tag_name.go → 通过 → Commit `feat(go): IKB 标签构建与匹配`**

- [ ] **Step 3: 写 client 测试（httptest 假爱快）**

```go
// internal/ikuai/client_test.go
func TestLoginFlow(t *testing.T) {
    // httptest 服务断言：POST /Action/login 的 body 字段齐全；
    // passwd == md5hex("pass"); pass == base64("salt_11"+"pass")；code 0 成功 / code 1 返回 api 错误
}
func TestCallEnvelope(t *testing.T) {
    // POST /Action/call body func_name/action/param 透传；code!=0 => 错误消息为爱快 message；
    // cookie 在 Login 后的第二次 Call 仍带上（jar 生效）
}
```

- [ ] **Step 4: 实现 client.go（cookiejar.New()、http.Client{Timeout:30s, Transport: 手工 Transport{Proxy:nil, DialContext: 5s 超时}}）→ 测试通过 → Commit `feat(go): 爱快 API 客户端`**

---

### Task 3: 爱快规则 CRUD 五模块 + 分片索引解析

**Files:**
- Create: `internal/ikuai/custom_isp.go`、`ip_group.go`、`ipv6_group.go`、`stream_domain.go`、`stream_ipport.go`、`clean.go`
- Test: `internal/ikuai/crud_test.go`（五个模块共用 httptest 假服务）
- 规格：`crates/core/src/ikuai/` 下同名 .rs；每个函数的参数字段落以 Rust 版为准（探索报告 §3 有完整清单）；`golang_archive/pkg/ikuai_api4/*.go` 是同名 Go 底稿可直接对照改

**Interfaces:**
- Produces（签名以 custom_isp 为例，其余四模块结构对称——ip_group/ipv6_group/stream_domain 用 `map[int64]int64`（chunk→id），stream_ipport 用 `map[string]int64`（tagname→id））:

```go
func ShowCustomIspByTagName(api *IKuaiClient, tagName string) ([]CustomIspData, error)
func AddCustomIsp(api *IKuaiClient, tag, ipgroup string, index int64) error
func EditCustomIsp(api *IKuaiClient, tag, ipgroup string, index, id int64) error
func DelCustomIsp(api *IKuaiClient, idCSV string) error
func GetCustomIspMap(api *IKuaiClient, tag string) (map[int64]int64, error) // chunk_index -> id
func DelCustomIspAll(api *IKuaiClient, cleanTag string) error
func BuildCustomIspChunkComment(index int64) string  // "IkuaiBypass" / "IkuaiBypass-N"
func ParseCustomIspChunkIndexFromComment(comment string) (int64, bool) // custom_isp.rs L130-158 三标记兼容
func ParseCustomIspChunkIndexFromName(name, tag string) (int64, bool)

// ip_group.go 额外：
func GetIpGroupMapWithName(api, tag) (map[int64]IpGroupEntry, error) // {ID int64; Name string}
func ResolveRuleReferenceIpGroupNames(api, name) ([]string, error)   // 精确名优先，否则按 IKB 标签展开分片
func ParseIndexFromGroupName(tag, groupName string) (int64, bool)     // 前缀剥离+尾数字兜底（截断兼容）

// clean.go
func IsManaged(comment, name string) bool        // name IKB 前缀 OR comment 含任一 marker
func MatchCleanTag(cleanTag, legacyTagName, currentTagName string) bool
```

add/edit 请求体固定字段（照抄 Rust）：custom_isp 用 `name/ipgroup/comment`；route_object 用 `group_name/type/group_value[{ip|ipv6,comment}]/comment:""`（v4 故意留空）；stream_domain 固定 `enabled:"yes", prio:31, time: 周 1234567 00:00-23:59, comment:NewComment`；stream_ipport 默认 `protocol 空时 "tcp+udp"`、`area_code:""`、`dst_type:""`。

- [ ] **Step 1: 写分片索引解析测试（兼容标识是存量用户清理正确性的关键）**

```go
// internal/ikuai/crud_test.go
func TestParseChunkIndexLegacyMarkers(t *testing.T) {
    cases := []struct{ in string; want int64; ok bool }{
        {"IkuaiBypass", 1, true}, {"IkuaiBypass-3", 3, true},
        {"joyanhui/ikuai-bypass-2", 2, true}, {"IKUAI_BYPASS_4", 4, true},
        {"别家的备注", 0, false},
    }
    // 逐一断言 ParseCustomIspChunkIndexFromComment
}
```

- [ ] **Step 2: 失败 → 实现 custom_isp.go（含解析函数）→ 通过 → Commit `feat(go): 自定义运营商 CRUD 与分片解析`**

- [ ] **Step 3: 用同一个 httptest 假服务模板为 ip_group/ipv6_group 写 `TestRouteObjectCrud`（断言 show 的 FILTER 参数 type=0/1、add/edit body 结构、GetMap 的索引解析、ResolveRuleReference 的精确名优先）**

- [ ] **Step 4: 实现 ip_group.go + ipv6_group.go → 通过 → Commit `feat(go): IP/IPv6 分组 CRUD`**

- [ ] **Step 5: 同法完成 stream_domain.go（含 resolve_src_addrs 的 custom/object 分离与 `IPGP<id>` 对象结构）与 stream_ipport.go → 测试 `TestStreamDomainSpec`/`TestStreamIpPortSpec`（默认 protocol tcp+udp）→ Commit `feat(go): 域名与端口分流 CRUD`**

- [ ] **Step 6: 实现 clean.go（IsManaged/MatchCleanTag，用 types.rs L6-17 + clean.rs 的用例反向测试：`joyanhui/ikuai-bypass-2` 必须命中受管）→ Commit `feat(go): 受管规则识别`**

---

### Task 4: 代理规划层 netx

**Files:**
- Create: `internal/netx/plan.go`
- Test: `internal/netx/plan_test.go`
- 规格：`crates/core/src/net.rs`（175 行）——把行为矩阵搬过来即可，无外部依赖

**Interfaces:**
- Produces:

```go
type ProxyChoice int // Direct | System | Custom
type HttpPlan struct {
    URL            string
    Proxy          ProxyChoice
    UsedGithubProxy bool
}
type NetConfig struct {
    Mode ProxyMode; ProxyURL, ProxyUser, ProxyPass, GithubProxy string
}
func NetConfigFromConfig(c *config.Config) NetConfig
func NetConfigFromParts(p *config.ProxyConfig, githubProxy string) NetConfig
func IsGithubURLForGhproxy(url string) bool   // raw.githubusercontent.com / github.com 前缀
func NormalizeURLPrefix(in string) string     // 无协议补 https://
func PlanRuleFetch(net NetConfig, originalURL string) HttpPlan   // Smart+ghproxy→改写 URL+Direct；Smart 且无改写→System；Custom→Custom
func PlanGithubAPI(net NetConfig, url string) HttpPlan           // 不改写 URL，其余同上
func ApplyProxyChoice(hc *http.Client, net NetConfig, choice ProxyChoice) // Direct/Custom 设 no_proxy；Custom 默认 7890 + basic auth；System 用环境
```

注意：`http.Client` 的代理通过替换 `Transport` 实现；Custom 模式 `http.ProxyURL(u)` + `u.User` 携带 basic auth。

- [ ] **Step 1: 写决策矩阵测试**

```go
// internal/netx/plan_test.go —— 每行一个矩阵项，期望值从 net.rs 行为抄
func TestPlanRuleFetchMatrix(t *testing.T) {
    cases := []struct{ name, mode, ghproxy, url string; wantURL string; wantProxy ProxyChoice; wantGh bool }{
        {"smart+ghproxy+github源", "smart", "https://gh.x/", "https://raw.githubusercontent.com/a/b", "https://gh.x/https://raw.githubusercontent.com/a/b", ProxyDirect, true},
        {"smart+ghproxy+非github源", "smart", "https://gh.x/", "https://example.com/l.txt", "https://example.com/l.txt", ProxySystem, false},
        {"smart无ghproxy", "smart", "", "https://example.com/l.txt", "https://example.com/l.txt", ProxySystem, false},
        {"custom", "custom", "", "https://example.com/l.txt", "https://example.com/l.txt", ProxyCustom, false},
    } // …逐一断言
}
func TestPlanGithubAPINeverRewrites(t *testing.T) // 即使 smart+ghproxy，api.github.com URL 也不被改写
```

- [ ] **Step 2: 失败 → 实现 → 通过 → Commit `feat(go): 代理规划 netx`**

---

### Task 5: 更新主流程（Safe-Before / Edit 优先 / 分片清理 / 严格顺序）★ 全计划最关键任务

**Files:**
- Create: `internal/update/update.go`、`modules.go`、`session.go`、`router.go`
- Test: `internal/update/update_test.go`
- 规格：`crates/core/src/update.rs`（1045 行）+ `session.rs` + `router.rs` + `runner.rs`

**Interfaces:**
- Consumes: Task 1/2/3/4 的全部接口
- Produces:

```go
type UpdateOptions struct{ ExportPath string; IpGroupNameAddRandomSuffix bool }
type UpdateError struct{ Kind string; Msg string } // login_params | ikuai | download | invalid_module

type LogSink func(rec LogRecord) // LogRecord 来自 internal/logger
func RunUpdateByModule(cfg *config.Config, cliLogin, module string, opts *UpdateOptions, sink LogSink) error
func ExportStreamDomainToTxt(cfg *config.Config, exportPath string, sink LogSink) error
func Group[T any](items []T, n int) [][]T // MaxNumberOfOneRecords 分片
```

行为规格（必须逐条对齐 update.rs）：
1. **严格顺序**：先 Login，再按 module 串行执行（Go 里就是普通 for 循环，天然顺序）：`ispdomain` / `ipgroup` / `ipv6group` / `ii`(ipgroup→ispdomain) / `ip`(ipgroup→ipv6group) / `iip`(ipgroup→ispdomain→ipv6group)；未知模块返回 invalid_module。**禁止 goroutine 并发**。
2. **Safe-Before**：四个 update 函数第一步 `httpGet(cfg, sink, url)`，失败直接 return；httpGet：connect 10s / 总 120s，经 netx.PlanRuleFetch 规划，非 2xx 或网络错误 => UpdateError{download}。
3. **Edit 优先**：`Get*Map(tag)` 取现有 chunk→id；分片循环 `i+1` 为 index：map 命中 => Edit（传既有 id；ip/ipv6 group 传既有 existing_name 保持名称）；未命中 => Add。单片失败：记中文日志 + `sleep(AddErrRetryWait)` 继续；成功后 `sleep(AddWait)` 节流。
4. **冗余分片清理**：循环后 map 剩余 id 合并 CSV 一次 Del（日志 "CLEAN:冗余删除"）。
5. 行清洗：去 `#` 注释、去空行、含 `:` 分 v4/v6、域名行含 `_` 过滤。
6. stream_ipport：每 tag 单条，map 取第一条命中 Edit 否则 Add；ip-group 引用先 Resolve 展开，空则跳过不报错。
7. session.go：`ParseLoginParams(cliLogin, cfg) (baseURL, user, pass)`——CLI `url,user,pass` > 配置 > router.go 网关猜测（Linux 读 `/proc/net/route` 默认网关）。

- [ ] **Step 1: 搭建内存版假爱快（httptest），支持 login + 五个 func_name 的 show/add/edit/del 且记录调用序列**

```go
// internal/update/update_test.go 内嵌 fake
type fakeIkuai struct {
    mu sync.Mutex
    calls []string          // 形如 "custom_isp.show" / "custom_isp.add(name=IKBxx1)"
    store map[string][]map[string]any // func_name -> rows
}
func (f *fakeIkuai) ServeHTTP(w http.ResponseWriter, r *http.Request) { /* /Action/login 与 /Action/call 分发 */ }
```

- [ ] **Step 2: 写 Safe-Before 测试（数据安全边界，最高优先级）**

```go
func TestSafeBefore_DownloadFailNeverMutates(t *testing.T) {
    // 列表源返回 502；假爱快 store 预置了该 tag 的 3 条旧规则
    // RunUpdateByModule 后断言：fake.calls 中不存在任何 .add/.edit/.del 调用
    // 且日志中出现中文错误（下载失败）
}
func TestSafeBefore_PerItemContinue(t *testing.T) {
    // 两个 tag，第一个源 502、第二个正常 => 第二个 tag 正常完成增改，进程不中断
}
```

- [ ] **Step 3: 写 Edit 优先与冗余清理测试**

```go
func TestEditFirst_KeepsIdAndName(t *testing.T) {
    // 预置 IKBdemo1(id=7,name="IKBdemo1")；新数据仍是 1 片
    // 断言：发生 edit(id=7) 且名称不变、无 add、无 del
}
func TestShrinkDeletesRedundantChunks(t *testing.T) {
    // 预置 3 片；新数据只够 1 片 => 片1 edit，片2/3 一次批量 del（单次调用，id CSV "8,9"）
}
func TestGrowAddsNewChunks(t *testing.T)          // 预置 1 片，新数据 3 片 => edit + 2×add
func TestStreamIpPortSingleRule(t *testing.T)     // 命中 edit 否则 add；引用展开为空跳过
```

- [ ] **Step 4: 跑测试失败 → 实现 update.go（updateCustomIsp/UpdateStreamDomain/UpdateIpGroup/UpdateIpv6Group/UpdateStreamIpPort + httpGet + 行清洗 + Group）→ 全部通过 → Commit `feat(go): 更新主流程（Safe-Before/Edit优先/分片清理）`**

- [ ] **Step 5: 实现 modules.go 的模块分发矩阵 + `TestModuleOrder`（用 fake.calls 断言 `ii` 模块先 ipgroup 后 ispdomain 的调用顺序）+ `TestInvalidModule` → Commit `feat(go): 模块编排`**

- [ ] **Step 6: 实现 session.go/router.go + `TestSessionPriority`（CLI login 参数优先于配置）→ Commit `feat(go): 登录参数解析`**

---

### Task 6: 日志与运行时（cron / run-once / 日志代理）

**Files:**
- Create: `internal/logger/logger.go`、`internal/runtime/runtime.go`、`cron_norm.go`、`broker.go`
- Test: `internal/runtime/runtime_test.go`、`cron_norm_test.go`、`internal/logger/logger_test.go`
- 规格：`crates/core/src/logger.rs`、`runtime.rs`（450 行）

**Interfaces:**
- Produces:

```go
// logger
type LogLevel string // "Info"|"Success"|"Warn"|"Error"（JSON 序列化值必须与 Rust 一致，前端 level 判断依赖）
type LogRecord struct{ Ts, Module, Tag string; Level LogLevel; Detail string }
type Logger struct{ /* module string; sink func(LogRecord) */ }
func NewLogger(module string, sink func(LogRecord)) *Logger
func (l *Logger) Info/Success/Warn/Error(tag, detail string)

// runtime —— 对应 RuntimeService（WebUI/CLI 共用）
type RuntimeStatus struct {          // JSON 字段名与 /api/runtime/status 完全一致
    Running bool `json:"running"`; CronRunning bool `json:"cron_running"`
    CronExpr string `json:"cron_expr"`; Module string `json:"module"`
    LastRunAt string `json:"last_run_at"`; NextRunAt string `json:"next_run_at"`
}
type RuntimeService struct{ /* cfg atomic.Pointer[config.Config]; running atomic.Bool; … */ }
func NewRuntimeService(cfg *config.Config, cliLogin, defaultCron, defaultModule string, opts *update.UpdateOptions) *RuntimeService
func (s *RuntimeService) SetDefaults(module, cronExpr string)
func (s *RuntimeService) TailLogs(n int) []LogRecord
func (s *RuntimeService) SubscribeLogs() (ch <-chan LogRecord, cancel func())
func (s *RuntimeService) Status() RuntimeStatus
func (s *RuntimeService) StartRunOnce(module string) (bool, error) // atomic.CompareAndSwap 防重入，false=已在跑
func (s *RuntimeService) StartCron(expr, module string) error
func (s *RuntimeService) StopCron() error
func (s *RuntimeService) StopAll() error

// broker.go：环形缓冲 5000 条 + 订阅广播（channel，容量 512，满则丢弃不阻塞）
// cron_norm.go
func NormalizeCronExpr(expr string) (string, error) // 5 段→前缀 "0 "；6 段→原样；7 段→去年份段
```

cron 循环：用 `robfig/cron/v3` + `cron.NewParser(cron.Second|cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow|cron.Descriptor)`（6 段含秒）；每秒轮询下次触发点写 `next_run_at`（RFC3339 本地时区）。run-once 结束写 `last_run_at`。

- [ ] **Step 1: 写 cron 归一化测试**

```go
// internal/runtime/cron_norm_test.go
func TestNormalizeCron(t *testing.T) {
    cases := []struct{ in, want string }{
        {"5 * * * *", "0 5 * * * *"},          // 5 段 → 补秒
        {"0 5 * * * *", "0 5 * * * *"},         // 6 段原样
        {"0 5 * * * * *", "0 5 * * * *"},       // 7 段去年份
        {"* * * *", ""},
    }
    // 最后一条期望 err != nil
}
```

- [ ] **Step 2: 失败 → 实现 → 通过 → Commit `feat(go): cron 表达式归一化`**

- [ ] **Step 3: 写 runtime 测试**

```go
// internal/runtime/runtime_test.go
func TestRunOnceReentrancy(t *testing.T)  // 第一次 true，未完成时第二次 false
func TestStatusFields(t *testing.T)       // JSON 序列化字段名逐一断言（running/cron_running/cron_expr/module/last_run_at/next_run_at）
func TestBrokerRing(t *testing.T)         // 写入 6000 条，TailLogs(10) 取到最新 10 条；缓冲总量封顶 5000
func TestSubscribeBroadcast(t *testing.T) // 订阅后写入，channel 收到；cancel 后不再收
```

- [ ] **Step 4: 实现 runtime.go/broker.go（goroutine + context 取消实现 stop）→ 通过 → Commit `feat(go): RuntimeService 与日志代理`**

- [ ] **Step 5: 实现 logger.go（Renderer ANSI 彩色 + 正则高亮，行为对齐 logger.rs；`TestLogLevelJSON` 断言 JSON 值首字母大写）→ Commit `feat(go): 日志记录器`**

---

### Task 7: app 服务层（github / diagnostics / clean / config_meta / fetch / url）

**Files:**
- Create: `internal/app/github.go`、`diagnostics.go`、`clean.go`、`config_meta.go`、`fetch.go`、`url.go`
- Test: `internal/app/app_test.go`
- 规格：`crates/core/src/app/` 各文件（签名清单见探索报告 §7）

**Interfaces:**
- Consumes: 前六个任务的全部接口
- Produces:

```go
type GithubRelease struct {
    TagName string `json:"tag_name"`; Name string `json:"name"`
    Prerelease bool `json:"prerelease"`; Draft bool `json:"draft"`; HTMLURL string `json:"html_url"`
    PublishedAt string `json:"published_at"`; CreatedAt string `json:"created_at"`
}
func FetchGithubReleases(proxy *config.ProxyConfig) ([]GithubRelease, error)
// GET https://api.github.com/repos/FelixJI/iKuai-Toolbox/releases?per_page=30，UA "ikb-core"，8s/15s 超时

type TestResult struct{ OK bool `json:"ok"`; Message string `json:"message"` }
type TestIkuaiLoginRequest struct{ BaseURL, Username, Password string } // JSON 同时接受 baseUrl 与 base_url（自定义 UnmarshalJSON）
type TestGithubProxyRequest struct{ GithubProxy string }                 // githubProxy / github_proxy
func TestIkuaiLogin(req TestIkuaiLoginRequest) TestResult
func TestGithubProxy(req TestGithubProxyRequest) TestResult // 直连探测 raw.githubusercontent.com/FelixJI/iKuai-Toolbox/main/.gitignore，检测 HTML 误回
func BuildDiagnosticsReport(cfg *config.Config, configPath string, rt *runtime.RuntimeStatus, cliLogin string) DiagnosticsReport // 密码脱敏 (set,len=N)；cron 归一化+下次触发；规则 URL Range bytes=0-2047 抽样探测
func RunClean(cfg *config.Config, cliLogin, cleanTag string) error // 顺序 custom_isp→stream_domain→ip_group→ipv6_group→stream_ipport；空 tag => "missing tag" 错误
func BuildConfigMeta(cfg *config.Config, configPath string) (ConfigMeta, error) // {config 展开为 JSON + conf_path + raw_yaml}
func FetchRemoteConfig(url string, proxy *config.ProxyConfig, githubProxy string) (string, error)
func NormalizeBaseURL(in string) string // 补 http://
```

- [ ] **Step 1: 写别名解码测试 `TestRequestAliases`（`{"baseUrl":...}` 与 `{"base_url":...}` 都解到 BaseURL；`githubProxy`/`github_proxy` 同理——前端 Tauri 走 camelCase、HTTP 走 snake_case）**

- [ ] **Step 2: 实现 github.go/fetch.go/url.go（走 netx 规划）→ 通过 → Commit `feat(go): 更新检查与远程拉取`**

- [ ] **Step 3: 写 clean 顺序测试 `TestCleanOrder`（fake 断言五模块删除顺序）+ `TestCleanRequiresTag` → 实现 clean.go → Commit `feat(go): 清理流程`**

- [ ] **Step 4: 实现 config_meta.go + diagnostics.go（脱敏逻辑测试：`TestPasswordMasked`——报告文本中出现 `(set,len=` 而非明文）→ Commit `feat(go): 配置元信息与诊断报告`**

---

### Task 8: Web 服务（15 端点 + BasicAuth + SSE + 静态嵌入）

**Files:**
- Create: `internal/webserver/server.go`、`routes.go`、`auth.go`、`sse.go`、`static.go`
- Test: `internal/webserver/routes_test.go`、`auth_test.go`
- 规格：`apps/cli/src/web.rs` + `embedded.rs`（路由表已由探索报告 §2 完整给出）

**Interfaces:**
- Consumes: `internal/app`、`internal/runtime`、`internal/config`
- Produces:

```go
type Server struct{ /* cfgHolder atomic.Pointer[config.Config]; rt *runtime.RuntimeService; … */ }
func NewServer(rt *runtime.RuntimeService, cfg *config.Config, cfgPath string) *Server
func (s *Server) Handler() http.Handler       // 挂全部路由 + BasicAuth 中间件 + 静态 fallback
func StartWebServer(s *Server, port string) error // 绑 0.0.0.0:{port}，打印 http://127.0.0.1:{port}
```

路由契约表（实现与测试的唯一依据）：

| 方法 | 路径 | 请求 | 成功响应 | 错误响应 |
|---|---|---|---|---|
| GET | /api/config | — | 200 JSON `{exe_path, <config 展开>, conf_path, raw_yaml}` | 500 text |
| GET | /api/config/default | — | 200 text/plain 默认 YAML | — |
| GET | /api/diagnostics/report | — | 200 JSON `{generated_at, text}` | — |
| POST | /api/save-raw | `{yaml_text}` | 200 JSON `{"status":"success","message":"Raw YAML saved successfully"}` | 400 text |
| POST | /api/remote/fetch | `{url, proxy, githubProxy}` | 200 text | 502 text |
| POST | /api/test/ikuai-login | `{baseUrl, username, password}` | 200 JSON TestResult | — |
| POST | /api/test/github-proxy | `{githubProxy}` | 200 JSON TestResult | — |
| GET/POST | /api/github/releases | POST 带 `{proxy}` | 200 JSON GithubRelease[] | 502 text |
| GET | /api/runtime/status | — | 200 JSON RuntimeStatus | — |
| POST | /api/runtime/run-once | `{module}` | 200 `{"started": bool}` | 500 text |
| POST | /api/runtime/cron/start | `{expr, module}` | 200 `{"status":"success"}` | 400 text |
| POST | /api/runtime/cron/stop | — | 200 `{"status":"success"}` | 500 text |
| POST | /api/runtime/stop | — | 200 `{"status":"success"}` | 500 text |
| POST | /api/runtime/clean | `{clean_tag}` | 200 `{"status":"success"}` | 400 text |
| GET | /api/runtime/logs?tail=N | 默认 200 | 200 JSON LogRecord[] | — |
| GET | /api/runtime/logs/stream | — | SSE `text/event-stream`，每条 `data:<LogRecord JSON>` | — |

关键行为：
1. **BasicAuth 动态读配置**：每个请求从 `cfgHolder` 取当前 `webui.user`；user 空=>放行；否则常数时间比较（`crypto/subtle.ConstantTimeCompare`），失败 401 + `WWW-Authenticate: Basic realm="Restricted"` + body `Unauthorized`。
2. `/api/save-raw` 语义：`ValidateAndSaveRawYAML` 落盘；仅当 `YAMLHasExplicitMode(raw)` 为真才用该 mode 调 `rt.SetDefaults`，否则传空保持现状；随后同步 cron 默认值。
3. 静态资源：`//go:embed all:frontends/app/dist`（构建脚本先 `bun run build`）；MIME 按扩展名（html/js/css/png/svg/ico/webp/json，其余 octet-stream）；未命中回退 index.html（SPA）。
4. `/api/config` 与 SSE 响应带 `Cache-Control: no-store`。
5. SSE：`SubscribeLogs` 的 channel 转 `data:` 行 + `http.Flusher`；客户端断开 cancel 订阅。

- [ ] **Step 1: 先构建前端产物**

```bash
cd frontends/app && bun install && bun run build   # 产出 dist/
```

- [ ] **Step 2: 写契约测试（每个端点至少一条：成功 + 一条错误路径）**

```go
// internal/webserver/routes_test.go 示例（其余端点同模板）
func TestSaveRawContract(t *testing.T) {
    s := newTestServer(t) // 注入临时配置路径与最小 RuntimeService
    req := httptest.NewRequest("POST", "/api/save-raw",
        strings.NewReader(`{"yaml_text":"ikuai-url: http://1.2.3.4\n"}`))
    rec := httptest.NewRecorder()
    s.Handler().ServeHTTP(rec, req)
    if rec.Code != 200 { t.Fatalf("code=%d body=%s", rec.Code, rec.Body) }
    if got := rec.Body.String(); !strings.Contains(got, `"status":"success"`) { t.Fatalf(got) }
}
func TestSaveRawRejectsBadYAML(t *testing.T)      // 400 + "Failed to save config:"
func TestBasicAuth(t *testing.T)                  // user 设置后无凭据 401；配置更新为空 user 后放行（动态读取）
func TestSSEStream(t *testing.T)                  // 注入日志后 body 出现 "data:" 且含 LogRecord JSON
func TestSpaFallback(t *testing.T)                // GET /unknown/path => 200 index.html
func TestConfigShape(t *testing.T)                // /api/config 响应含 exe_path/conf_path/raw_yaml 与 ikuai-url 键
```

- [ ] **Step 3: 失败 → 实现 server.go/routes.go（`http.ServeMux` Go 1.22 方法路由：`mux.HandleFunc("POST /api/save-raw", …)`）→ 通过 → Commit `feat(go): Web 服务 15 端点契约`**

- [ ] **Step 4: 实现 auth.go + sse.go + static.go → 全部测试通过 → Commit `feat(go): BasicAuth/SSE/静态嵌入`**

- [ ] **Step 5: 前端真实联调**

```bash
go run ./cmd/ikuai-bypass -c test-config.yml   # test-config.yml 设 webui.enable=true, port=19001
# 浏览器打开 http://127.0.0.1:19001 ：配置页加载、可视化编辑保存、运行一次、日志流全部可用
```

Expected: 现有 Astro 前端在 Go 后端上全功能可用（这一步是"GUI 不变"的验收点）

---

### Task 9: CLI 入口（flags / 运行模式 / 信号 / 退出码）

**Files:**
- Modify: `cmd/ikuai-bypass/main.go`（补全为完整入口）
- Test: `cmd/ikuai-bypass/main_test.go`（模式分发抽成可测函数）
- 规格：`apps/cli/src/main.rs` + `lib.rs`（参数表与退出码见探索报告 §1）

**Interfaces:**
- Consumes: Task 5/6/7/8
- Produces: 可执行 `ikuai-bypass`

参数表（长参单横线兼容必须实现——LuCI/libexec 用 Go 风格调用）：`-c/--c` 配置路径、`-r/--r` 运行模式、`-m/--m` 模块、`-tag`、`-exportPath`（默认 /tmp）、`-login`、`-isIpGroupNameAddRandomSuff`（默认 "1"，非 `0/false/off/no/空` 为 true）。统一把 `-xxx` 重写为 `--xxx`（含 `=` 形式）再解析。

运行模式：`exportDomainStreamToTxt` 导出（exit 2/1/0）；`cron` 先跑一次再定时；`cronAft` 仅定时；`nocron/once/1` 单次；`clean` 要求 `-tag` 非空否则 exit 2；`web` 报错提示改用 webui.enable（exit 2）；未知 exit 2。配置文件不存在且 TTY => 交互询问 y 写默认；非 TTY exit 1。cron/cronAft 读 `webui.enable/port`，启用则 StartWebServer；cron 空且无 WebUI => exit 0；否则信号阻塞（SIGINT/SIGTERM => rt.StopAll() 后 exit 0）。

- [ ] **Step 1: 写模式分发测试 `TestRunModeDispatch`（表驱动：输入模式=>期望调用的服务方法与退出码；web/未知/空 tag clean => 2）**

- [ ] **Step 2: 实现 main.go（flag 重写 + 分发 + signal.NotifyContext）→ 通过 → Commit `feat(go): CLI 入口与运行模式`**

- [ ] **Step 3: 手工冒烟：`go run ./cmd/ikuai-bypass -m ispdomain -r once -c test-config.yml` 对假爱快/模拟器全流程跑通 → Commit（如有修正）**

---

### Task 10: 集成测试移植（模拟器 + smoke 套件）

**Files:**
- Create: `apps/integration-tests-go/` 目录：`simulator/simulator.go`（Go 版爱快模拟器）、`smoke/safe_before_test.go`、`smoke/sync_update_test.go`、`smoke/clean_test.go`、`smoke/cli_modes_test.go`、`smoke/export_test.go`
- 规格：`apps/integration-tests/`（Rust 版 16 个 smoke 的行为清单见 `golang_archive` 探索缺口项 5）

**Interfaces:**
- Consumes: 编译出的 `ikuai-bypass` 二进制（`os/exec` 子进程跑真实 CLI）
- Produces: `go test ./apps/integration-tests-go/...`（CI 的 Go 版验收门）

模拟器行为契约（用 Go `httptest` 实现独立进程）：
- `POST /Action/login`：校验 md5/base64 双字段，成功发 cookie，失败 code 1
- `POST /Action/call`：按 `func_name`+`action` 分发内存存储（custom_isp/route_object(type 0/1)/stream_domain/stream_ipport），支持 show 的 FILTER/tag 过滤、add/edit/del；每次调用写 JSONL 审计日志供断言

Smoke 清单（每个都是端到端：起模拟器 → 起二进制 → 断言模拟器状态）：
1. `safe_before`：源 502 => 规则零变动；2. `safe_before_chunk_shrink`：源内容缩片 => 只删冗余片；3. `rule_sync_update_in_place`：同规模内容变化 => edit 保 id 保名；4. `clean_all`（cleanAll 删光全部受管）与 `clean_mode`（按 tag）；5. `cli_modes`：once/cronAft 参数路径；6. `export_stream_domain`：导出 TXT 内容正确。

- [ ] **Step 1: 实现模拟器 + `TestSimulatorLoginAndCall`（自测）→ Commit `test(go): 爱快模拟器`**

- [ ] **Step 2: 逐个移植 smoke（每个 smoke 一个提交）：safe_before → chunk_shrink → in_place → clean → cli_modes → export → Commit 每步 `test(go): 移植 xxx smoke`**

- [ ] **Step 3: 全量跑通：`go test ./...` 绿 → Commit `test(go): 集成测试全量通过`**

---

### Task 11: Tauri 桌面壳改造（GUI 不变的实现方式）

**Files:**
- Modify: `apps/gui/src/lib.rs`（重写，预计 ~200 行）、`apps/gui/tauri.conf.json`
- Modify: `apps/gui/Cargo.toml`（删 `ikb-core` 依赖）
- Create: `apps/gui/sidecar/README.md`（说明 sidecar 打包方式）
- 不动: `frontends/app/` 全部文件

**Interfaces:**
- Consumes: Task 8 的 Web 服务（Go 二进制）
- Produces: Tauri 应用 = "启动本机 Go 服务 + 打开其页面" 的薄壳；**前端 bridge.ts 无需感知**（窗口加载 Go 服务 URL 后无 `__TAURI__` 注入，bridge 自动走 HTTP 模式，与浏览器行为一致）

实现规格：
1. `tauri.conf.json`：`app.windows[0].url` 改为启动时动态设置（Rust 侧 `window.eval` 或 WebviewWindowBuilder URL 指向 `http://127.0.0.1:{port}`）；`devUrl` 保留 `http://localhost:4321`（前端开发不受影响）。
2. `lib.rs`：setup 阶段 (a) 解析配置路径（沿用现有 resolve_gui_config_path 逻辑，移动端目录约定保留）；(b) 选空闲端口写入临时配置或以 `-c` 传参；(c) 用 `tauri_plugin_shell` sidecar 方式启动 `ikuai-bypass -c <path> -r cronAft`（externalBin 配置，Windows `-x64.exe`）；(d) 轮询 `GET /api/runtime/status` 就绪后建窗口；(e) 退出事件杀子进程。
3. 移动端（Android/iOS 无法起子进程）：本任务只做**桌面**；移动端策略另立后续任务（远程连接模式），不在本计划范围。
4. 日志：壳层只做 stdout 转发到 `tauri_plugin_log`，`ikb://log` 事件不再需要（SSE 已覆盖）——保留 `withGlobalTauri: true` 无害。

- [ ] **Step 1: 改 tauri.conf.json（externalBin + 动态 URL）与 Cargo.toml（去 ikb-core，加 tauri-plugin-shell）→ `cargo check` 过 → Commit `feat(gui): Tauri 壳改 sidecar 配置`**

- [ ] **Step 2: 重写 lib.rs（配置解析/端口选择/sidecar 启动/就绪探测/窗口创建/退出清理）→ 桌面手工验收：配置页加载、保存、运行一次、日志 SSE 可用 → Commit `feat(gui): Go sidecar 启动器`**

- [ ] **Step 3: 验收 GUI 不变清单：前端构建产物 diff 为空（`cd frontends/app && git status` 干净）→ Commit（如有杂项）**

---

### Task 12: 打包与 CI 切换

**Files:**
- Modify: `Dockerfile`（构建阶段改 golang:1.25-alpine，`CGO_ENABLED=0 go build`，运行阶段 scratch/alpine 保留 TZ 逻辑）
- Modify: `.github/workflows/release.yml`（Rust matrix 换 Go 交叉编译；**保持触发约束不变**：仅 tag push + workflow_dispatch、prerelease 判定规则、manual-release 自动命名、nightly MIPS 等约束逐条保留——见 AGENTS.md CI 约束节）
- Modify: `.github/workflows/integration.yml`（cargo test 换 `go test ./...`，模拟器路径指向 integration-tests-go）
- Modify: `packaging/ikuai-ipkg/`、`packaging/openwrt-luci/build-openwrt-luci-package.sh`（资产名不变，来源二进制名不变——通常零改动，核对即可）

**Interfaces:**
- Consumes: Task 9 的构建命令 `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -ldflags "-s -w" -o ikuai-bypass<ext> ./cmd/ikuai-bypass`

- [ ] **Step 1: Dockerfile 改造 → 本地 `docker build -t ikb-go . && docker run --rm ikb-go --help` 验证 → Commit `build(go): Dockerfile`**

- [ ] **Step 2: release.yml 重写构建 matrix（linux/amd64,arm64,mips,mipsle,mips64,386 + darwin + windows），前端 dist 构建步骤前置（bun install/build），zip/tar 命名与现资产完全一致 → YAML lint（`yamllint` 或 actionlint）过 → Commit `ci(go): release 构建矩阵`**

- [ ] **Step 3: integration.yml 换 go test → Commit `ci(go): 集成测试工作流`**

---

### Task 13: 停机切换（归档 Rust，Go 上位）

**Files:**
- Create: `rust_archive/`（移入 `crates/`、`apps/cli/`、`apps/gui/src` 旧实现、根 `Cargo.toml`/`Cargo.lock`/rust-toolchain 等全部 Rust 工程文件）、`rust_archive/README.md`
- Modify: `AGENTS_This.md`（技术栈描述、测试命令、目录结构）、`README.md`（技术栈徽章 Rust→Go）、根目录清理（`.cargo/`、`flake.nix` 的 Rust 部分）
- Delete: 原 Rust 目录（移动后）

**Interfaces:**
- Consumes: Task 10 全绿 + Task 11/12 验收通过

- [ ] **Step 1: 打迁移完成 tag**

```bash
git tag rust-final  # 冻结点标记，方便回溯
```

- [ ] **Step 2: `git mv crates rust_archive/crates && git mv apps/cli rust_archive/apps-cli && …`（gui 的 tauri.conf/Cargo 保留在新结构，仅 src/lib.rs 旧核心逻辑已由 Task 11 替换）→ 写 rust_archive/README.md（同 golang_archive 风格：不再维护、目录结构差异说明）→ Commit `chore: 归档 Rust 实现`**

- [ ] **Step 3: 更新 AGENTS_This.md（目录结构节、集成测试路径、命令、技术栈；删除零 Clone 等 Rust 专属约束，写入 Go 约束：错误用 fmt.Errorf %w、禁止 panic、并发仅限 update 之外）与 README.md 徽章 → Commit `docs: 切换主线为 Go`**

- [ ] **Step 4: 合并回 main 并发版**

```bash
git checkout main && git merge --no-ff go-migration
# 打 tag（按 release.yml 约定：含 "rc" 等 prerelease 标记）
git tag v5.0.0-rc.1 && git push origin main --tags
# CI 自动构建发布，核对 release 资产齐全（各架构二进制 + ipk + docker）
```

- [ ] **Step 5: 上线验证清单：Docker 镜像拉取运行、LuCI ipk 安装、install.sh 一键安装、桌面 GUI 下载安装——四渠道各过一遍 → 在 docs/go-migration-notes.md 记录收尾结论 → Commit `chore: v5.0.0-rc.1 发布完成`**

---

## 风险与回滚

- **行为漂移风险集中地**：tag_name 截断/IP 分组 hash 后缀（Task 2）、chunk 索引解析（Task 3）、Safe-Before（Task 5）——这三处的测试必须先于实现存在。
- **回滚**：切换前 `rust-final` tag + 分支结构保证任何时刻可回到 Rust 主线；release 渠道在 v5.0.0 正式版前都保留 Rust v4.4.109 资产。
- **移动端**：本计划明确不覆盖（Task 11 说明）；如需，后续立独立计划（远程连接模式）。

## 自审记录（Self-Review）

- 规格覆盖：配置（T1）、客户端/标签（T2）、五模块 CRUD（T3）、代理（T4）、更新/安全（T5）、运行时（T6）、app 服务（T7）、Web 契约（T8）、CLI（T9）、集成测试（T10）、GUI 不变（T11）、打包 CI（T12）、切换（T13）——探索报告各节均有对应任务；`paths.rs/router.rs/session.rs` 并入 T1/T5，`logger.rs` 在 T6，`app/url.rs` 在 T7。
- 占位符扫描：无 TBD/TODO；每个任务给出文件、签名、测试代码与验收命令。
- 命名一致性：`Duration/ProxyMode/LogRecord/RuntimeStatus/HttpPlan` 等跨任务引用名已核对一致；HTTP 端点表与 bridge.ts 探索报告逐条对齐。
