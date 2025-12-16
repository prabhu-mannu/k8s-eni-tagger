package controller

import (
	"strings"
	"testing"
)

// TestParseTags tests the parseTags function with various formats
func TestParseTags(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name:    "empty string",
			input:   "",
			want:    map[string]string{},
			wantErr: false,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			want:    map[string]string{},
			wantErr: false,
		},
		{
			name:    "valid JSON single tag",
			input:   `{"key":"value"}`,
			want:    map[string]string{"key": "value"},
			wantErr: false,
		},
		{
			name:    "valid JSON multiple tags",
			input:   `{"CostCenter":"1234","Team":"Platform"}`,
			want:    map[string]string{"CostCenter": "1234", "Team": "Platform"},
			wantErr: false,
		},
		{
			name:    "valid comma-separated single",
			input:   "key=value",
			want:    map[string]string{"key": "value"},
			wantErr: false,
		},
		{
			name:    "valid comma-separated multiple",
			input:   "CostCenter=1234,Team=Platform",
			want:    map[string]string{"CostCenter": "1234", "Team": "Platform"},
			wantErr: false,
		},
		{
			name:    "comma-separated with spaces",
			input:   " CostCenter = 1234 , Team = Platform ",
			want:    map[string]string{"CostCenter": "1234", "Team": "Platform"},
			wantErr: false,
		},
		{
			name:    "empty value allowed",
			input:   `{"key":""}`,
			want:    map[string]string{"key": ""},
			wantErr: false,
		},
		{
			name:    "invalid format no equals",
			input:   "keyvalue",
			wantErr: true,
		},
		{
			name:    "empty key",
			input:   "=value",
			wantErr: true,
		},
		{
			name:    "reserved prefix aws:",
			input:   `{"aws:test":"value"}`,
			wantErr: true,
		},
		{
			name:    "reserved prefix kubernetes.io/cluster/",
			input:   `{"kubernetes.io/cluster/test":"value"}`,
			wantErr: true,
		},
		{
			name:    "key too long",
			input:   `{"` + strings.Repeat("a", 128) + `":"value"}`,
			wantErr: true,
		},
		{
			name:    "key at max length",
			input:   `{"` + strings.Repeat("a", 127) + `":"value"}`,
			want:    map[string]string{strings.Repeat("a", 127): "value"},
			wantErr: false,
		},
		{
			name:    "value too long",
			input:   `{"key":"` + strings.Repeat("a", 256) + `"}`,
			wantErr: true,
		},
		{
			name:    "value at max length",
			input:   `{"key":"` + strings.Repeat("a", 255) + `"}`,
			want:    map[string]string{"key": strings.Repeat("a", 255)},
			wantErr: false,
		},
		{
			name:    "annotation value too long",
			input:   strings.Repeat("a", MaxAnnotationValueLength+1),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTags(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTags() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("parseTags() got %d tags, want %d", len(got), len(tt.want))
					return
				}
				for k, v := range tt.want {
					if got[k] != v {
						t.Errorf("parseTags() got[%q] = %q, want %q", k, got[k], v)
					}
				}
			}
		})
	}
}

// TestValidateParsedTags_TagLimit tests the 50 tag limit boundary
func TestValidateParsedTags_TagLimit(t *testing.T) {
	tests := []struct {
		name     string
		tagCount int
		wantErr  bool
	}{
		{"49 tags - under limit", 49, false},
		{"50 tags - at limit", 50, false},
		{"51 tags - over limit", 51, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := make(map[string]string)
			for i := 0; i < tt.tagCount; i++ {
				tags[strings.Repeat("k", 10)+string(rune('a'+i%26))+string(rune('0'+i/26))] = "value"
			}
			_, err := validateParsedTags(tags)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateParsedTags() with %d tags: error = %v, wantErr %v", tt.tagCount, err, tt.wantErr)
			}
		})
	}
}

