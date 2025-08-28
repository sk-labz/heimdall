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
	"net/url"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dadrus/heimdall/internal/rules/event"
	"github.com/dadrus/heimdall/internal/rules/rule/mocks"
)

// TestOptimizationIntegration tests the basic integration of optimization components.
func TestOptimizationIntegration(t *testing.T) {
	t.Parallel()

	// Create optimized repository
	queue := make(event.RuleSetChangedEventQueue, 10)
	factory := mocks.NewFactoryMock(t)
	factory.EXPECT().HasDefaultRule().Return(false).Maybe()

	config := &OptimizationConfig{
		Enabled:      true,
		TrieEnabled:  true,
		CacheEnabled: true,
		StatsEnabled: true,
	}

	logger := zerolog.Nop()
	repo := NewOptimizedRepository(queue, factory, logger, config)

	// Create test rules
	rule1 := mocks.NewRuleMock(t)
	rule1.EXPECT().ID().Return("api-rule").Maybe()
	rule1.EXPECT().SrcID().Return("src1").Maybe()
	rule1.EXPECT().MatchesURL(mock.Anything).Return(true).Maybe()

	rule2 := mocks.NewRuleMock(t)
	rule2.EXPECT().ID().Return("user-rule").Maybe()
	rule2.EXPECT().SrcID().Return("src1").Maybe()
	rule2.EXPECT().MatchesURL(mock.Anything).Return(false).Maybe()

	// Add rules to repository
	repo.addRuleToStructures(rule1)
	repo.addRuleToStructures(rule2)

	// Test rule lookup
	testURL, err := url.Parse("http://example.com/api/users")
	require.NoError(t, err)

	foundRule, err := repo.FindRule(testURL)
	require.NoError(t, err)
	assert.NotNil(t, foundRule)
	assert.Equal(t, "api-rule", foundRule.ID())

	// Verify statistics
	stats := repo.GetStats()
	assert.Equal(t, int64(1), stats.TotalLookups)
	assert.Equal(t, 2, stats.RuleCount)

	// Verify trie statistics
	trieStats := repo.GetTrieStats()
	assert.Equal(t, 2, trieStats.RuleCount)
	assert.Greater(t, trieStats.NodeCount, 0)

	// Verify cache statistics
	cacheStats := repo.GetCacheStats()
	assert.Greater(t, cacheStats.L1Misses, int64(0))
}

// TestCacheIntegration tests cache behavior.
func TestCacheIntegration(t *testing.T) {
	t.Parallel()

	cache := NewCacheManager(&CacheConfig{
		L1Size:       100,
		StatsEnabled: true,
	})

	// Test rule caching
	rule := mocks.NewRuleMock(t)
	rule.EXPECT().ID().Return("test-rule").Maybe()

	testURL := "http://example.com/test"

	// First lookup should miss
	_, found := cache.GetRule(testURL)
	assert.False(t, found)

	// Put rule in cache
	cache.PutRule(testURL, rule)

	// Second lookup should hit
	cachedRule, found := cache.GetRule(testURL)
	assert.True(t, found)
	assert.Equal(t, rule, cachedRule)

	// Verify statistics
	stats := cache.GetStats()
	assert.Equal(t, int64(1), stats.L1Hits)
	assert.Equal(t, int64(1), stats.L1Misses)
	assert.Equal(t, 0.5, stats.HitRatio)
}

// TestTrieIntegration tests trie behavior.
func TestTrieIntegration(t *testing.T) {
	t.Parallel()

	trie := NewPathTrie(&TrieConfig{
		StatsEnabled: true,
	})

	// Add test rule
	rule := mocks.NewRuleMock(t)
	rule.EXPECT().ID().Return("test-rule").Maybe()
	rule.EXPECT().SrcID().Return("src1").Maybe()

	err := trie.AddRule(rule)
	require.NoError(t, err)

	// Test candidate finding
	testURL, err := url.Parse("http://example.com/api/users")
	require.NoError(t, err)

	candidates, err := trie.FindCandidates(testURL)
	require.NoError(t, err)
	assert.NotNil(t, candidates)

	// Verify statistics
	stats := trie.GetStats()
	assert.Equal(t, 1, stats.RuleCount)
	assert.Equal(t, int64(1), stats.TotalLookups)
}


