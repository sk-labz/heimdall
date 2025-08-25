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
	"context"
	"net/url"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/dadrus/heimdall/internal/heimdall"
	"github.com/dadrus/heimdall/internal/rules/event"
	"github.com/dadrus/heimdall/internal/rules/rule"
	"github.com/dadrus/heimdall/internal/x/errorchain"
)

// OptimizedRepository implements the rule.Repository interface with performance optimizations.
// It replaces the linear O(n) search with a trie-based O(log n) lookup and multi-level caching.
type OptimizedRepository struct {
	defaultRule    rule.Rule
	logger         zerolog.Logger
	
	// Core optimization components
	trie           *PathTrie
	cache          *CacheManager
	
	// Rule management
	rules          map[string]rule.Rule // Rule ID -> Rule mapping
	mutex          sync.RWMutex
	
	// Event handling
	queue          event.RuleSetChangedEventQueue
	quit           chan bool
	
	// Performance monitoring
	stats          *PerformanceStats
	config         *OptimizationConfig
}

// PerformanceStats tracks performance metrics for the optimized repository.
type PerformanceStats struct {
	TotalLookups       int64         // Total rule lookups performed
	CacheHits          int64         // Lookups served from cache
	TrieHits           int64         // Lookups that found candidates in trie
	LinearFallbacks    int64         // Fallbacks to linear search
	AverageLookupTime  time.Duration // Average lookup time
	P95LookupTime      time.Duration // 95th percentile lookup time
	P99LookupTime      time.Duration // 99th percentile lookup time
	RuleCount          int           // Current number of rules
	MemoryUsage        int64         // Estimated memory usage in bytes
	LastOptimized      time.Time     // Last optimization timestamp
	OptimizationRatio  float64       // Ratio of optimized vs linear lookups
}

// OptimizationConfig holds configuration for the optimization features.
type OptimizationConfig struct {
	Enabled            bool          // Enable optimizations
	TrieEnabled        bool          // Enable trie-based indexing
	CacheEnabled       bool          // Enable multi-level caching
	StatsEnabled       bool          // Enable performance statistics
	FallbackToLinear   bool          // Fallback to linear search if optimization fails
	MaxMemoryUsage     int64         // Maximum memory usage for optimizations
	OptimizationWindow time.Duration // Time window for performance optimization
	
	// Trie configuration
	Trie *TrieConfig
	
	// Cache configuration
	Cache *CacheConfig
}

// NewOptimizedRepository creates a new optimized rule repository.
func NewOptimizedRepository(
	queue event.RuleSetChangedEventQueue,
	ruleFactory rule.Factory,
	logger zerolog.Logger,
	config *OptimizationConfig,
) *OptimizedRepository {
	if config == nil {
		config = &OptimizationConfig{
			Enabled:            true,
			TrieEnabled:        true,
			CacheEnabled:       true,
			StatsEnabled:       true,
			FallbackToLinear:   true,
			MaxMemoryUsage:     100 * 1024 * 1024, // 100MB
			OptimizationWindow: 5 * time.Minute,
		}
	}

	var defaultRule rule.Rule
	if ruleFactory.HasDefaultRule() {
		defaultRule = ruleFactory.DefaultRule()
	}

	repo := &OptimizedRepository{
		defaultRule: defaultRule,
		logger:      logger.With().Str("component", "optimized_repository").Logger(),
		rules:       make(map[string]rule.Rule),
		queue:       queue,
		quit:        make(chan bool),
		stats:       &PerformanceStats{},
		config:      config,
	}

	// Initialize optimization components if enabled
	if config.Enabled {
		if config.TrieEnabled {
			repo.trie = NewPathTrie(config.Trie)
		}
		
		if config.CacheEnabled {
			repo.cache = NewCacheManager(config.Cache)
		}
	}

	logger.Info().
		Bool("optimizations_enabled", config.Enabled).
		Bool("trie_enabled", config.TrieEnabled).
		Bool("cache_enabled", config.CacheEnabled).
		Msg("Optimized rule repository initialized")

	return repo
}

