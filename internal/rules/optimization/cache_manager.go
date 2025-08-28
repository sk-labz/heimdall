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
	"container/list"
	"sync"
	"time"

	"github.com/dadrus/heimdall/internal/rules/patternmatcher"
	"github.com/dadrus/heimdall/internal/rules/rule"
)

// CacheManager implements a multi-level caching system for rule lookups.
// It provides three cache levels with different optimization strategies:
// L1: Direct URL to Rule mapping for hot paths
// L2: Compiled pattern cache to avoid recompilation
// L3: HTTP method to rule subset mapping
type CacheManager struct {
	l1Cache     *LRUCache[string, rule.Rule]                    // URL -> Rule
	l2Cache     *LRUCache[string, patternmatcher.PatternMatcher] // Pattern -> Compiled Matcher
	l3Cache     *LRUCache[string, []rule.Rule]                  // Method -> Rules subset
	stats       *CacheStats
	config      *CacheConfig
	mutex       sync.RWMutex
	invalidator *CacheInvalidator
}

// CacheConfig holds configuration for the cache system.
type CacheConfig struct {
	L1Size          int           // Size of L1 cache (URL -> Rule)
	L2Size          int           // Size of L2 cache (Pattern cache)
	L3Size          int           // Size of L3 cache (Method cache)
	TTL             time.Duration // Time-to-live for cache entries
	StatsEnabled    bool          // Enable cache statistics
	WarmupEnabled   bool          // Enable cache warmup on startup
	EvictionPolicy  string        // Eviction policy: "lru", "lfu", "ttl"
}

// CacheStats tracks cache performance metrics.
type CacheStats struct {
	L1Hits        int64   // L1 cache hits
	L1Misses      int64   // L1 cache misses
	L2Hits        int64   // L2 cache hits
	L2Misses      int64   // L2 cache misses
	L3Hits        int64   // L3 cache hits
	L3Misses      int64   // L3 cache misses
	HitRatio      float64 // Overall hit ratio
	EvictionCount int64   // Total evictions across all levels
	WarmupTime    time.Duration // Time taken for cache warmup
}

// LRUCache implements a generic LRU cache with TTL support.
type LRUCache[K comparable, V any] struct {
	capacity int
	ttl      time.Duration
	items    map[K]*lruItem[V]
	list     *list.List
	mutex    sync.RWMutex
}

type lruItem[V any] struct {
	key       interface{}
	value     V
	element   *list.Element
	timestamp time.Time
}

// CacheInvalidator handles cache invalidation when rules change.
type CacheInvalidator struct {
	manager *CacheManager
	ruleMap map[string][]string // Rule ID -> Cache keys affected
	mutex   sync.RWMutex
}

// NewCacheManager creates a new multi-level cache manager.
func NewCacheManager(config *CacheConfig) *CacheManager {
	if config == nil {
		config = &CacheConfig{
			L1Size:         10000,
			L2Size:         5000,
			L3Size:         1000,
			TTL:            5 * time.Minute,
			StatsEnabled:   true,
			WarmupEnabled:  true,
			EvictionPolicy: "lru",
		}
	}

	cm := &CacheManager{
		l1Cache: NewLRUCache[string, rule.Rule](config.L1Size, config.TTL),
		l2Cache: NewLRUCache[string, patternmatcher.PatternMatcher](config.L2Size, config.TTL),
		l3Cache: NewLRUCache[string, []rule.Rule](config.L3Size, config.TTL),
		stats:   &CacheStats{},
		config:  config,
	}

	cm.invalidator = &CacheInvalidator{
		manager: cm,
		ruleMap: make(map[string][]string),
	}

	return cm
}

// NewLRUCache creates a new LRU cache with the specified capacity and TTL.
func NewLRUCache[K comparable, V any](capacity int, ttl time.Duration) *LRUCache[K, V] {
	return &LRUCache[K, V]{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[K]*lruItem[V]),
		list:     list.New(),
	}
}

// GetRule attempts to retrieve a rule from L1 cache.
func (cm *CacheManager) GetRule(url string) (rule.Rule, bool) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	if rule, found := cm.l1Cache.Get(url); found {
		if cm.config.StatsEnabled {
			cm.stats.L1Hits++
		}
		return rule, true
	}

	if cm.config.StatsEnabled {
		cm.stats.L1Misses++
	}
	return nil, false
}

