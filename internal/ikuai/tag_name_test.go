// tag_name_test.go IKB 标签构建与匹配测试，行为字节级对齐 crates/core/src/ikuai/tag_name.rs。
// 所有期望值的推导依据均以 tag_name.rs 行号标注；测试先于实现编写（TDD）。
// Behavioral tests byte-aligned with crates/core/src/ikuai/tag_name.rs.
// Every expected value cites the tag_name.rs lines it derives from; tests were written before the implementation (TDD).
package ikuai

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSanitizeTagName 对齐 tag_name.rs L14-32：先剥已知前缀再做 [^\p{Han}A-Za-z0-9]+ 清洗。
// Rust trim_start_matches 会剥掉全部重复前缀，Go 侧必须循环剥前缀而不是单次 TrimPrefix。
func TestSanitizeTagName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "abcd", "abcd"},
		{"strip legacy prefix", "IKUAI_BYPASS_foo", "foo"},
		{"strip ikb prefix", "IKBabc", "abc"},
		{"strip repeated ikb", "IKBIKBx", "x"},
		{"strip repeated legacy", "IKUAI_BYPASS_IKUAI_BYPASS_y", "y"},
		{"legacy then ikb", "IKUAI_BYPASS_IKBz", "z"},
		{"symbols removed", "a-b_c d!e", "abcde"},
		{"han preserved", "运营商4G", "运营商4G"},
		{"outer spaces trimmed", "  abcd  ", "abcd"},
		{"all stripped", "###", ""},
	}
	for _, tc := range cases {
		if got := SanitizeTagName(tc.in); got != tc.want {
			t.Errorf("%s: SanitizeTagName(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestBuildTagName 对齐 tag_name.rs L34-43 + L84-97：
// "IKB"+token 后按字节安全截断到 15 字节（不切多字节字符）；空 token 直接返回 "IKB"。
func TestBuildTagName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"short", "abcd", "IKBabcd"},
		{"empty raw keeps prefix", "", "IKB"},
		{"sanitize-only raw keeps prefix", "###", "IKB"},
		// Rust 自带测试（tag_name.rs L166-170）：IKBSafeChunkRoute 17 字节截到 15。
		{"ascii truncate", "SafeChunkRoute", "IKBSafeChunkRou"},
		// 20 个 x：IKB+20x=23 字节 → 截 15 字节 = IKB+12x。
		{"twenty ascii", strings.Repeat("x", 20), "IKB" + strings.Repeat("x", 12)},
		// 10 个汉字 30 字节：IKB(3)+每字 3 字节，第 5 字结束于 18>15，故只保留 4 字（L84-97 逐字符推进）。
		{"han truncate", "运营商自定义超长名称", "IKB运营商自"},
	}
	for _, tc := range cases {
		if got := BuildTagName(tc.in); got != tc.want {
			t.Errorf("%s: BuildTagName(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
	if got := BuildTagName(strings.Repeat("x", 20)); len(got) != 15 {
		t.Errorf("BuildTagName(20x) len = %d, want 15", len(got))
	}
	if !utf8.ValidString(BuildTagName("运营商自定义超长名称")) {
		t.Error("truncated tag name must stay valid UTF-8")
	}
}

// TestBuildTagNameTruncation 任务书要求的精确断言集合（tag_name.rs L34-43/L84-97）。
func TestBuildTagNameTruncation(t *testing.T) {
	if BuildTagName("abcd") != "IKBabcd" {
		t.Fatalf("got %q", BuildTagName("abcd"))
	}
	if got := BuildTagName(strings.Repeat("x", 20)); len(got) != 15 {
		t.Fatalf("len=%d", len(got))
	}
	han := BuildTagName("运营商自定义超长名称")
	if !strings.HasPrefix(han, "IKB") || len(han) > 16 {
		t.Fatalf("got %q", han)
	}
}

// TestBuildIndexedTagName 对齐 tag_name.rs L45-51：尾缀 = index+1 的十进制串，
// base 截到 15-len(suffix) 字节后拼接。
func TestBuildIndexedTagName(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		index int64
		want  string
	}{
		{"index zero appends 1", "abcd", 0, "IKBabcd1"},
		{"index nine appends 10", "abcd", 9, "IKBabcd10"},
		// 20 个 x：base 先截 15 字节（IKB+12x），再截到 15-1=14 字节（IKB+11x）+ "1"。
		{"long raw truncated", strings.Repeat("x", 20), 0, "IKB" + strings.Repeat("x", 11) + "1"},
	}
	for _, tc := range cases {
		if got := BuildIndexedTagName(tc.raw, tc.index); got != tc.want {
			t.Errorf("%s: BuildIndexedTagName(%q, %d) = %q, want %q", tc.name, tc.raw, tc.index, got, tc.want)
		}
	}
}

// TestIpGroupHashSuffixStable 对齐 tag_name.rs L61-82：
// hash_token_letters 取 md5(raw.trim()) 前 2 字节，各 %26+'A'，'R' 映射为 'S'（永不出现 R）。
// 锚点值手工推导：md5("abcd")=e2fc714c...，0xe2%26=18→'S'，0xfc%26=18→'S'，故后缀 "RSS"；
// md5(20 个 x)=baf1da0e...，0xba%26=4→'E'，0xf1%26=7→'H'，故后缀 "REH"。
func TestIpGroupHashSuffixStable(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		index int64
		want  string
	}{
		// base=IKBabcd(7 字节) 无需截断（max=15-3-1=11），拼 "RSS"+"1"。
		{"abcd index 0", "abcd", 0, "IKBabcdRSS1"},
		{"abcd index 9", "abcd", 9, "IKBabcdRSS10"},
		// base 截到 15-3-1=11 字节 = IKB+8x，拼 "REH"+"1"。
		{"long raw", strings.Repeat("x", 20), 0, "IKB" + strings.Repeat("x", 8) + "REH1"},
		// hash_token_letters 内部 trim（L63），前后空白不影响哈希。
		{"raw with spaces", " abcd ", 0, "IKBabcdRSS1"},
	}
	for _, tc := range cases {
		if got := BuildIndexedIpGroupTagName(tc.raw, tc.index); got != tc.want {
			t.Errorf("%s: BuildIndexedIpGroupTagName(%q, %d) = %q, want %q", tc.name, tc.raw, tc.index, got, tc.want)
		}
	}
	if BuildIndexedIpGroupTagName("abcd", 0) != BuildIndexedIpGroupTagName("abcd", 0) {
		t.Fatal("same input must produce same output")
	}
	// 'R' 会被替换成 'S'（L69-71），任意输入都不该出现第二个 'R' 之外的 'R'：
	// 即后缀两字母均不为 'R'，且 marker 'R' 只出现一次。
	for _, raw := range []string{"abcd", strings.Repeat("x", 20), "运营商", "a", "", "IKUAI_BYPASS_x"} {
		got := BuildIndexedIpGroupTagName(raw, 3)
		if strings.Count(got, "R") != 1 {
			t.Errorf("raw %q: got %q, want exactly one marker R", raw, got)
		}
	}
}

// TestMatchTagNameFilter 对齐 tag_name.rs L114-139 的完整分支：
// L115-117 空 filter 恒真；L118-123 IKB 前缀命中；L125-133 截断兜底（去尾数字 + 剥 R?? 随机后缀）；
// L134-136 legacy comment 包含命中。
func TestMatchTagNameFilter(t *testing.T) {
	cases := []struct {
		name          string
		filterTag     string
		currentName   string
		legacyComment string
		want          bool
	}{
		{"empty filter", "", "whatever", "", true},
		{"blank filter", "   ", "whatever", "", true},
		{"ikuai prefix hit", "mytag", "IKBmytag1", "", true},
		{"comma candidates first", "foo,bar", "IKBbar2", "", true},
		{"comma candidates second", "foo,bar", "IKBfoo9", "", true},
		// L125-133：name 被爱快 15 字节限制截断后，候选词以截断名为前缀即命中。
		{"truncated name fallback", "SafeChunkRoute", "IKBSafeChunkRou", "", true},
		{"truncated name with index", "SafeChunkRoute", "IKBSafeChunkRou3", "", true},
		// L134-136：旧备注包含候选词。
		{"legacy comment hit", "mytag", "unrelated", "joyanhui/ikuai-bypass IKBmytag x", true},
		// L118：非 IKB 开头且备注为空 → 不命中。
		{"unmanaged name miss", "mytag", "mytag", "", false},
		{"legacy empty miss", "mytag", "unrelated", "", false},
		{"legacy mismatch miss", "mytag", "unrelated", "other stuff", false},
		// L99-112：filter 全被清洗掉 → 无候选 → false。
		{"sanitized away filter", "###", "IKBfoo", "", false},
		{"candidate mismatch", "other", "IKBmytag1", "", false},
		// L118/L120 均对 current_name 做 trim。
		{"current name trimmed", "foo", "  IKBfoo1  ", "", true},
	}
	for _, tc := range cases {
		if got := MatchTagNameFilter(tc.filterTag, tc.currentName, tc.legacyComment); got != tc.want {
			t.Errorf("%s: MatchTagNameFilter(%q, %q, %q) = %v, want %v",
				tc.name, tc.filterTag, tc.currentName, tc.legacyComment, got, tc.want)
		}
	}
}

// TestMatchTagNameFilterIpGroupRoundTrip 端到端兜底：IP 分组长名被截断并带 R?? 后缀后，
// 仍能被同名 filter 识别（L128-132 去尾数字 + strip_ip_group_rand_affix L141-159）。
func TestMatchTagNameFilterIpGroupRoundTrip(t *testing.T) {
	raw := strings.Repeat("x", 20)
	name := BuildIndexedIpGroupTagName(raw, 0) // "IKBxxxxxxxxREH1"
	if !MatchTagNameFilter(raw, name, "") {
		t.Fatalf("ip-group truncated name %q must match its own filter", name)
	}
}

// TestManagedCommentMarkers 对齐 crates/core/src/ikuai/types.rs L12-17 的固定顺序。
func TestManagedCommentMarkers(t *testing.T) {
	got := ManagedCommentMarkers()
	want := [3]string{NewComment, LegacyRepoComment, CommentIkuaiBypass}
	if got != want {
		t.Fatalf("ManagedCommentMarkers() = %v, want %v", got, want)
	}
	if want != [3]string{"IkuaiBypass", "joyanhui/ikuai-bypass", "IKUAI_BYPASS"} {
		t.Fatalf("marker constants drifted: %v", want)
	}
}
