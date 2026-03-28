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

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/dadrus/heimdall/internal/rules/event"
	"github.com/dadrus/heimdall/internal/rules/rule/mocks"
)

// BenchmarkLinearSearch benchmarks the traditional linear search approach.
func BenchmarkLinearSearch(b *testing.B) {
	ruleCounts := []int{100, 500, 1000, 5000, 10000}

	for _, ruleCount := range ruleCounts {
		b.Run(fmt.Sprintf("rules_%d", ruleCount), func(b *testing.B) {
			// Create test rules
			rules := make([]MockRule, ruleCount)
			for i := 0; i < ruleCount; i++ {
				rules[i] = MockRule{
					id:      fmt.Sprintf("rule_%d", i),
					pattern: fmt.Sprintf("/api/v%d/resource", i%10), // Some variety in patterns
				}
			}

			testURL, _ := url.Parse("http://example.com/api/v5/resource/123")

			b.ResetTimer()
			
			for i := 0; i < b.N; i++ {
				// Simulate linear search
				for _, rule := range rules {
					if rule.MatchesURL(testURL) {
						break // Found matching rule
					}
				}
			}
		})
	}
}

// BenchmarkOptimizedSearch benchmarks the optimized trie-based search.
func BenchmarkOptimizedSearch(b *testing.B) {
	ruleCounts := []int{100, 500, 1000, 5000, 10000}

	for _, ruleCount := range ruleCounts {
		b.Run(fmt.Sprintf("rules_%d", ruleCount), func(b *testing.B) {
			// Create optimized repository
			queue := make(event.RuleSetChangedEventQueue, 10)
			factory := mocks.NewFactoryMock(b)
			factory.EXPECT().HasDefaultRule().Return(false).Maybe()
			
			config := &OptimizationConfig{
				Enabled:      true,
				TrieEnabled:  true,
				CacheEnabled: true,
				StatsEnabled: false, // Disable for pure performance testing
			}

			repo := NewOptimizedRepository(queue, factory, testLogger(), config)
			
			// Add test rules
			rules := make([]*mocks.RuleMock, ruleCount)
			for i := 0; i < ruleCount; i++ {
				rule := mocks.NewRuleMock(b)
				ruleID := fmt.Sprintf("rule_%d", i)
				srcID := fmt.Sprintf("src_%d", i%10)
				
				rule.EXPECT().ID().Return(ruleID).Maybe()
				rule.EXPECT().SrcID().Return(srcID).Maybe()
				rule.EXPECT().MatchesURL(mock.Anything).Return(i == ruleCount/2).Maybe() // Make middle rule match
				
				rules[i] = rule
				repo.addRuleToStructures(rule)
			}

			testURL, _ := url.Parse("http://example.com/api/v5/resource/123")

			b.ResetTimer()
			
			for i := 0; i < b.N; i++ {
				_, _ = repo.FindRule(testURL)
			}
		})
	}
}

// BenchmarkCachePerformance benchmarks cache hit performance.
func BenchmarkCachePerformance(b *testing.B) {
	config := &CacheConfig{
		L1Size:       10000,
		L2Size:       5000,
		L3Size:       1000,
		StatsEnabled: false,
	}

	cache := NewCacheManager(config)

	// Pre-populate cache
	for i := 0; i < 1000; i++ {
		rule := mocks.NewRuleMock(b)
		ruleID := fmt.Sprintf("rule_%d", i)
		rule.EXPECT().ID().Return(ruleID).Maybe()
		
		url := fmt.Sprintf("http://example.com/path_%d", i)
		cache.PutRule(url, rule)
	}

	testURL := "http://example.com/path_500" // Should be in cache

	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		_, _ = cache.GetRule(testURL)
	}
}

