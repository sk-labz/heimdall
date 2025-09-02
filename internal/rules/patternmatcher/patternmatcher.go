package patternmatcher

// PatternMatcher interface for pattern matching
type PatternMatcher interface {
	Match(pattern string) bool
}

// patternMatcher implements PatternMatcher
type patternMatcher struct{}

// NewPatternMatcher creates a new pattern matcher
func NewPatternMatcher() PatternMatcher {
	return &patternMatcher{}
}

// Match implements pattern matching (stub implementation)
func (pm *patternMatcher) Match(pattern string) bool {
	return true
}
