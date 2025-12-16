package controller

import (
	"fmt"
	"k8s-eni-tagger/pkg/utils"
	"testing"
)

// BenchmarkComputeHash benchmarks the computeHash function with various tag counts
func BenchmarkComputeHash(b *testing.B) {
	// Test with different numbers of tags
	testCases := []struct {
		name string
		tags map[string]string
	}{
		{
			name: "Empty tags",
			tags: map[string]string{},
		},
		{
			name: "Single tag",
			tags: map[string]string{"key1": "value1"},
		},
		{
			name: "5 tags",
			tags: map[string]string{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
				"key4": "value4",
				"key5": "value5",
			},
		},
		{
			name: "10 tags",
			tags: map[string]string{
				"key1":  "value1",
				"key2":  "value2",
				"key3":  "value3",
				"key4":  "value4",
				"key5":  "value5",
				"key6":  "value6",
				"key7":  "value7",
				"key8":  "value8",
				"key9":  "value9",
				"key10": "value10",
			},
		},
		{
			name: "20 tags",
			tags: generateTestTags(20),
		},
		{
			name: "50 tags (AWS max)",
			tags: generateTestTags(50),
		},
	}
	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = computeHash(tc.tags)
			}
		})
	}
}

// BenchmarkApplyNamespace benchmarks the applyNamespace function
func BenchmarkApplyNamespace(b *testing.B) {
	tags := generateTestTags(10)
	namespace := "test-namespace"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = applyNamespace(tags, namespace)
	}
}

// BenchmarkBuildKeyValue benchmarks the BuildKeyValue utility function
func BenchmarkBuildKeyValue(b *testing.B) {
	key := "test-key"
	value := "test-value"
	separator := ":"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = utils.BuildKeyValue(key, value, separator)
	}
}

// BenchmarkBuildCommaSeparatedList benchmarks the BuildCommaSeparatedList function
func BenchmarkBuildCommaSeparatedList(b *testing.B) {
	parts := []string{"subnet-1", "subnet-2", "subnet-3", "subnet-4", "subnet-5"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = utils.BuildCommaSeparatedList(parts)
	}
}

// generateTestTags creates a map of test tags with the specified count
func generateTestTags(count int) map[string]string {
	tags := make(map[string]string, count)
	for i := 1; i <= count; i++ {
		tags[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}
	return tags
}

// Note: Correctness tests for BuildKeyValue and BuildCommaSeparatedList are in utils_test.go
// This file only contains benchmarks to avoid test duplication