// BenchmarkTrieLookup benchmarks pure trie lookup performance.
func BenchmarkTrieLookup(b *testing.B) {
	nodeDepths := []int{5, 10, 15, 20}

	for _, depth := range nodeDepths {
		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			trie := NewPathTrie(&TrieConfig{
				MaxDepth:     depth,
				StatsEnabled: false,
			})

			// Create rules with varying depths
			for i := 0; i < 1000; i++ {
				rule := mocks.NewRuleMock(b)
				ruleID := fmt.Sprintf("rule_%d", i)
				rule.EXPECT().ID().Return(ruleID).Maybe()
				rule.EXPECT().SrcID().Return("src1").Maybe()
				
				_ = trie.AddRule(rule)
			}

			testURL, _ := url.Parse("http://example.com/api/v1/users/123/profile/settings")

			b.ResetTimer()
			
			for i := 0; i < b.N; i++ {
				_, _ = trie.FindCandidates(testURL)
			}
		})
	}
}

// BenchmarkConcurrentLookups benchmarks concurrent rule lookups.
func BenchmarkConcurrentLookups(b *testing.B) {
	concurrencyLevels := []int{1, 10, 100, 1000}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrent_%d", concurrency), func(b *testing.B) {
			// Create optimized repository
			queue := make(event.RuleSetChangedEventQueue, 10)
			factory := mocks.NewFactoryMock(b)
			factory.EXPECT().HasDefaultRule().Return(false).Maybe()
			
			config := &OptimizationConfig{
				Enabled:      true,
				TrieEnabled:  true,
				CacheEnabled: true,
				StatsEnabled: false,
			}

			repo := NewOptimizedRepository(queue, factory, testLogger(), config)
			
			// Add test rules
			for i := 0; i < 1000; i++ {
				rule := mocks.NewRuleMock(b)
				ruleID := fmt.Sprintf("rule_%d", i)
				rule.EXPECT().ID().Return(ruleID).Maybe()
				rule.EXPECT().SrcID().Return("src1").Maybe()
				
				repo.addRuleToStructures(rule)
			}

			testURLs := make([]*url.URL, 100)
			for i := 0; i < 100; i++ {
				testURLs[i], _ = url.Parse(fmt.Sprintf("http://example.com/api/v1/resource_%d", i))
			}

			b.ResetTimer()
			
			b.RunParallel(func(pb *testing.PB) {
				urlIndex := 0
				for pb.Next() {
					testURL := testURLs[urlIndex%len(testURLs)]
					_, _ = repo.FindRule(testURL)
					urlIndex++
				}
			})
		})
	}
}

// BenchmarkMemoryUsage benchmarks memory usage patterns.
func BenchmarkMemoryUsage(b *testing.B) {
	b.Run("trie_memory_growth", func(b *testing.B) {
		trie := NewPathTrie(&TrieConfig{
			StatsEnabled: true,
		})

		b.ResetTimer()
		
		for i := 0; i < b.N; i++ {
			rule := mocks.NewRuleMock(b)
			ruleID := fmt.Sprintf("rule_%d", i)
			rule.EXPECT().ID().Return(ruleID).Maybe()
			rule.EXPECT().SrcID().Return("src1").Maybe()
			
			_ = trie.AddRule(rule)
		}

		stats := trie.GetStats()
		b.ReportMetric(float64(stats.NodeCount), "nodes")
		b.ReportMetric(float64(stats.RuleCount), "rules")
	})

	b.Run("cache_memory_growth", func(b *testing.B) {
		cache := NewCacheManager(&CacheConfig{
			L1Size:       b.N,
			StatsEnabled: true,
		})

		b.ResetTimer()
		
		for i := 0; i < b.N; i++ {
			rule := mocks.NewRuleMock(b)
			ruleID := fmt.Sprintf("rule_%d", i)
			rule.EXPECT().ID().Return(ruleID).Maybe()
			
			url := fmt.Sprintf("http://example.com/path_%d", i)
			cache.PutRule(url, rule)
		}

		_ = cache.GetStats()
		b.ReportMetric(float64(cache.l1Cache.Size()), "l1_entries")
	})
}

