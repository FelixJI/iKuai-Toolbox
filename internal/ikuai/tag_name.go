// tag_name.go IKB 标签构建与匹配，字节级对齐 rust_archive/crates/core/src/ikuai/tag_name.rs。
// Tag name building and matching, byte-aligned with rust_archive/crates/core/src/ikuai/tag_name.rs.
package ikuai

import (
	"crypto/md5"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// tagSanitizer 对齐 tag_name.rs L7-8：仅保留汉字、英文字母和数字。
// tagSanitizer mirrors tag_name.rs L7-8: keep Han characters, letters and digits only.
var tagSanitizer = regexp.MustCompile(`[^\p{Han}A-Za-z0-9]+`)

// maxTagNameLength 爱快 4.0.101 的 tagname 长度限制，对齐 tag_name.rs L10。
// maxTagNameLength is the iKuai 4.0.101 tagname limit, mirroring tag_name.rs L10.
const maxTagNameLength = 15

// ipGroupRandMarker IP 分组确定性随机后缀的标记字母，对齐 tag_name.rs L12。
// ipGroupRandMarker is the marker letter of the ip-group deterministic suffix, tag_name.rs L12.
const ipGroupRandMarker = "R"

// trimStartMatchesPrefix 复刻 Rust str::trim_start_matches：剥掉全部重复出现的指定前缀。
// trimStartMatchesPrefix replicates Rust str::trim_start_matches: strips every repeated occurrence of prefix.
func trimStartMatchesPrefix(s, prefix string) string {
	for strings.HasPrefix(s, prefix) {
		s = s[len(prefix):]
	}
	return s
}

// stripKnownPrefix 对齐 tag_name.rs L14-24：剥 "IKUAI_BYPASS_"（全部重复）与 "IKB"（全部重复），各步骤后 trim。
// stripKnownPrefix mirrors tag_name.rs L14-24: strip "IKUAI_BYPASS_" (all repeats) then "IKB" (all repeats), trimming after each step.
func stripKnownPrefix(raw string) string {
	s := strings.TrimSpace(raw)
	legacy := CommentIkuaiBypass + "_"
	if strings.HasPrefix(s, legacy) {
		s = strings.TrimSpace(trimStartMatchesPrefix(s, legacy))
	}
	if strings.HasPrefix(s, NamePrefixIKB) {
		s = strings.TrimSpace(trimStartMatchesPrefix(s, NamePrefixIKB))
	}
	return s
}

// SanitizeTagName 对齐 tag_name.rs L26-32：剥已知前缀后移除全部非 [Han A-Za-z0-9] 字符。
// SanitizeTagName mirrors tag_name.rs L26-32: strip known prefixes then drop every non-[Han A-Za-z0-9] run.
func SanitizeTagName(raw string) string {
	return tagSanitizer.ReplaceAllString(stripKnownPrefix(raw), "")
}

// BuildTagName 对齐 tag_name.rs L34-43："IKB"+清洗后的 token，UTF-8 安全截到 15 字节；空 token 只返回 "IKB"。
// BuildTagName mirrors tag_name.rs L34-43: "IKB"+sanitized token truncated UTF-8-safely to 15 bytes; bare "IKB" for an empty token.
func BuildTagName(raw string) string {
	token := SanitizeTagName(raw)
	if token == "" {
		return NamePrefixIKB
	}
	return truncateUTF8ByBytes(NamePrefixIKB+token, maxTagNameLength)
}

// BuildIndexedTagName 对齐 tag_name.rs L45-51：base 截到 15-len(suffix) 字节后拼十进制尾缀 index+1。
// BuildIndexedTagName mirrors tag_name.rs L45-51: base truncated to 15-len(suffix) bytes then the decimal suffix index+1.
func BuildIndexedTagName(raw string, index int64) string {
	suffix := strconv.FormatInt(index+1, 10)
	maxBaseLen := saturatingSub(maxTagNameLength, len(suffix))
	base := truncateUTF8ByBytes(BuildTagName(raw), maxBaseLen)
	return base + suffix
}

// buildIndexedTagNameWithAffix 对齐 tag_name.rs L53-59：base 截到 15-len(affix)-len(suffix) 字节后拼 affix+尾缀。
// buildIndexedTagNameWithAffix mirrors tag_name.rs L53-59: base truncated to 15-len(affix)-len(suffix) bytes, then affix+suffix.
func buildIndexedTagNameWithAffix(raw, affix string, index int64) string {
	suffix := strconv.FormatInt(index+1, 10)
	maxBaseLen := saturatingSub(maxTagNameLength, len(affix)+len(suffix))
	base := truncateUTF8ByBytes(BuildTagName(raw), maxBaseLen)
	return base + affix + suffix
}

// hashTokenLetters 对齐 tag_name.rs L61-74：md5(raw.trim()) 前 len 字节各 %26+'A'，'R' 映射为 'S'。
// hashTokenLetters mirrors tag_name.rs L61-74: each of the first len bytes of md5(raw.trim()) maps to %26+'A', with 'R' rewritten to 'S'.
func hashTokenLetters(raw string, length int) string {
	if length < 1 {
		length = 1
	}
	if length > 6 {
		length = 6
	}
	digest := md5.Sum([]byte(strings.TrimSpace(raw)))
	out := make([]byte, 0, length)
	for i := 0; i < length; i++ {
		ch := digest[i]%26 + 'A'
		// Avoid using marker letter to make parsing easier.
		// 避免使用 marker 字母，便于解析。
		if ch == 'R' {
			ch = 'S'
		}
		out = append(out, ch)
	}
	return string(out)
}

// BuildIndexedIpGroupTagName 对齐 tag_name.rs L78-82：IP 分组用 "R"+2 位确定性字母作随机后缀。
// BuildIndexedIpGroupTagName mirrors tag_name.rs L78-82: ip-groups use "R"+2 deterministic letters as the random-like suffix.
func BuildIndexedIpGroupTagName(raw string, index int64) string {
	affix := ipGroupRandMarker + hashTokenLetters(raw, 2)
	return buildIndexedTagNameWithAffix(raw, affix, index)
}

// saturatingSub 复刻 usize::saturating_sub，防止尾缀超长时出现负长度。
// saturatingSub replicates usize::saturating_sub so an overlong suffix cannot produce a negative length.
func saturatingSub(a, b int) int {
	if a < b {
		return 0
	}
	return a - b
}

// truncateUTF8ByBytes 对齐 tag_name.rs L84-97：按字节长度截断且不切断多字节字符。
// truncateUTF8ByBytes mirrors tag_name.rs L84-97: truncate by byte budget without splitting a multi-byte character.
func truncateUTF8ByBytes(s string, maxBytes int) string {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(s) <= maxBytes {
		return s
	}
	end := 0
	for i, ch := range s {
		next := i + utf8.RuneLen(ch)
		if next > maxBytes {
			break
		}
		end = next
	}
	return s[:end]
}

// buildTagNameCandidates 对齐 tag_name.rs L99-112：逗号分词清洗后生成去重候选，按字典序返回（BTreeSet 语义）。
// buildTagNameCandidates mirrors tag_name.rs L99-112: comma-split sanitized deduped candidates in lexicographic order (BTreeSet semantics).
func buildTagNameCandidates(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	set := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		token := SanitizeTagName(part)
		if token != "" {
			set[NamePrefixIKB+token] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MatchTagNameFilter 对齐 tag_name.rs L114-139：空 filter 恒真；IKB 前缀命中；
// 截断兜底（去尾数字 + 剥 R?? 随机后缀后候选词以截断名为前缀）；legacy comment 包含命中。
// MatchTagNameFilter mirrors tag_name.rs L114-139: empty filter matches all; IKB-prefix hit;
// truncation fallback (strip trailing digits and the R?? suffix, then candidate startswith base); legacy comment containment hit.
func MatchTagNameFilter(filterTag, currentName, legacyComment string) bool {
	if strings.TrimSpace(filterTag) == "" {
		return true
	}
	name := strings.TrimSpace(currentName)
	isManaged := strings.HasPrefix(name, NamePrefixIKB)
	for _, c := range buildTagNameCandidates(filterTag) {
		if isManaged && strings.HasPrefix(name, c) {
			return true
		}

		// Handle auto-truncated names caused by iKuai tagname length limit.
		// 兼容爱快 tagname 长度限制导致的自动截断（避免无法识别/无法原地更新）。
		if isManaged {
			base0 := strings.TrimRightFunc(name, func(r rune) bool {
				return r >= '0' && r <= '9'
			})
			base := stripIpGroupRandAffix(base0)
			if base != "" && strings.HasPrefix(c, base) {
				return true
			}
		}
		if legacyComment != "" && strings.Contains(legacyComment, c) {
			return true
		}
	}
	return false
}

// stripIpGroupRandAffix 对齐 tag_name.rs L141-159：结尾形如 "R"+2 位大写字母的确定性后缀被剥掉。
// stripIpGroupRandAffix mirrors tag_name.rs L141-159: strips a trailing "R"+2-uppercase-letter deterministic suffix.
func stripIpGroupRandAffix(s string) string {
	n := len(s)
	if n < 3 {
		return s
	}
	if s[n-3] != 'R' {
		return s
	}
	a, c := s[n-2], s[n-1]
	if !(a >= 'A' && a <= 'Z' && c >= 'A' && c <= 'Z') {
		return s
	}
	// ASCII suffix => safe to slice by bytes.
	// ASCII 后缀 => 按字节切片安全。
	return s[:n-3]
}
