// clean.go 受管规则识别与清理标签匹配，行为对齐 crates/core/src/ikuai/clean.rs。
// Managed-rule detection and clean-tag matching, aligned with clean.rs.
package ikuai

import "strings"

// IsManaged 对齐 clean.rs L3-11：名字以 IKB 前缀开头，或备注包含任一历史标记
// （IkuaiBypass / joyanhui/ikuai-bypass / IKUAI_BYPASS）。名字参与判断前 trim，备注不 trim。
// IsManaged mirrors clean.rs L3-11: the (trimmed) name starts with the IKB prefix,
// or the (untrimmed) comment contains any historical marker.
func IsManaged(comment, name string) bool {
	if strings.HasPrefix(strings.TrimSpace(name), NamePrefixIKB) {
		return true
	}
	for _, marker := range ManagedCommentMarkers() {
		if strings.Contains(comment, marker) {
			return true
		}
	}
	return false
}

// MatchCleanTag 对齐 clean.rs L13-35：空 cleanTag 恒不删；非受管规则恒不删；
// cleanAll 删全部受管；否则要求 legacy（备注）或 current（名字）等于或包含 cleanTag。
// MatchCleanTag mirrors clean.rs L13-35: an empty cleanTag never deletes;
// unmanaged rules never delete; cleanAll deletes every managed rule;
// otherwise the legacy comment or current name must equal or contain the cleanTag.
func MatchCleanTag(cleanTag, legacyTagName, currentTagName string) bool {
	cleanTag = strings.TrimSpace(cleanTag)
	if cleanTag == "" {
		return false
	}
	if !IsManaged(legacyTagName, currentTagName) {
		return false
	}
	if cleanTag == CleanModeAll {
		return true
	}
	if legacyTagName != "" &&
		(legacyTagName == cleanTag || strings.Contains(legacyTagName, cleanTag)) {
		return true
	}
	if currentTagName != "" &&
		(currentTagName == cleanTag || strings.Contains(currentTagName, cleanTag)) {
		return true
	}
	return false
}
