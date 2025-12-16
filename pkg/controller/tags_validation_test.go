package controller

import (
	"fmt"
	"strings"
	"testing"
)

/*
Current behavior vs Desired behavior

Current behavior (locked tests assert these):
 - tag key/value regexes only allow ASCII alphanumerics and the characters " + = . _ : / @ -"
 - reserved prefixes are matched case-sensitively (e.g., "aws:" is reserved, "AWS:" is allowed)
 - JSON parsing preserves whitespace in values; comma-separated format trims keys/values

Desired / future behavior (spec tests are marked skipped):
 - Allow Unicode characters in keys/values (Unicode-aware regex)
 - Make reserved-prefix checks case-insensitive
 - Ensure anchoring behaves strictly for newline control characters (if behavior changes)

These tests lock current behavior and include skipped tests for desirable future changes.
*/

func TestValidateParsedTags_KeyValidation(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"simple", "Key", false},
		{"min-one", "a", false},
		{"max-len", strings.Repeat("k", MaxTagKeyLength), false},
		{"too-long", strings.Repeat("k", MaxTagKeyLength+1), true},
		{"space", "with space", false},
		{"allowed-chars", "A+=._:/@-", false},
		{"empty-key", "", true},
		{"newline", "bad\nkey", true},
		{"tab", "bad\tkey", true},
		{"unicode", "键", true}, // current behavior: Unicode NOT allowed
		{"reserved-aws", "aws:foo", true},
		{"reserved-k8s", "kubernetes.io/cluster/mycluster", true},
		{"invalid-char", "no!bang", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tags := map[string]string{tc.key: "val"}
			_, err := validateParsedTags(tags)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateParsedTags(%q) error = %v, wantErr=%v", tc.key, err, tc.wantErr)
			}
		})
	}
}

func TestValidateParsedTags_ValueValidation(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty", "", false},
		{"simple", "v", false},
		{"max-len", strings.Repeat("v", MaxTagValueLength), false},
		{"too-long", strings.Repeat("v", MaxTagValueLength+1), true},
		{"space", "with space", false},
		{"allowed-chars", "A+=._:/@-", false},
		{"newline", "bad\nvalue", true},
		{"tab", "bad\tvalue", true},
		{"unicode", "値", true}, // current behavior: Unicode NOT allowed
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tags := map[string]string{"k": tc.value}
			_, err := validateParsedTags(tags)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateParsedTags(value=%q) error = %v, wantErr=%v", tc.value, err, tc.wantErr)
			}
		})
	}
}

func TestParseTags_JSONAndCommaFormats(t *testing.T) {
	t.Run("json-preserves-whitespace", func(t *testing.T) {
		in := `{"A":" 1 ", "B":"2"}`
		tags, err := parseTags(in)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if tags["A"] != " 1 " {
			t.Fatalf("expected json value to preserve whitespace, got %q", tags["A"])
		}
	})

	t.Run("comma-trims-values-and-keys", func(t *testing.T) {
		in := "A= 1 , B =2"
		tags, err := parseTags(in)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if tags["A"] != "1" || tags["B"] != "2" {
			t.Fatalf("comma parsing did not trim appropriately: %#v", tags)
		}
	})

	t.Run("comma-empty-item-error", func(t *testing.T) {
		_, err := parseTags("a=1,,b=2")
		if err == nil {
			t.Fatalf("expected error for empty item, got nil")
		}
	})

	t.Run("comma-empty-key-error", func(t *testing.T) {
		_, err := parseTags("=1")
		if err == nil {
			t.Fatalf("expected error for empty key, got nil")
		}
	})
}

func TestMaxTagsPerENI_Enforced(t *testing.T) {
	t.Run("too-many-tags", func(t *testing.T) {
		pairs := make([]string, MaxTagsPerENI+1)
		for i := 0; i < MaxTagsPerENI+1; i++ {
			pairs[i] = fmt.Sprintf("k%d=v%d", i, i)
		}
		in := strings.Join(pairs, ",")
		_, err := parseTags(in)
		if err == nil {
			t.Fatalf("expected error for >MaxTagsPerENI, got nil")
		}
	})
}

func TestReservedPrefix_CaseSensitivity(t *testing.T) {
	t.Run("reserved-lowercase", func(t *testing.T) {
		_, err := parseTags("aws:tag=1")
		if err == nil {
			t.Fatalf("expected reserved prefix to be rejected (aws:)")
		}
	})

	t.Run("reserved-uppercase-allowed-currently", func(t *testing.T) {
		// Current behavior: reserved check is case-sensitive, so "AWS:" is allowed.
		tags, err := parseTags("AWS:tag=1")
		if err != nil {
			t.Fatalf("unexpected error for AWS: prefix (case-sensitive allowed): %v", err)
		}
		if tags["AWS:tag"] != "1" {
			t.Fatalf("expected key preserved: %#v", tags)
		}
	})

	t.Run("desired-case-insensitive-reserved-prefix", func(t *testing.T) {
		t.Skip("TODO: enable when reserved-prefix checks become case-insensitive")
		_, err := parseTags("AWS:tag=1")
		if err == nil {
			t.Fatalf("expected AWS: to be rejected once we make reserved prefix checks case-insensitive")
		}
	})
}

func TestAnchoringAndControlChars_Specs(t *testing.T) {
	t.Run("newline-in-key-is-rejected", func(t *testing.T) {
		_, err := parseTags("bad\nkey=1")
		if err == nil {
			t.Fatalf("expected newline in key to be rejected")
		}
	})

	t.Run("desired-unicode-support", func(t *testing.T) {
		t.Skip("TODO: enable when regex is updated to allow Unicode characters in keys/values")
		_, err := parseTags("键=値")
		if err != nil {
			t.Fatalf("expected unicode keys/values to be allowed when regex is updated: %v", err)
		}
	})
}
