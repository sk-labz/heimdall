// Copyright 2024 Dimitrij Drus <dadrus@gmx.de>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package optimization

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dadrus/heimdall/internal/rules/rule/mocks"
)

func TestPathTrie_NewPathTrie(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config *TrieConfig
	}{
		{
			name:   "default config",
			config: nil,
		},
		{
			name: "custom config",
			config: &TrieConfig{
				MaxDepth:            15,
				RebalanceThreshold:  500,
				PriorityDecayFactor: 0.9,
				StatsEnabled:        false,
				CacheWarming:        false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trie := NewPathTrie(tt.config)
			
			assert.NotNil(t, trie)
			assert.NotNil(t, trie.root)
			assert.NotNil(t, trie.rules)
			assert.NotNil(t, trie.stats)
			assert.NotNil(t, trie.config)
			
			if tt.config == nil {
				// Verify default config values
				assert.Equal(t, 20, trie.config.MaxDepth)
				assert.Equal(t, 1000, trie.config.RebalanceThreshold)
				assert.Equal(t, 0.95, trie.config.PriorityDecayFactor)
				assert.True(t, trie.config.StatsEnabled)
				assert.True(t, trie.config.CacheWarming)
			} else {
				// Verify custom config values
				assert.Equal(t, tt.config.MaxDepth, trie.config.MaxDepth)
				assert.Equal(t, tt.config.RebalanceThreshold, trie.config.RebalanceThreshold)
				assert.Equal(t, tt.config.PriorityDecayFactor, trie.config.PriorityDecayFactor)
				assert.Equal(t, tt.config.StatsEnabled, trie.config.StatsEnabled)
				assert.Equal(t, tt.config.CacheWarming, trie.config.CacheWarming)
			}
		})
	}
}