// PutRule stores a rule in L1 cache.
func (cm *CacheManager) PutRule(url string, r rule.Rule) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	cm.l1Cache.Put(url, r)
	
	// Track for invalidation
	cm.invalidator.trackCacheKey(r.ID(), "l1:"+url)
}

// GetPattern attempts to retrieve a compiled pattern from L2 cache.
func (cm *CacheManager) GetPattern(pattern string) (patternmatcher.PatternMatcher, bool) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	if matcher, found := cm.l2Cache.Get(pattern); found {
		if cm.config.StatsEnabled {
			cm.stats.L2Hits++
		}
		return matcher, true
	}

	if cm.config.StatsEnabled {
		cm.stats.L2Misses++
	}
	return nil, false
}

// PutPattern stores a compiled pattern in L2 cache.
func (cm *CacheManager) PutPattern(pattern string, matcher patternmatcher.PatternMatcher) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	cm.l2Cache.Put(pattern, matcher)
}

// GetMethodRules attempts to retrieve rules for a specific HTTP method from L3 cache.
func (cm *CacheManager) GetMethodRules(method string) ([]rule.Rule, bool) {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	if rules, found := cm.l3Cache.Get(method); found {
		if cm.config.StatsEnabled {
			cm.stats.L3Hits++
		}
		return rules, true
	}

	if cm.config.StatsEnabled {
		cm.stats.L3Misses++
	}
	return nil, false
}

// PutMethodRules stores rules for a specific HTTP method in L3 cache.
func (cm *CacheManager) PutMethodRules(method string, rules []rule.Rule) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	cm.l3Cache.Put(method, rules)
}

// InvalidateRule removes all cache entries related to a specific rule.
func (cm *CacheManager) InvalidateRule(ruleID string) {
	cm.invalidator.invalidateRule(ruleID)
}

// InvalidateAll clears all cache levels.
func (cm *CacheManager) InvalidateAll() {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	cm.l1Cache.Clear()
	cm.l2Cache.Clear()
	cm.l3Cache.Clear()
	
	// Reset invalidation tracking
	cm.invalidator.ruleMap = make(map[string][]string)
}

// WarmupCache pre-populates cache with frequently used patterns.
func (cm *CacheManager) WarmupCache(rules []rule.Rule) {
	if !cm.config.WarmupEnabled {
		return
	}

	start := time.Now()
	
	// Pre-compile common patterns
	commonPatterns := []string{
		"/api/*",
		"/api/v1/*",
		"/health",
		"/metrics",
		"/admin/*",
	}

	for _, pattern := range commonPatterns {
		if matcher, err := patternmatcher.NewPatternMatcher("glob", pattern); err == nil {
			cm.PutPattern(pattern, matcher)
		}
	}

	// Group rules by HTTP method
	methodGroups := make(map[string][]rule.Rule)
	for _, r := range rules {
		// This would need to be implemented based on actual rule structure
		methods := []string{"GET", "POST", "PUT", "DELETE"} // Placeholder
		for _, method := range methods {
			methodGroups[method] = append(methodGroups[method], r)
		}
	}

	for method, methodRules := range methodGroups {
		cm.PutMethodRules(method, methodRules)
	}

	if cm.config.StatsEnabled {
		cm.stats.WarmupTime = time.Since(start)
	}
}

// GetStats returns current cache performance statistics.
func (cm *CacheManager) GetStats() CacheStats {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	// Calculate overall hit ratio
	totalHits := cm.stats.L1Hits + cm.stats.L2Hits + cm.stats.L3Hits
	totalRequests := totalHits + cm.stats.L1Misses + cm.stats.L2Misses + cm.stats.L3Misses
	
	if totalRequests > 0 {
		cm.stats.HitRatio = float64(totalHits) / float64(totalRequests)
	}

	return *cm.stats
}

// LRU Cache Implementation