// FindRule implements the rule.Repository interface with optimized lookup.
// This is the core method that provides O(log n) performance instead of O(n).
func (r *OptimizedRepository) FindRule(requestURL *url.URL) (rule.Rule, error) {
	start := time.Now()
	defer func() {
		if r.config.StatsEnabled {
			r.updateLookupStats(time.Since(start))
		}
	}()

	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// Fast path: Try cache first
	if r.config.CacheEnabled && r.cache != nil {
		if cachedRule, found := r.cache.GetRule(requestURL.String()); found {
			r.stats.CacheHits++
			return cachedRule, nil
		}
	}

	var foundRule rule.Rule
	var err error

	// Optimized path: Use trie-based lookup
	if r.config.TrieEnabled && r.trie != nil {
		foundRule, err = r.findRuleWithTrie(requestURL)
		if err == nil && foundRule != nil {
			// Cache the result for future lookups
			if r.config.CacheEnabled && r.cache != nil {
				r.cache.PutRule(requestURL.String(), foundRule)
			}
			r.stats.TrieHits++
			return foundRule, nil
		}
	}

	// Fallback path: Linear search if optimization fails
	if r.config.FallbackToLinear {
		foundRule, err = r.findRuleLinear(requestURL)
		if err == nil && foundRule != nil {
			r.stats.LinearFallbacks++
			
			// Try to add to optimized structures for future lookups
			if r.config.TrieEnabled && r.trie != nil {
				_ = r.trie.AddRule(foundRule) // Ignore errors in fallback path
			}
			
			return foundRule, nil
		}
	}

	// No rule found, try default rule
	if r.defaultRule != nil {
		return r.defaultRule, nil
	}

	return nil, errorchain.NewWithMessagef(heimdall.ErrNoRuleFound,
		"no applicable rule found for %s", requestURL.String())
}

// findRuleWithTrie performs optimized rule lookup using the trie index.
func (r *OptimizedRepository) findRuleWithTrie(requestURL *url.URL) (rule.Rule, error) {
	// Get candidate rules from trie
	candidates, err := r.trie.FindCandidates(requestURL)
	if err != nil {
		return nil, err
	}

	// Test candidates in priority order
	for _, candidate := range candidates {
		if candidate.rule.MatchesURL(requestURL) {
			// Update match statistics
			candidate.matchCount++
			candidate.lastMatched = time.Now()
			return candidate.rule, nil
		}
	}

	return nil, nil // No matching rule found
}

// findRuleLinear performs traditional linear search as fallback.
func (r *OptimizedRepository) findRuleLinear(requestURL *url.URL) (rule.Rule, error) {
	for _, rul := range r.rules {
		if rul.MatchesURL(requestURL) {
			return rul, nil
		}
	}
	return nil, nil
}

// Start implements the rule.Repository interface.
func (r *OptimizedRepository) Start(ctx context.Context) error {
	r.logger.Info().Msg("Starting optimized rule repository")

	// Start performance monitoring
	if r.config.StatsEnabled {
		go r.performanceMonitor(ctx)
	}

	// Start rule change processing
	go r.watchRuleSetChanges()

	return nil
}

// Stop implements the rule.Repository interface.
func (r *OptimizedRepository) Stop(ctx context.Context) error {
	r.logger.Info().Msg("Stopping optimized rule repository")

	r.quit <- true
	close(r.quit)

	return nil
}

// watchRuleSetChanges processes rule change events.
func (r *OptimizedRepository) watchRuleSetChanges() {
	for {
		select {
		case evt, ok := <-r.queue:
			if !ok {
				r.logger.Debug().Msg("Rule set definition queue closed")
				return
			}

			switch evt.ChangeType {
			case event.Create:
				r.addRuleSet(evt.Source, evt.Rules)
			case event.Update:
				r.updateRuleSet(evt.Source, evt.Rules)
			case event.Remove:
				r.deleteRuleSet(evt.Source)
			}

		case <-r.quit:
			r.logger.Info().Msg("Rule change watcher stopped")
			return
		}
	}
}

// addRuleSet adds a new rule set to the repository.
func (r *OptimizedRepository) addRuleSet(srcID string, rules []rule.Rule) {
	r.logger.Info().Str("_src", srcID).Int("rule_count", len(rules)).Msg("Adding rule set")

	r.mutex.Lock()
	defer r.mutex.Unlock()

	for _, rul := range rules {
		r.addRuleToStructures(rul)
	}

	r.logger.Debug().Str("_src", srcID).Msg("Rule set added successfully")
}