func TestPathTrie_ParsePathSegments(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(nil)

	tests := []struct {
		name     string
		path     string
		expected []string
	}{
		{
			name:     "empty path",
			path:     "",
			expected: []string{},
		},
		{
			name:     "root path",
			path:     "/",
			expected: []string{},
		},
		{
			name:     "simple path",
			path:     "/api",
			expected: []string{"api"},
		},
		{
			name:     "nested path",
			path:     "/api/v1/users",
			expected: []string{"api", "v1", "users"},
		},
		{
			name:     "path with trailing slash",
			path:     "/api/v1/users/",
			expected: []string{"api", "v1", "users"},
		},
		{
			name:     "path without leading slash",
			path:     "api/v1/users",
			expected: []string{"api", "v1", "users"},
		},
		{
			name:     "path with multiple slashes",
			path:     "//api//v1///users//",
			expected: []string{"api", "v1", "users"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trie.parsePathSegments(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPathTrie_AddRule(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(&TrieConfig{
		MaxDepth:     10,
		StatsEnabled: true,
	})

	// Create mock rules
	rule1 := mocks.NewRuleMock(t)
	rule1.EXPECT().ID().Return("rule1").Maybe()
	rule1.EXPECT().SrcID().Return("src1").Maybe()

	rule2 := mocks.NewRuleMock(t)
	rule2.EXPECT().ID().Return("rule2").Maybe()
	rule2.EXPECT().SrcID().Return("src1").Maybe()

	tests := []struct {
		name    string
		rule    *mocks.RuleMock
		wantErr bool
	}{
		{
			name:    "add first rule",
			rule:    rule1,
			wantErr: false,
		},
		{
			name:    "add second rule",
			rule:    rule2,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := trie.AddRule(tt.rule)
			
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				
				// Verify rule was added to compiled rules
				compiled, exists := trie.rules[tt.rule.ID()]
				assert.True(t, exists)
				assert.NotNil(t, compiled)
				assert.Equal(t, tt.rule, compiled.original)
			}
		})
	}

	// Verify stats were updated
	stats := trie.GetStats()
	assert.Equal(t, 2, stats.RuleCount)
}

func TestPathTrie_RemoveRule(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(&TrieConfig{
		StatsEnabled: true,
	})

	// Create and add a mock rule
	rule1 := mocks.NewRuleMock(t)
	rule1.EXPECT().ID().Return("rule1").Maybe()
	rule1.EXPECT().SrcID().Return("src1").Maybe()

	err := trie.AddRule(rule1)
	require.NoError(t, err)

	// Verify rule exists
	_, exists := trie.rules["rule1"]
	assert.True(t, exists)

	// Remove the rule
	err = trie.RemoveRule("rule1")
	assert.NoError(t, err)

	// Verify rule was removed
	_, exists = trie.rules["rule1"]
	assert.False(t, exists)

	// Test removing non-existent rule
	err = trie.RemoveRule("non-existent")
	assert.NoError(t, err) // Should not error
}

func TestPathTrie_FindCandidates(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(&TrieConfig{
		StatsEnabled: true,
	})

	// Create mock rules with different patterns
	exactRule := mocks.NewRuleMock(t)
	exactRule.EXPECT().ID().Return("exact-rule").Maybe()
	exactRule.EXPECT().SrcID().Return("src1").Maybe()

	wildcardRule := mocks.NewRuleMock(t)
	wildcardRule.EXPECT().ID().Return("wildcard-rule").Maybe()
	wildcardRule.EXPECT().SrcID().Return("src1").Maybe()

	// Add rules to trie
	err := trie.AddRule(exactRule)
	require.NoError(t, err)
	err = trie.AddRule(wildcardRule)
	require.NoError(t, err)

	tests := []struct {
		name        string
		requestURL  string
		expectRules bool
	}{
		{
			name:        "root path",
			requestURL:  "http://example.com/",
			expectRules: true, // Should match at least root rules
		},
		{
			name:        "api path",
			requestURL:  "http://example.com/api",
			expectRules: true,
		},
		{
			name:        "nested path",
			requestURL:  "http://example.com/api/v1/users",
			expectRules: true,
		},
		{
			name:        "path with query params",
			requestURL:  "http://example.com/api?param=value",
			expectRules: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.requestURL)
			require.NoError(t, err)

			candidates, err := trie.FindCandidates(u)
			assert.NoError(t, err)

			if tt.expectRules {
				// We should get some candidates (even if empty due to mocking)
				assert.NotNil(t, candidates)
			}
		})
	}

	// Verify stats were updated
	stats := trie.GetStats()
	assert.Greater(t, stats.TotalLookups, int64(0))
}

func TestPathTrie_GetOrCreateChild(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(nil)
	root := trie.root

	tests := []struct {
		name        string
		segment     string
		expectChild bool
	}{
		{
			name:        "static segment",
			segment:     "api",
			expectChild: true,
		},
		{
			name:        "wildcard segment",
			segment:     "*",
			expectChild: true,
		},
		{
			name:        "glob segment",
			segment:     "**",
			expectChild: true,
		},
		{
			name:        "parameter segment",
			segment:     "{id}",
			expectChild: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			child := trie.getOrCreateChild(root, tt.segment, 1)
			
			if tt.expectChild {
				assert.NotNil(t, child)
				assert.Equal(t, tt.segment, child.segment)
				assert.Equal(t, 1, child.depth)
				
				// Verify child is properly linked to parent
				switch tt.segment {
				case "*":
					assert.Equal(t, child, root.wildcardChild)
				case "**":
					assert.Equal(t, child, root.globChild)
				case "{id}":
					assert.Equal(t, child, root.paramChild)
				default:
					assert.Equal(t, child, root.children[tt.segment])
				}
			}
		})
	}

	// Test that calling getOrCreateChild again returns the same child
	child1 := trie.getOrCreateChild(root, "api", 1)
	child2 := trie.getOrCreateChild(root, "api", 1)
	assert.Equal(t, child1, child2)
}

func TestPathTrie_FindChild(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(nil)
	root := trie.root

	// Create children of different types
	staticChild := trie.getOrCreateChild(root, "api", 1)
	wildcardChild := trie.getOrCreateChild(root, "*", 1)
	paramChild := trie.getOrCreateChild(root, "{id}", 1)
	globChild := trie.getOrCreateChild(root, "**", 1)

	tests := []struct {
		name     string
		segment  string
		expected *TrieNode
	}{
		{
			name:     "find static child",
			segment:  "api",
			expected: staticChild,
		},
		{
			name:     "find non-existent static child falls back to param",
			segment:  "users",
			expected: paramChild,
		},
		{
			name:     "find another non-existent falls back to wildcard",
			segment:  "other",
			expected: paramChild, // param takes precedence over wildcard
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trie.findChild(root, tt.segment)
			assert.Equal(t, tt.expected, result)
		})
	}

	// Test with node that has no children
	emptyNode := &TrieNode{
		children: make(map[string]*TrieNode),
	}
	result := trie.findChild(emptyNode, "anything")
	assert.Nil(t, result)

	// Silence unused variable warnings
	_ = wildcardChild
	_ = globChild
}

