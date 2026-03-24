package accounting

import "testing"

func TestRuleMatchesEq(t *testing.T) {
	rule := MappingRule{MatchField: "description", MatchOperator: "eq", MatchValue: "bankgebyr"}
	if !ruleMatches(rule, "Bankgebyr", "", "") {
		t.Error("eq should be case-insensitive")
	}
	if ruleMatches(rule, "Bankgebyr mars", "", "") {
		t.Error("eq should not match substrings")
	}
}

func TestRuleMatchesLike(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		{"%forsikring%", "Betaling forsikring 2026", true},
		{"%forsikring%", "Forsikring", true},
		{"%forsikring%", "Strøm", false},
		{"strøm%", "Strøm og oppvarming", true},
		{"strøm%", "Betaling strøm", false},
		{"%gebyr", "Bankgebyr", true},
		{"%gebyr", "Gebyrer bank", false},
		{"%", "anything", true},
		{"exact", "exact", true},
		{"exact", "not exact", false},
	}

	for _, tt := range tests {
		rule := MappingRule{MatchField: "description", MatchOperator: "like", MatchValue: tt.pattern}
		got := ruleMatches(rule, tt.value, "", "")
		if got != tt.want {
			t.Errorf("like %q against %q = %v, want %v", tt.pattern, tt.value, got, tt.want)
		}
	}
}

func TestRuleMatchesRegex(t *testing.T) {
	rule := MappingRule{MatchField: "description", MatchOperator: "regex", MatchValue: `^Vipps.*innbetaling`}
	if !ruleMatches(rule, "Vipps test innbetaling", "", "") {
		t.Error("regex should match")
	}
	if ruleMatches(rule, "Bankoverføring Vipps", "", "") {
		t.Error("regex should not match (no anchor)")
	}
}

func TestRuleMatchesRegexInvalid(t *testing.T) {
	rule := MappingRule{MatchField: "description", MatchOperator: "regex", MatchValue: `[invalid`}
	if ruleMatches(rule, "anything", "", "") {
		t.Error("invalid regex should not match")
	}
}

func TestRuleMatchesCounterpart(t *testing.T) {
	rule := MappingRule{MatchField: "counterpart", MatchOperator: "like", MatchValue: "%kommune%"}
	if !ruleMatches(rule, "", "Moss Kommune", "") {
		t.Error("counterpart should match")
	}
	if ruleMatches(rule, "Kommune", "", "") {
		t.Error("should not match description field")
	}
}

func TestRuleMatchesKIDPrefix(t *testing.T) {
	rule := MappingRule{MatchField: "kid_prefix", MatchOperator: "like", MatchValue: "000%"}
	if !ruleMatches(rule, "", "", "000123456") {
		t.Error("kid_prefix should match")
	}
	if ruleMatches(rule, "", "", "999123456") {
		t.Error("kid_prefix should not match different prefix")
	}
}

func TestRuleMatchesUnknownField(t *testing.T) {
	rule := MappingRule{MatchField: "nonexistent", MatchOperator: "eq", MatchValue: "test"}
	if ruleMatches(rule, "test", "test", "test") {
		t.Error("unknown field should not match")
	}
}

func TestRuleMatchesUnknownOperator(t *testing.T) {
	rule := MappingRule{MatchField: "description", MatchOperator: "unknown", MatchValue: "test"}
	if ruleMatches(rule, "test", "", "") {
		t.Error("unknown operator should not match")
	}
}

func TestLikeMatch(t *testing.T) {
	tests := []struct {
		value   string
		pattern string
		want    bool
	}{
		{"hello world", "%world", true},
		{"hello world", "hello%", true},
		{"hello world", "%lo wo%", true},
		{"hello", "hello", true},
		{"hello", "world", false},
		{"", "%", true},
		{"abc", "%", true},
	}

	for _, tt := range tests {
		got := likeMatch(tt.value, tt.pattern)
		if got != tt.want {
			t.Errorf("likeMatch(%q, %q) = %v, want %v", tt.value, tt.pattern, got, tt.want)
		}
	}
}