// Get retrieves a value from the LRU cache.
func (c *LRUCache[K, V]) Get(key K) (V, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	var zero V
	
	item, exists := c.items[key]
	if !exists {
		return zero, false
	}

	// Check TTL
	if c.ttl > 0 && time.Since(item.timestamp) > c.ttl {
		// Item expired, remove it
		c.removeItem(key, item)
		return zero, false
	}

	// Move to front (most recently used)
	c.list.MoveToFront(item.element)
	
	return item.value, true
}

// Put stores a value in the LRU cache.
func (c *LRUCache[K, V]) Put(key K, value V) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Check if key already exists
	if item, exists := c.items[key]; exists {
		// Update existing item
		item.value = value
		item.timestamp = time.Now()
		c.list.MoveToFront(item.element)
		return
	}

	// Create new item
	item := &lruItem[V]{
		key:       key,
		value:     value,
		timestamp: time.Now(),
	}
	
	item.element = c.list.PushFront(item)
	c.items[key] = item

	// Check if we need to evict
	if len(c.items) > c.capacity {
		c.evictLRU()
	}
}

// Clear removes all items from the cache.
func (c *LRUCache[K, V]) Clear() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.items = make(map[K]*lruItem[V])
	c.list.Init()
}

// Size returns the current number of items in the cache.
func (c *LRUCache[K, V]) Size() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.items)
}

// evictLRU removes the least recently used item.
func (c *LRUCache[K, V]) evictLRU() {
	if c.list.Len() == 0 {
		return
	}

	// Get the least recently used item (back of the list)
	element := c.list.Back()
	if element != nil {
		item := element.Value.(*lruItem[V])
		c.removeItem(item.key.(K), item)
	}
}

// removeItem removes a specific item from the cache.
func (c *LRUCache[K, V]) removeItem(key K, item *lruItem[V]) {
	delete(c.items, key)
	c.list.Remove(item.element)
}

// Cache Invalidation Implementation

// trackCacheKey tracks which cache keys are affected by a rule.
func (ci *CacheInvalidator) trackCacheKey(ruleID, cacheKey string) {
	ci.mutex.Lock()
	defer ci.mutex.Unlock()

	if ci.ruleMap[ruleID] == nil {
		ci.ruleMap[ruleID] = make([]string, 0)
	}
	ci.ruleMap[ruleID] = append(ci.ruleMap[ruleID], cacheKey)
}

// invalidateRule removes all cache entries for a specific rule.
func (ci *CacheInvalidator) invalidateRule(ruleID string) {
	ci.mutex.Lock()
	defer ci.mutex.Unlock()

	cacheKeys, exists := ci.ruleMap[ruleID]
	if !exists {
		return
	}

	// Invalidate each tracked cache key
	for _, cacheKey := range cacheKeys {
		ci.invalidateCacheKey(cacheKey)
	}

	// Remove tracking for this rule
	delete(ci.ruleMap, ruleID)
}

// invalidateCacheKey removes a specific cache key from the appropriate cache level.
func (ci *CacheInvalidator) invalidateCacheKey(cacheKey string) {
	switch {
	case len(cacheKey) > 3 && cacheKey[:3] == "l1:":
		key := cacheKey[3:]
		ci.manager.l1Cache.mutex.Lock()
		if item, exists := ci.manager.l1Cache.items[key]; exists {
			ci.manager.l1Cache.removeItem(key, item)
		}
		ci.manager.l1Cache.mutex.Unlock()
		
	case len(cacheKey) > 3 && cacheKey[:3] == "l2:":
		key := cacheKey[3:]
		ci.manager.l2Cache.mutex.Lock()
		if item, exists := ci.manager.l2Cache.items[key]; exists {
			ci.manager.l2Cache.removeItem(key, item)
		}
		ci.manager.l2Cache.mutex.Unlock()
		
	case len(cacheKey) > 3 && cacheKey[:3] == "l3:":
		key := cacheKey[3:]
		ci.manager.l3Cache.mutex.Lock()
		if item, exists := ci.manager.l3Cache.items[key]; exists {
			ci.manager.l3Cache.removeItem(key, item)
		}
		ci.manager.l3Cache.mutex.Unlock()
	}

	if ci.manager.config.StatsEnabled {
		ci.manager.stats.EvictionCount++
	}
}