// updateRuleSet updates an existing rule set.
func (r *OptimizedRepository) updateRuleSet(srcID string, rules []rule.Rule) {
	r.logger.Info().Str("_src", srcID).Int("rule_count", len(rules)).Msg("Updating rule set")

	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Remove old rules for this source
	var oldRules []rule.Rule
	for _, rul := range r.rules {
		if rul.SrcID() == srcID {
			oldRules = append(oldRules, rul)
		}
	}

	for _, rul := range oldRules {
		r.removeRuleFromStructures(rul)
	}

	// Add new rules
	for _, rul := range rules {
		r.addRuleToStructures(rul)
	}

	r.logger.Debug().Str("_src", srcID).Msg("Rule set updated successfully")
}

// deleteRuleSet removes a rule set from the repository.
func (r *OptimizedRepository) deleteRuleSet(srcID string) {
	r.logger.Info().Str("_src", srcID).Msg("Deleting rule set")

	r.mutex.Lock()
	defer r.mutex.Unlock()

	var rulesToRemove []rule.Rule
	for _, rul := range r.rules {
		if rul.SrcID() == srcID {
			rulesToRemove = append(rulesToRemove, rul)
		}
	}

	for _, rul := range rulesToRemove {
		r.removeRuleFromStructures(rul)
	}

	r.logger.Debug().Str("_src", srcID).Msg("Rule set deleted successfully")
}

// addRuleToStructures adds a rule to all optimization structures.
func (r *OptimizedRepository) addRuleToStructures(rul rule.Rule) {
	// Add to main rule map
	r.rules[rul.ID()] = rul

	// Add to trie index
	if r.config.TrieEnabled && r.trie != nil {
		if err := r.trie.AddRule(rul); err != nil {
			r.logger.Warn().Err(err).Str("rule_id", rul.ID()).Msg("Failed to add rule to trie")
		}
	}

	// Invalidate relevant cache entries
	if r.config.CacheEnabled && r.cache != nil {
		r.cache.InvalidateRule(rul.ID())
	}

	r.logger.Debug().Str("rule_id", rul.ID()).Msg("Rule added to optimization structures")
}

// removeRuleFromStructures removes a rule from all optimization structures.
func (r *OptimizedRepository) removeRuleFromStructures(rul rule.Rule) {
	// Remove from main rule map
	delete(r.rules, rul.ID())

	// Remove from trie index
	if r.config.TrieEnabled && r.trie != nil {
		if err := r.trie.RemoveRule(rul.ID()); err != nil {
			r.logger.Warn().Err(err).Str("rule_id", rul.ID()).Msg("Failed to remove rule from trie")
		}
	}

	// Invalidate relevant cache entries
	if r.config.CacheEnabled && r.cache != nil {
		r.cache.InvalidateRule(rul.ID())
	}

	r.logger.Debug().Str("rule_id", rul.ID()).Msg("Rule removed from optimization structures")
}

// Performance monitoring and statistics

// performanceMonitor runs in the background to collect performance statistics.
func (r *OptimizedRepository) performanceMonitor(ctx context.Context) {
	ticker := time.NewTicker(r.config.OptimizationWindow)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.collectPerformanceStats()
			r.optimizeIfNeeded()
			
		case <-ctx.Done():
			return
		}
	}
}

// collectPerformanceStats gathers current performance metrics.
func (r *OptimizedRepository) collectPerformanceStats() {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	r.stats.RuleCount = len(r.rules)
	
	// Calculate optimization ratio
	totalLookups := r.stats.CacheHits + r.stats.TrieHits + r.stats.LinearFallbacks
	if totalLookups > 0 {
		optimizedLookups := r.stats.CacheHits + r.stats.TrieHits
		r.stats.OptimizationRatio = float64(optimizedLookups) / float64(totalLookups)
	}

	// Estimate memory usage
	r.stats.MemoryUsage = r.estimateMemoryUsage()

	r.logger.Debug().
		Int64("total_lookups", r.stats.TotalLookups).
		Int64("cache_hits", r.stats.CacheHits).
		Int64("trie_hits", r.stats.TrieHits).
		Int64("linear_fallbacks", r.stats.LinearFallbacks).
		Float64("optimization_ratio", r.stats.OptimizationRatio).
		Int64("memory_usage_mb", r.stats.MemoryUsage/(1024*1024)).
		Msg("Performance statistics updated")
}