// BenchmarkPatternComplexity benchmarks different pattern complexities.
func BenchmarkPatternComplexity(b *testing.B) {
	patterns := map[string]string{
		"exact":      "/api/users/123",
		"wildcard":   "/api/*/123",
		"glob":       "/api/**/123",
		"param":      "/api/{id}/profile",
		"complex":    "/api/{version}/users/*/data/**/{type}",
	}

	for name := range patterns {
		b.Run(name, func(b *testing.B) {
			trie := NewPathTrie(&TrieConfig{
				StatsEnabled: false,
			})

			// Add rule with specific pattern
			rule := mocks.NewRuleMock(b)
			rule.EXPECT().ID().Return("test_rule").Maybe()
			rule.EXPECT().SrcID().Return("src1").Maybe()
			
			_ = trie.AddRule(rule)

			testURL, _ := url.Parse("http://example.com/api/v1/users/123/data/json/export")

			b.ResetTimer()
			
			for i := 0; i < b.N; i++ {
				_, _ = trie.FindCandidates(testURL)
			}
		})
	}
}

// Performance comparison test
func BenchmarkPerformanceComparison(b *testing.B) {
	ruleCounts := []int{100, 1000, 10000}

	for _, ruleCount := range ruleCounts {
		b.Run(fmt.Sprintf("comparison_%d_rules", ruleCount), func(b *testing.B) {
			// Test linear approach
			b.Run("linear", func(b *testing.B) {
				rules := make([]MockRule, ruleCount)
				for i := 0; i < ruleCount; i++ {
					rules[i] = MockRule{
						id:      fmt.Sprintf("rule_%d", i),
						pattern: fmt.Sprintf("/api/v%d/resource", i%10),
					}
				}

				testURL, _ := url.Parse("http://example.com/api/v5/resource/123")

				b.ResetTimer()
				
				for i := 0; i < b.N; i++ {
					for _, rule := range rules {
						if rule.MatchesURL(testURL) {
							break
						}
					}
				}
			})

			// Test optimized approach
			b.Run("optimized", func(b *testing.B) {
				queue := make(event.RuleSetChangedEventQueue, 10)
				factory := mocks.NewFactoryMock(b)
				factory.EXPECT().HasDefaultRule().Return(false).Maybe()
				
				repo := NewOptimizedRepository(queue, factory, testLogger(), &OptimizationConfig{
					Enabled:      true,
					TrieEnabled:  true,
					CacheEnabled: true,
					StatsEnabled: false,
				})

				for i := 0; i < ruleCount; i++ {
					rule := mocks.NewRuleMock(b)
					ruleID := fmt.Sprintf("rule_%d", i)
					rule.EXPECT().ID().Return(ruleID).Maybe()
					rule.EXPECT().SrcID().Return("src1").Maybe()
					
					repo.addRuleToStructures(rule)
				}

				testURL, _ := url.Parse("http://example.com/api/v5/resource/123")

				b.ResetTimer()
				
				for i := 0; i < b.N; i++ {
					_, _ = repo.FindRule(testURL)
				}
			})
		})
	}
}

// Helper types and functions for benchmarks

type MockRule struct {
	id      string
	pattern string
}

func (m MockRule) MatchesURL(u *url.URL) bool {
	// Simple mock matching logic
	return u.Path == m.pattern || m.pattern == "/api/v5/resource"
}

func testLogger() zerolog.Logger {
	return zerolog.Nop() // No-op logger for testing
}

// Test to verify benchmark validity
func TestBenchmarkValidity(t *testing.T) {
	// Quick sanity check that our benchmark setup works
	rule := MockRule{id: "test", pattern: "/api/v5/resource"}
	testURL, _ := url.Parse("http://example.com/api/v5/resource")
	
	assert.True(t, rule.MatchesURL(testURL))
	
	nonMatchURL, _ := url.Parse("http://example.com/other/path")
	assert.False(t, rule.MatchesURL(nonMatchURL))
}
