package event

// RuleSetChangedEventQueue represents an event queue for ruleset changes
type RuleSetChangedEventQueue struct{}

// Create represents a create event type
var Create = "create"

// Update represents an update event type  
var Update = "update"

// Remove represents a remove event type
var Remove = "remove"
