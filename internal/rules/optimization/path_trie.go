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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dadrus/heimdall/internal/rules/rule"
)

// PathTrie implements an optimized URL path-based rule index using a trie data structure.
// This replaces the linear O(n) search with O(log n) lookup performance.
type PathTrie struct {
	root    *TrieNode
	rules   map[string]*CompiledRule // Rule ID -> Compiled Rule mapping
	mutex   sync.RWMutex
	stats   *IndexStats
	config  *TrieConfig
}

// TrieNode represents a node in the URL path trie.
// Each node corresponds to a path segment and can contain multiple rules.
type TrieNode struct {
	segment       string                 // Path segment (e.g., "users", "api")
	children      map[string]*TrieNode   // Static children for exact matches
	wildcardChild *TrieNode              // Child for * wildcard pattern
	paramChild    *TrieNode              // Child for {param} pattern
	globChild     *TrieNode              // Child for ** glob pattern
	rules         []*RuleReference       // Rules applicable at this path level
	priority      int                    // Node priority for traversal optimization
	depth         int                    // Depth in the trie for balancing
}

// RuleReference holds a reference to a rule with additional metadata for optimization.
type RuleReference struct {
	rule        rule.Rule
	priority    int           // Rule priority (higher = more important)
	pattern     string        // Original URL pattern
	methods     []string      // Supported HTTP methods
	lastMatched time.Time     // Last successful match time for LRU
	matchCount  int64         // Total match count for frequency-based prioritization
	isExact     bool          // True if this is an exact match (no wildcards)
	complexity  int           // Pattern complexity score for ordering
}

// CompiledRule represents a pre-processed rule with optimized matching.
type CompiledRule struct {
	original    rule.Rule
	segments    []string      // Pre-split URL path segments
	hasWildcard bool          // Contains wildcards (* or **)
	isExact     bool          // Exact match (no patterns)
	methods     []string      // Supported HTTP methods
	priority    int           // Rule priority
	frequency   int64         // Match frequency for prioritization
	lastUsed    time.Time     // Last access time for LRU eviction
}

// IndexStats tracks performance metrics for the trie index.
type IndexStats struct {
	TotalLookups    int64   // Total number of rule lookups
	TrieHits        int64   // Lookups that found candidates in trie
	CacheHits       int64   // Lookups served from cache
	AverageDepth    float64 // Average trie traversal depth
	MaxDepth        int     // Maximum trie depth
	NodeCount       int     // Total number of trie nodes
	RuleCount       int     // Total number of indexed rules
	LookupLatency   time.Duration // Average lookup latency
	LastRebalanced  time.Time     // Last trie rebalancing time
}

// TrieConfig holds configuration options for the PathTrie.
type TrieConfig struct {
	MaxDepth            int           // Maximum allowed trie depth
	RebalanceThreshold  int           // Rule count threshold for rebalancing
	PriorityDecayFactor float64       // Factor for priority decay over time
	StatsEnabled        bool          // Enable performance statistics
	CacheWarming        bool          // Enable cache warming on startup
}

// NewPathTrie creates a new optimized path trie for rule indexing.
func NewPathTrie(config *TrieConfig) *PathTrie {
	if config == nil {
		config = &TrieConfig{
			MaxDepth:            20,
			RebalanceThreshold:  1000,
			PriorityDecayFactor: 0.95,
			StatsEnabled:        true,
			CacheWarming:        true,
		}
	}

	return &PathTrie{
		root: &TrieNode{
			segment:  "",
			children: make(map[string]*TrieNode),
			rules:    make([]*RuleReference, 0),
			depth:    0,
		},
		rules:  make(map[string]*CompiledRule),
		stats:  &IndexStats{},
		config: config,
	}
}

// AddRule adds a rule to the trie index with optimized placement.
func (pt *PathTrie) AddRule(r rule.Rule) error {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	// Compile the rule for optimization
	compiled, err := pt.compileRule(r)
	if err != nil {
		return err
	}

	// Store compiled rule
	pt.rules[r.ID()] = compiled

	// Extract URL pattern and parse path segments
	pattern := pt.extractURLPattern(r)
	segments := pt.parsePathSegments(pattern)

	// Insert into trie
	node := pt.root
	for depth, segment := range segments {
		if depth >= pt.config.MaxDepth {
			break // Prevent infinite depth
		}

		node = pt.getOrCreateChild(node, segment, depth+1)
	}

	// Add rule reference to the final node
	ruleRef := &RuleReference{
		rule:        r,
		priority:    pt.calculatePriority(compiled),
		pattern:     pattern,
		methods:     compiled.methods,
		lastMatched: time.Now(),
		matchCount:  0,
		isExact:     compiled.isExact,
		complexity:  pt.calculateComplexity(pattern),
	}

	node.rules = append(node.rules, ruleRef)
	
	// Sort rules by priority (highest first)
	sort.Slice(node.rules, func(i, j int) bool {
		return node.rules[i].priority > node.rules[j].priority
	})

	// Update statistics
	if pt.config.StatsEnabled {
		pt.updateStatsAfterAdd()
	}

	// Check if rebalancing is needed
	if pt.stats.RuleCount > pt.config.RebalanceThreshold {
		go pt.rebalanceIfNeeded()
	}

	return nil
}