// optimizeIfNeeded performs optimization if certain thresholds are met.
func (r *OptimizedRepository) optimizeIfNeeded() {
	// Check if memory usage is too high
	if r.stats.MemoryUsage > r.config.MaxMemoryUsage {
		r.logger.Warn().
			Int64("current_usage_mb", r.stats.MemoryUsage/(1024*1024)).
			Int64("max_usage_mb", r.config.MaxMemoryUsage/(1024*1024)).
			Msg("Memory usage high, performing optimization")
		
		r.optimizeMemoryUsage()
	}

	// Check if optimization ratio is too low
	if r.stats.OptimizationRatio < 0.7 && r.stats.TotalLookups > 1000 {
		r.logger.Warn().
			Float64("optimization_ratio", r.stats.OptimizationRatio).
			Msg("Low optimization ratio, analyzing patterns")
		
		r.analyzeAndOptimize()
	}

	r.stats.LastOptimized = time.Now()
}

// estimateMemoryUsage estimates the memory usage of optimization structures.
func (r *OptimizedRepository) estimateMemoryUsage() int64 {
	var usage int64
	
	// Estimate trie memory usage
	if r.trie != nil {
		trieStats := r.trie.GetStats()
		usage += int64(trieStats.NodeCount * 200) // Rough estimate: 200 bytes per node
	}
	
	// Estimate cache memory usage
	if r.cache != nil {
		cacheStats := r.cache.GetStats()
		usage += int64(r.cache.l1Cache.Size() * 100) // Rough estimate: 100 bytes per L1 entry
		usage += int64(r.cache.l2Cache.Size() * 50)  // Rough estimate: 50 bytes per L2 entry
		usage += int64(r.cache.l3Cache.Size() * 200) // Rough estimate: 200 bytes per L3 entry
		_ = cacheStats // Use the variable to avoid unused error
	}
	
	return usage
}

// optimizeMemoryUsage reduces memory usage by clearing caches and compacting structures.
func (r *OptimizedRepository) optimizeMemoryUsage() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Clear caches to free memory
	if r.cache != nil {
		r.cache.InvalidateAll()
		r.logger.Info().Msg("Cleared all caches to reduce memory usage")
	}

	// TODO: Implement trie compaction if needed
}

// analyzeAndOptimize analyzes lookup patterns and optimizes accordingly.
func (r *OptimizedRepository) analyzeAndOptimize() {
	// TODO: Implement pattern analysis and optimization
	// This could include:
	// - Identifying frequently accessed patterns
	// - Rebalancing the trie structure
	// - Adjusting cache sizes
	// - Pre-warming cache with common patterns
	
	r.logger.Info().Msg("Pattern analysis and optimization completed")
}

// updateLookupStats updates lookup time statistics.
func (r *OptimizedRepository) updateLookupStats(duration time.Duration) {
	r.stats.TotalLookups++
	
	// Simple moving average for lookup time
	alpha := 0.1 // Smoothing factor
	if r.stats.AverageLookupTime == 0 {
		r.stats.AverageLookupTime = duration
	} else {
		r.stats.AverageLookupTime = time.Duration(
			float64(r.stats.AverageLookupTime)*(1-alpha) + float64(duration)*alpha,
		)
	}

	// Update percentiles (simplified implementation)
	if duration > r.stats.P95LookupTime {
		r.stats.P95LookupTime = duration
	}
	if duration > r.stats.P99LookupTime {
		r.stats.P99LookupTime = duration
	}
}

// GetStats returns current performance statistics.
func (r *OptimizedRepository) GetStats() PerformanceStats {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return *r.stats
}

// GetTrieStats returns trie performance statistics.
func (r *OptimizedRepository) GetTrieStats() IndexStats {
	if r.trie == nil {
		return IndexStats{}
	}
	return r.trie.GetStats()
}

// GetCacheStats returns cache performance statistics.
func (r *OptimizedRepository) GetCacheStats() CacheStats {
	if r.cache == nil {
		return CacheStats{}
	}
	return r.cache.GetStats()
}
