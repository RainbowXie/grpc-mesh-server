package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStaticACLExactMatch tests exact pattern matching
func TestStaticACLExactMatch(t *testing.T) {
	rules := []ACLRule{
		{
			PeerPattern:   "node-001",
			MethodPattern: "service.Method",
			Action:        ActionAllow,
			Priority:      100,
		},
		{
			PeerPattern:   "*",
			MethodPattern: "*",
			Action:        ActionDeny,
			Priority:      1,
		},
	}

	acl := NewStaticACL(rules)

	// Should allow exact match
	assert.True(t, acl.Check("node-001", "service.Method"))

	// Should deny non-match
	assert.False(t, acl.Check("node-002", "service.Method"))
	assert.False(t, acl.Check("node-001", "service.OtherMethod"))
}

// TestStaticACLWildcardMatch tests wildcard matching
func TestStaticACLWildcardMatch(t *testing.T) {
	rules := []ACLRule{
		{
			PeerPattern:   "*",
			MethodPattern: "health.*",
			Action:        ActionAllow,
			Priority:      100,
		},
		{
			PeerPattern:   "prod-*",
			MethodPattern: "*",
			Action:        ActionAllow,
			Priority:      200,
		},
	}

	acl := NewStaticACL(rules)

	// Health methods allowed for all
	assert.True(t, acl.Check("any-node", "health.ping"))
	assert.True(t, acl.Check("any-node", "health.check"))

	// Prod nodes allowed for all methods
	assert.True(t, acl.Check("prod-node-001", "any.method"))
	assert.True(t, acl.Check("prod-worker-01", "service.call"))
}

// TestPermissiveACL tests permissive policy
func TestPermissiveACL(t *testing.T) {
	acl := NewPermissiveACL()

	// Should allow everything
	assert.True(t, acl.Check("any-peer", "any.method"))
	assert.True(t, acl.Check("", ""))
}

// TestRestrictiveACL tests restrictive policy
func TestRestrictiveACL(t *testing.T) {
	acl := NewRestrictiveACL()

	// Should deny everything
	assert.False(t, acl.Check("any-peer", "any.method"))
	assert.False(t, acl.Check("trusted", "safe.method"))
}