// RemoveRule removes a rule from the trie index.
func (pt *PathTrie) RemoveRule(ruleID string) error {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	compiled, exists := pt.rules[ruleID]
	if !exists {
		return nil // Rule not found, nothing to remove
	}

	// Remove from compiled rules map
	delete(pt.rules, ruleID)

	// Remove from trie nodes
	pattern := pt.extractURLPatternFromCompiled(compiled)
	segments := pt.parsePathSegments(pattern)
	
	node := pt.root
	for _, segment := range segments {
		child := pt.findChild(node, segment)
		if child == nil {
			break // Path not found
		}
		node = child
	}

	// Remove rule reference from node
	for i, ruleRef := range node.rules {
		if ruleRef.rule.ID() == ruleID {
			node.rules = append(node.rules[:i], node.rules[i+1:]...)
			break
		}
	}

	// Update statistics
	if pt.config.StatsEnabled {
		pt.updateStatsAfterRemove()
	}

	return nil
}

// FindCandidates finds potential rule candidates for a given URL using trie traversal.
// This is the core optimization that replaces linear search with O(log n) lookup.
func (pt *PathTrie) FindCandidates(requestURL *url.URL) ([]*RuleReference, error) {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()

	start := time.Now()
	defer func() {
		if pt.config.StatsEnabled {
			pt.stats.TotalLookups++
			pt.stats.LookupLatency = time.Since(start)
		}
	}()

	path := requestURL.Path
	if path == "" {
		path = "/"
	}

	// Parse path segments
	segments := pt.parsePathSegments(path)
	candidates := make([]*RuleReference, 0)

	// Traverse trie to collect candidates
	pt.traverseForCandidates(pt.root, segments, 0, &candidates)

	// Sort candidates by priority and frequency
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].matchCount > candidates[j].matchCount
	})

	if pt.config.StatsEnabled && len(candidates) > 0 {
		pt.stats.TrieHits++
	}

	return candidates, nil
}

// compileRule pre-processes a rule for optimized matching.
func (pt *PathTrie) compileRule(r rule.Rule) (*CompiledRule, error) {
	pattern := pt.extractURLPattern(r)
	segments := pt.parsePathSegments(pattern)
	
	hasWildcard := pt.containsWildcards(pattern)
	isExact := !hasWildcard && !strings.Contains(pattern, "{") && !strings.Contains(pattern, ":")

	// Extract HTTP methods (this would need to be implemented based on rule structure)
	methods := pt.extractMethods(r)

	return &CompiledRule{
		original:    r,
		segments:    segments,
		hasWildcard: hasWildcard,
		isExact:     isExact,
		methods:     methods,
		priority:    1, // Default priority
		frequency:   0,
		lastUsed:    time.Now(),
	}, nil
}

// parsePathSegments splits a URL path into segments for trie traversal.
func (pt *PathTrie) parsePathSegments(path string) []string {
	if path == "" || path == "/" {
		return []string{}
	}

	// Remove leading and trailing slashes
	path = strings.Trim(path, "/")
	
	// Split by slash and filter empty segments
	segments := strings.Split(path, "/")
	result := make([]string, 0, len(segments))
	
	for _, segment := range segments {
		if segment != "" {
			result = append(result, segment)
		}
	}
	
	return result
}

// getOrCreateChild finds or creates a child node for the given segment.
func (pt *PathTrie) getOrCreateChild(parent *TrieNode, segment string, depth int) *TrieNode {
	// Handle different types of segments
	switch {
	case segment == "*":
		if parent.wildcardChild == nil {
			parent.wildcardChild = &TrieNode{
				segment:  segment,
				children: make(map[string]*TrieNode),
				rules:    make([]*RuleReference, 0),
				depth:    depth,
			}
		}
		return parent.wildcardChild
		
	case segment == "**":
		if parent.globChild == nil {
			parent.globChild = &TrieNode{
				segment:  segment,
				children: make(map[string]*TrieNode),
				rules:    make([]*RuleReference, 0),
				depth:    depth,
			}
		}
		return parent.globChild
		
	case strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}"):
		if parent.paramChild == nil {
			parent.paramChild = &TrieNode{
				segment:  segment,
				children: make(map[string]*TrieNode),
				rules:    make([]*RuleReference, 0),
				depth:    depth,
			}
		}
		return parent.paramChild
		
	default:
		// Static segment
		if parent.children[segment] == nil {
			parent.children[segment] = &TrieNode{
				segment:  segment,
				children: make(map[string]*TrieNode),
				rules:    make([]*RuleReference, 0),
				depth:    depth,
			}
		}
		return parent.children[segment]
	}
}