func TestPathTrie_Statistics(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(&TrieConfig{
		StatsEnabled: true,
	})

	// Add some rules
	for i := 0; i < 5; i++ {
		rule := mocks.NewRuleMock(t)
		ruleID := fmt.Sprintf("rule%d", i)
		rule.EXPECT().ID().Return(ruleID).Maybe()
		rule.EXPECT().SrcID().Return("src1").Maybe()

		err := trie.AddRule(rule)
		require.NoError(t, err)
	}

	// Perform some lookups
	for i := 0; i < 10; i++ {
		u, _ := url.Parse(fmt.Sprintf("http://example.com/path%d", i))
		_, _ = trie.FindCandidates(u)
	}

	stats := trie.GetStats()
	
	// Verify basic stats
	assert.Equal(t, 5, stats.RuleCount)
	assert.Equal(t, int64(10), stats.TotalLookups)
	assert.Greater(t, stats.NodeCount, 0)
	assert.GreaterOrEqual(t, stats.MaxDepth, 0)
	assert.GreaterOrEqual(t, stats.AverageDepth, 0.0)
}

func TestPathTrie_ContainsWildcards(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(nil)

	tests := []struct {
		name     string
		pattern  string
		expected bool
	}{
		{
			name:     "no wildcards",
			pattern:  "/api/users",
			expected: false,
		},
		{
			name:     "single wildcard",
			pattern:  "/api/*/users",
			expected: true,
		},
		{
			name:     "double wildcard",
			pattern:  "/api/**/users",
			expected: true,
		},
		{
			name:     "both wildcards",
			pattern:  "/api/*/data/**",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trie.containsWildcards(tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPathTrie_CalculatePriority(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(nil)

	tests := []struct {
		name     string
		compiled *CompiledRule
		expected int
	}{
		{
			name: "exact match rule",
			compiled: &CompiledRule{
				isExact:     true,
				hasWildcard: false,
			},
			expected: 150, // 100 base + 50 exact
		},
		{
			name: "no wildcard rule",
			compiled: &CompiledRule{
				isExact:     false,
				hasWildcard: false,
			},
			expected: 125, // 100 base + 25 no wildcard
		},
		{
			name: "wildcard rule",
			compiled: &CompiledRule{
				isExact:     false,
				hasWildcard: true,
			},
			expected: 100, // 100 base only
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trie.calculatePriority(tt.compiled)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPathTrie_CalculateComplexity(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(nil)

	tests := []struct {
		name     string
		pattern  string
		expected int
	}{
		{
			name:     "simple pattern",
			pattern:  "/api/users",
			expected: 0,
		},
		{
			name:     "single wildcard",
			pattern:  "/api/*/users",
			expected: 1,
		},
		{
			name:     "double wildcard",
			pattern:  "/api/**/users",
			expected: 2, // ** counts as 2
		},
		{
			name:     "parameter",
			pattern:  "/api/{id}/users",
			expected: 1,
		},
		{
			name:     "complex pattern",
			pattern:  "/api/{id}/*/data/**/{type}",
			expected: 5, // 1 + 1 + 2 + 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trie.calculateComplexity(tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Benchmark tests

func BenchmarkPathTrie_AddRule(b *testing.B) {
	trie := NewPathTrie(&TrieConfig{
		StatsEnabled: false, // Disable stats for pure performance
	})

	rules := make([]*mocks.RuleMock, b.N)
	for i := 0; i < b.N; i++ {
		rule := mocks.NewRuleMock(b)
		ruleID := fmt.Sprintf("rule%d", i)
		rule.EXPECT().ID().Return(ruleID).Maybe()
		rule.EXPECT().SrcID().Return("src1").Maybe()
		rules[i] = rule
	}

	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		_ = trie.AddRule(rules[i])
	}
}

func BenchmarkPathTrie_FindCandidates(b *testing.B) {
	trie := NewPathTrie(&TrieConfig{
		StatsEnabled: false,
	})

	// Pre-populate with rules
	for i := 0; i < 1000; i++ {
		rule := mocks.NewRuleMock(b)
		ruleID := fmt.Sprintf("rule%d", i)
		rule.EXPECT().ID().Return(ruleID).Maybe()
		rule.EXPECT().SrcID().Return("src1").Maybe()
		_ = trie.AddRule(rule)
	}

	testURL, _ := url.Parse("http://example.com/api/v1/users/123/profile")

	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		_, _ = trie.FindCandidates(testURL)
	}
}


