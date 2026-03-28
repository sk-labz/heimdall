# Heimdall Rule Lookup Optimization Plan

## Problem Statement

**Current Issue**: Heimdall's rule lookup uses linear search (`O(n)`) through all rules for URL matching, which becomes a performance bottleneck as the number of rules grows.

**Current Implementation Analysis**:
```go
// internal/rules/repository_impl.go:61-77
func (r *repository) FindRule(requestURL *url.URL) (rule.Rule, error) {
    r.mutex.RLock()
    defer r.mutex.RUnlock()

    for _, rul := range r.rules {  // O(n) linear search
        if rul.MatchesURL(requestURL) {
            return rul, nil
        }
    }
    // ... fallback to default rule
}
```

**Performance Impact**:
- With 100 rules: ~100 pattern matches per request
- With 1000 rules: ~1000 pattern matches per request
- Each pattern match involves glob/regex compilation overhead
- Read lock contention increases with request concurrency

## Solution Overview

This comprehensive optimization plan implements a **3-tier performance improvement strategy**:

1. **Tier 1**: URL Path Trie Index (Primary optimization)
2. **Tier 2**: Multi-level Caching System  
3. **Tier 3**: Rule Compilation & Pre-processing

**Expected Performance Gains**:
- 🚀 **50-80% reduction** in rule lookup latency
- 🔄 **90% reduction** in pattern matching operations
- 📈 **3-5x improvement** in concurrent request throughput
- 💾 **60% reduction** in CPU usage for rule matching

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Request Processing                        │
├─────────────────────────────────────────────────────────────┤
│  URL: /api/v1/users/123/profile                           │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────────────┐
│               Tier 1: URL Path Trie Index                  │
├─────────────────────────────────────────────────────────────┤
│  /api          [Node: rules=[rule1, rule2]]                │
│    └─ /v1      [Node: rules=[rule3]]                       │
│       └─ /users [Node: rules=[rule4, rule5]]               │
│          └─ /*  [Node: rules=[rule6]]                      │
└─────────────────┬───────────────────────────────────────────┘
                  │ O(log n) lookup
                  ▼
┌─────────────────────────────────────────────────────────────┐
│              Tier 2: Multi-level Cache                     │
├─────────────────────────────────────────────────────────────┤
│  L1: LRU Cache    [URL -> Rule mapping]                    │
│  L2: Pattern Cache [Compiled matchers]                     │
│  L3: Method Cache  [Method -> Rule subset]                 │
└─────────────────┬───────────────────────────────────────────┘
                  │ Cache hit: O(1)
                  ▼
┌─────────────────────────────────────────────────────────────┐
│            Tier 3: Compiled Rule Matching                  │
├─────────────────────────────────────────────────────────────┤
│  Pre-compiled patterns, optimized matching order           │
│  Statistical prioritization, batch compilation             │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
              [Rule Found]
```

## Implementation Strategy

### Phase 1: URL Path Trie Index (Core Optimization)

**Goal**: Replace linear search with trie-based O(log n) lookup

**Components**:
1. **PathTrie**: Hierarchical URL path indexing
2. **RuleIndex**: Mapping from trie nodes to rule candidates
3. **IndexManager**: Maintains trie consistency during rule updates

**Key Features**:
- Path segment-based trie structure
- Wildcard and pattern support (`*`, `**`, `{param}`)
- Priority-based rule ordering
- Concurrent read access with minimal locking

### Phase 2: Multi-level Caching System

**Goal**: Eliminate repeated pattern matching overhead

**Cache Layers**:
1. **L1 LRU Cache**: Direct URL → Rule mapping (hot paths)
2. **L2 Pattern Cache**: Pre-compiled glob/regex matchers
3. **L3 Method Cache**: HTTP method → Rule subset mapping

**Cache Strategy**:
- Adaptive cache sizing based on memory pressure
- TTL-based invalidation with rule change notifications
- Statistics-driven cache warming

### Phase 3: Rule Compilation & Pre-processing

**Goal**: Optimize individual rule matching performance

**Optimizations**:
1. **Pattern Compilation**: Pre-compile all glob/regex patterns
2. **Rule Prioritization**: Statistical reordering based on match frequency
3. **Batch Processing**: Group rules by common prefixes
4. **Fast-path Detection**: Identify exact-match rules for O(1) lookup

## Detailed Technical Design

### 1. URL Path Trie Implementation

```go
type PathTrie struct {
    root    *TrieNode
    rules   map[string]*CompiledRule  // Rule ID -> Compiled Rule
    mutex   sync.RWMutex
    stats   *IndexStats
}

type TrieNode struct {
    segment      string                 // Path segment
    children     map[string]*TrieNode   // Static children
    wildcardChild *TrieNode             // * wildcard
    paramChild   *TrieNode              // {param} pattern
    globChild    *TrieNode              // ** glob pattern
    rules        []*RuleReference       // Rules applicable to this path
    priority     int                    // Node priority for ordering
}

type RuleReference struct {
    rule     rule.Rule
    priority int
    pattern  CompiledPattern
    methods  []string
}
```

### 2. Multi-level Cache Design

```go
type CacheManager struct {
    l1Cache     *LRUCache[string, rule.Rule]      // URL -> Rule
    l2Cache     *LRUCache[string, CompiledPattern] // Pattern -> Compiled
    l3Cache     *LRUCache[string, []rule.Rule]    // Method -> Rules
    stats       *CacheStats
    invalidator *CacheInvalidator
}

type CacheStats struct {
    L1Hits, L1Misses int64
    L2Hits, L2Misses int64  
    L3Hits, L3Misses int64
    HitRatio         float64
    EvictionCount    int64
}
```

### 3. Compiled Rule System

```go
type CompiledRule struct {
    original    rule.Rule
    patterns    []CompiledPattern
    methods     []string
    priority    int
    frequency   int64              // Match frequency for prioritization
    fastPath    bool              // True for exact-match rules
    lastMatched time.Time         // For LRU prioritization
}

type CompiledPattern struct {
    pattern     string
    matcher     patternmatcher.PatternMatcher
    complexity  int               // Pattern complexity score
    segments    []string          // Pre-split path segments
    hasWildcard bool
    isExact     bool
}
```

## Implementation Plan

### Week 1: Core Trie Implementation
- [ ] Implement `PathTrie` and `TrieNode` structures
- [ ] Build URL path parsing and trie insertion logic
- [ ] Create basic trie traversal and lookup methods
- [ ] Add unit tests for trie operations

### Week 2: Integration with Existing Repository
- [ ] Create new `OptimizedRepository` implementing `rule.Repository`
- [ ] Implement rule addition/removal with trie updates
- [ ] Add migration path from old repository
- [ ] Ensure backward compatibility

### Week 3: Caching Layer Implementation
- [ ] Implement `CacheManager` with LRU caches
- [ ] Add cache invalidation on rule changes
- [ ] Implement cache statistics and monitoring
- [ ] Add cache warming strategies

### Week 4: Rule Compilation & Optimization
- [ ] Implement `CompiledRule` and pattern pre-processing
- [ ] Add rule prioritization based on usage statistics
- [ ] Implement fast-path detection for exact matches
- [ ] Performance testing and tuning

### Week 5: Testing & Performance Validation
- [ ] Comprehensive unit and integration tests
- [ ] Performance benchmarks comparing old vs new implementation
- [ ] Load testing with realistic rule sets
- [ ] Memory usage analysis and optimization

### Week 6: Production Deployment
- [ ] Feature flag implementation for gradual rollout
- [ ] Monitoring and alerting setup
- [ ] Documentation and migration guide
- [ ] Performance monitoring in production

## Testing Strategy

### 1. Unit Tests
- Trie operations (insert, lookup, delete)
- Cache behavior and invalidation
- Pattern compilation and matching
- Concurrent access safety

### 2. Integration Tests  
- End-to-end rule lookup flows
- Rule provider integration
- Performance under load
- Memory usage patterns

### 3. Performance Tests
- Benchmark comparison (old vs new)
- Scalability testing (100, 1K, 10K rules)
- Concurrent request handling
- Memory efficiency analysis

### 4. Compatibility Tests
- Existing rule format support
- Pattern matching behavior preservation
- API compatibility verification
- Migration path validation

## Monitoring & Observability

### Key Metrics
- **Rule Lookup Latency**: P50, P95, P99 percentiles
- **Cache Hit Ratios**: L1, L2, L3 cache performance
- **Trie Depth & Balance**: Index structure health
- **Memory Usage**: Cache and trie memory consumption
- **Rule Match Frequency**: For prioritization optimization

### Dashboards
1. **Performance Dashboard**: Latency trends, throughput metrics
2. **Cache Dashboard**: Hit ratios, eviction rates, warming effectiveness  
3. **Index Dashboard**: Trie structure health, rebalancing needs
4. **Memory Dashboard**: Usage patterns, GC impact

## Risk Assessment & Mitigation

### High Risks
1. **Memory Usage**: Trie and cache structures increase memory footprint
   - *Mitigation*: Configurable cache sizes, memory pressure monitoring
   
2. **Complexity**: New indexing logic introduces potential bugs
   - *Mitigation*: Comprehensive testing, gradual rollout with feature flags

3. **Migration Issues**: Breaking changes during transition
   - *Mitigation*: Backward compatibility layer, parallel running

### Medium Risks
1. **Pattern Compatibility**: New matching behavior differs from original
   - *Mitigation*: Extensive compatibility testing, regression tests

2. **Performance Regression**: Edge cases where new system is slower
   - *Mitigation*: Comprehensive benchmarking, fallback mechanisms

## Configuration Options

```yaml
rules:
  optimization:
    enabled: true
    
    # Trie Configuration
    trie:
      max_depth: 20
      rebalance_threshold: 1000
      
    # Cache Configuration  
    cache:
      l1_size: 10000      # URL -> Rule cache
      l2_size: 5000       # Pattern cache
      l3_size: 1000       # Method cache
      ttl: 300s           # Cache TTL
      
    # Compilation Options
    compilation:
      batch_size: 100
      prioritization: true
      fast_path_detection: true
      
    # Monitoring
    monitoring:
      enabled: true
      metrics_interval: 30s
      statistics_retention: 24h
```

## Success Criteria

### Performance Targets
- ✅ **50-80% reduction** in P95 rule lookup latency
- ✅ **90% cache hit ratio** for L1 cache after warmup
- ✅ **3x improvement** in concurrent request throughput
- ✅ **Linear scalability** up to 10,000 rules

### Quality Targets  
- ✅ **100% backward compatibility** with existing rules
- ✅ **Zero regression** in pattern matching behavior
- ✅ **95% test coverage** for new optimization code
- ✅ **< 50MB additional memory** usage for 1000 rules

### Operational Targets
- ✅ **Zero-downtime deployment** capability
- ✅ **Real-time monitoring** of optimization effectiveness
- ✅ **Graceful degradation** on optimization failures
- ✅ **Sub-second warmup** time on service restart

---

## Next Steps

1. **Review and approve** this optimization plan
2. **Set up development environment** with benchmarking tools
3. **Create GitHub issues** for each implementation phase
4. **Begin Week 1 implementation** with core trie structure
5. **Establish performance baseline** measurements with current system

This optimization will significantly improve Heimdall's performance and scalability while maintaining full backward compatibility and operational reliability.