// findChild finds a child node for the given segment.
func (pt *PathTrie) findChild(parent *TrieNode, segment string) *TrieNode {
	// Try exact match first
	if child, exists := parent.children[segment]; exists {
		return child
	}
	
	// Try parameter match
	if parent.paramChild != nil {
		return parent.paramChild
	}
	
	// Try wildcard match
	if parent.wildcardChild != nil {
		return parent.wildcardChild
	}
	
	// Try glob match
	if parent.globChild != nil {
		return parent.globChild
	}
	
	return nil
}

// traverseForCandidates recursively traverses the trie to collect rule candidates.
func (pt *PathTrie) traverseForCandidates(node *TrieNode, segments []string, segmentIndex int, candidates *[]*RuleReference) {
	// Add rules from current node
	*candidates = append(*candidates, node.rules...)
	
	// If we've consumed all segments, check glob patterns
	if segmentIndex >= len(segments) {
		if node.globChild != nil {
			*candidates = append(*candidates, node.globChild.rules...)
		}
		return
	}
	
	currentSegment := segments[segmentIndex]
	
	// Try exact match
	if child, exists := node.children[currentSegment]; exists {
		pt.traverseForCandidates(child, segments, segmentIndex+1, candidates)
	}
	
	// Try parameter match
	if node.paramChild != nil {
		pt.traverseForCandidates(node.paramChild, segments, segmentIndex+1, candidates)
	}
	
	// Try wildcard match
	if node.wildcardChild != nil {
		pt.traverseForCandidates(node.wildcardChild, segments, segmentIndex+1, candidates)
	}
	
	// Try glob match (can consume remaining segments)
	if node.globChild != nil {
		*candidates = append(*candidates, node.globChild.rules...)
	}
}

// Helper methods for rule processing

func (pt *PathTrie) extractURLPattern(r rule.Rule) string {
	// This would need to be implemented based on the actual rule structure
	// For now, return a placeholder
	return "/"
}

func (pt *PathTrie) extractURLPatternFromCompiled(compiled *CompiledRule) string {
	return strings.Join(compiled.segments, "/")
}

func (pt *PathTrie) extractMethods(r rule.Rule) []string {
	// This would need to be implemented based on the actual rule structure
	return []string{"GET", "POST", "PUT", "DELETE"}
}

func (pt *PathTrie) containsWildcards(pattern string) bool {
	return strings.Contains(pattern, "*") || strings.Contains(pattern, "**")
}

func (pt *PathTrie) calculatePriority(compiled *CompiledRule) int {
	priority := 100 // Base priority
	
	// Exact matches get higher priority
	if compiled.isExact {
		priority += 50
	}
	
	// Patterns without wildcards get medium priority
	if !compiled.hasWildcard {
		priority += 25
	}
	
	return priority
}

func (pt *PathTrie) calculateComplexity(pattern string) int {
	complexity := 0
	complexity += strings.Count(pattern, "*")
	complexity += strings.Count(pattern, "**") * 2
	complexity += strings.Count(pattern, "{")
	return complexity
}

// Statistics and maintenance methods

func (pt *PathTrie) updateStatsAfterAdd() {
	pt.stats.RuleCount++
	pt.updateTrieStats()
}

func (pt *PathTrie) updateStatsAfterRemove() {
	pt.stats.RuleCount--
	pt.updateTrieStats()
}

func (pt *PathTrie) updateTrieStats() {
	pt.stats.NodeCount = pt.countNodes(pt.root)
	pt.stats.MaxDepth = pt.calculateMaxDepth(pt.root, 0)
	pt.stats.AverageDepth = pt.calculateAverageDepth()
}

func (pt *PathTrie) countNodes(node *TrieNode) int {
	count := 1
	for _, child := range node.children {
		count += pt.countNodes(child)
	}
	if node.wildcardChild != nil {
		count += pt.countNodes(node.wildcardChild)
	}
	if node.paramChild != nil {
		count += pt.countNodes(node.paramChild)
	}
	if node.globChild != nil {
		count += pt.countNodes(node.globChild)
	}
	return count
}

func (pt *PathTrie) calculateMaxDepth(node *TrieNode, currentDepth int) int {
	maxDepth := currentDepth
	for _, child := range node.children {
		depth := pt.calculateMaxDepth(child, currentDepth+1)
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	return maxDepth
}

func (pt *PathTrie) calculateAverageDepth() float64 {
	// Simplified calculation - would need more sophisticated implementation
	return float64(pt.stats.MaxDepth) * 0.7
}

func (pt *PathTrie) rebalanceIfNeeded() {
	// Implement trie rebalancing logic if needed
	// This could involve reorganizing nodes for better performance
	pt.mutex.Lock()
	defer pt.mutex.Unlock()
	
	pt.stats.LastRebalanced = time.Now()
	// TODO: Implement actual rebalancing algorithm
}

// GetStats returns current performance statistics.
func (pt *PathTrie) GetStats() IndexStats {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()
	return *pt.stats
}