// TestApplyNamespace_Comprehensive tests namespace prefix application with edge cases
func TestApplyNamespace_Comprehensive(t *testing.T) {
	tests := []struct {
		name      string
		tags      map[string]string
		namespace string
		want      map[string]string
		wantErr   bool
	}{
		{
			name:      "empty namespace - no change",
			tags:      map[string]string{"key": "value"},
			namespace: "",
			want:      map[string]string{"key": "value"},
			wantErr:   false,
		},
		{
			name:      "valid namespace",
			tags:      map[string]string{"CostCenter": "1234"},
			namespace: "acme-corp",
			want:      map[string]string{"acme-corp:CostCenter": "1234"},
			wantErr:   false,
		},
		{
			name:      "namespace creates reserved prefix aws:",
			tags:      map[string]string{"test": "value"},
			namespace: "aws",
			wantErr:   true,
		},
		{
			name:      "namespace key too long after prefixing",
			tags:      map[string]string{strings.Repeat("k", 120): "value"},
			namespace: "longnamespace",
			wantErr:   true,
		},
		{
			name:      "namespace key at exact max length",
			tags:      map[string]string{strings.Repeat("k", 115): "value"}, // 115 + 1 (":") + 11 ("testns") = 127
			namespace: "testnslong",
			want:      map[string]string{"testnslong:" + strings.Repeat("k", 115): "value"},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyNamespace(tt.tags, tt.namespace)
			if (err != nil) != tt.wantErr {
				t.Errorf("applyNamespace() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("applyNamespace() got %d tags, want %d", len(got), len(tt.want))
					return
				}
				for k, v := range tt.want {
					if got[k] != v {
						t.Errorf("applyNamespace() got[%q] = %q, want %q", k, got[k], v)
					}
				}
			}
		})
	}
}

// Note: TestIsRetryableError is now in pkg/utils/utils_test.go

// TestComputeHash_EmptyMap tests hash of empty tag map
func TestComputeHash_EmptyMap(t *testing.T) {
	h := computeHash(map[string]string{})
	if h == "" {
		t.Fatal("computeHash of empty map should not be empty string")
	}
	if len(h) != 32 {
		t.Fatalf("expected hash length 32, got %d", len(h))
	}
}

// TestComputeHash_SpecialCharacters tests hash with AWS-allowed special chars
func TestComputeHash_SpecialCharacters(t *testing.T) {
	tags := map[string]string{
		"key-with-dash":  "value_with_underscore",
		"key.with.dot":   "value:with:colon",
		"key/with/slash": "value@with@at",
		"key+with+plus":  "value=with=equals",
	}
	h1 := computeHash(tags)
	h2 := computeHash(tags)
	if h1 != h2 {
		t.Fatalf("computeHash with special chars not deterministic: %q != %q", h1, h2)
	}
}

func TestComputeHash_DeterministicAndLength(t *testing.T) {
	tags := map[string]string{"b": "2", "a": "1"}
	h1 := computeHash(tags)
	h2 := computeHash(tags)
	if h1 != h2 {
		t.Fatalf("computeHash not deterministic: %q != %q", h1, h2)
	}
	// Hash is now 32 chars (128 bits) for collision resistance
	if len(h1) != 32 {
		t.Fatalf("expected hash length 32, got %d", len(h1))
	}
}

func TestComputeHash_OrderIndependence(t *testing.T) {
	t1 := map[string]string{"a": "1", "b": "2", "c": "3"}
	t2 := map[string]string{"c": "3", "b": "2", "a": "1"}
	if computeHash(t1) != computeHash(t2) {
		t.Fatalf("hash should be independent of map insertion order")
	}
}

func TestComputeHash_Uniqueness(t *testing.T) {
	m1 := map[string]string{"a": "1"}
	m2 := map[string]string{"a": "2"}
	if computeHash(m1) == computeHash(m2) {
		t.Fatalf("different tag sets produced the same hash")
	}
}
