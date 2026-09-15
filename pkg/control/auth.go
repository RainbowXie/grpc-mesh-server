package control

import (
	"errors"
	"fmt"
)

// AuthPolicy handles token-based authentication.
//
// Two modes exist:
//   - Node-bound tokens (node_tokens): each node_id has its own token. A
//     token is only valid for the node it was issued to, so a leaked token
//     cannot be used to impersonate (and replace) a different node's session.
//   - Legacy shared tokens (allowed_tokens): any node may authenticate with
//     any allowed token. Identity is NOT bound; configure node_tokens when
//     node impersonation matters.
type AuthPolicy struct {
	enabled       bool
	allowedTokens map[string]bool
	nodeTokens    map[string]string
}

// NewAuthPolicy creates a new auth policy. When nodeTokens is non-empty it
// takes precedence and allowedTokens is ignored.
func NewAuthPolicy(enabled bool, tokens []string, nodeTokens map[string]string) *AuthPolicy {
	tokenMap := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		if token != "" {
			tokenMap[token] = true
		}
	}

	bound := make(map[string]string, len(nodeTokens))
	for nodeID, token := range nodeTokens {
		if nodeID != "" && token != "" {
			bound[nodeID] = token
		}
	}

	return &AuthPolicy{
		enabled:       enabled,
		allowedTokens: tokenMap,
		nodeTokens:    bound,
	}
}

// Authorize checks if a token is valid without binding it to an identity.
//
// Deprecated: use AuthorizeNode, which also verifies that the token was
// issued for the claiming node.
func (p *AuthPolicy) Authorize(token string) error {
	if !p.enabled {
		return nil // Auth disabled
	}

	if len(p.allowedTokens) == 0 && len(p.nodeTokens) == 0 {
		return errors.New("no allowed tokens configured")
	}

	if token == "" {
		return errors.New("token required but not provided")
	}

	if p.allowedTokens[token] {
		return nil
	}
	for _, bound := range p.nodeTokens {
		if bound == token {
			return nil
		}
	}

	return errors.New("invalid token")
}

// AuthorizeNode authenticates a handshake claim: the presented token must be
// the one issued to exactly this node. Rejection messages intentionally do
// not distinguish unknown-node from wrong-token to avoid oracle value.
func (p *AuthPolicy) AuthorizeNode(nodeID, token string) error {
	if !p.enabled {
		return nil // Auth disabled
	}

	if token == "" {
		return errors.New("token required but not provided")
	}

	if len(p.nodeTokens) > 0 {
		bound, known := p.nodeTokens[nodeID]
		if !known || bound != token {
			return fmt.Errorf("node %q is not authorized with the provided token", nodeID)
		}
		return nil
	}

	if len(p.allowedTokens) == 0 {
		return errors.New("no allowed tokens configured")
	}

	if !p.allowedTokens[token] {
		return errors.New("invalid token")
	}
	return nil
}

// IdentityBound reports whether the policy binds tokens to node identities.
func (p *AuthPolicy) IdentityBound() bool {
	return p.enabled && len(p.nodeTokens) > 0
}
