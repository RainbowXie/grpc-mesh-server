package server

import (
	"strings"
)

// Action represents ACL action
type Action int

const (
	ActionDeny Action = iota
	ActionAllow
)

// ACLRule represents a single access control rule
type ACLRule struct {
	PeerPattern   string
	MethodPattern string
	Action        Action
	Priority      int
}

// ACLPolicy interface
type ACLPolicy interface {
	Check(peerID, method string) bool
	Match(peerID, method string) (*ACLRule, bool)
}

// StaticACL implements static rule-based ACL
type StaticACL struct {
	rules []ACLRule
}

// NewStaticACL creates a new static ACL
func NewStaticACL(rules []ACLRule) *StaticACL {
	return &StaticACL{rules: rules}
}

// Check checks if access is allowed
func (acl *StaticACL) Check(peerID, method string) bool {
	rule, found := acl.Match(peerID, method)
	if !found {
		return false // Default deny
	}
	return rule.Action == ActionAllow
}

// Match finds the matching rule
func (acl *StaticACL) Match(peerID, method string) (*ACLRule, bool) {
	var bestMatch *ACLRule
	bestPriority := -1
	
	for i := range acl.rules {
		rule := &acl.rules[i]
		
		if matchPattern(peerID, rule.PeerPattern) && 
		   matchPattern(method, rule.MethodPattern) {
			if rule.Priority > bestPriority {
				bestMatch = rule
				bestPriority = rule.Priority
			}
		}
	}
	
	return bestMatch, bestMatch != nil
}

// UpdateRules updates the rule set
func (acl *StaticACL) UpdateRules(rules []ACLRule) {
	acl.rules = rules
}

// PermissiveACL allows all access
type PermissiveACL struct{}

func NewPermissiveACL() ACLPolicy {
	return &PermissiveACL{}
}

func (acl *PermissiveACL) Check(peerID, method string) bool {
	return true
}

func (acl *PermissiveACL) Match(peerID, method string) (*ACLRule, bool) {
	return &ACLRule{
		PeerPattern:   "*",
		MethodPattern: "*",
		Action:        ActionAllow,
		Priority:      0,
	}, true
}

// RestrictiveACL denies all access
type RestrictiveACL struct{}

func NewRestrictiveACL() ACLPolicy {
	return &RestrictiveACL{}
}

func (acl *RestrictiveACL) Check(peerID, method string) bool {
	return false
}

func (acl *RestrictiveACL) Match(peerID, method string) (*ACLRule, bool) {
	return &ACLRule{
		PeerPattern:   "*",
		MethodPattern: "*",
		Action:        ActionDeny,
		Priority:      0,
	}, true
}

func matchPattern(value, pattern string) bool {
	if pattern == "*" {
		return true
	}
	
	if !strings.Contains(pattern, "*") {
		return value == pattern
	}
	
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		substr := strings.Trim(pattern, "*")
		return strings.Contains(value, substr)
	}
	
	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(value, suffix)
	}
	
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(value, prefix)
	}
	
	return false
}
