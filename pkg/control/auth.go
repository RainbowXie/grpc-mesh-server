package control

import (
	"errors"
)

// AuthPolicy handles token-based authentication
type AuthPolicy struct {
	enabled       bool
	allowedTokens map[string]bool
}

// NewAuthPolicy creates a new auth policy
func NewAuthPolicy(enabled bool, tokens []string) *AuthPolicy {
	tokenMap := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		if token != "" {
			tokenMap[token] = true
		}
	}

	return &AuthPolicy{
		enabled:       enabled,
		allowedTokens: tokenMap,
	}
}

// Authorize checks if a token is valid
func (p *AuthPolicy) Authorize(token string) error {
	if !p.enabled {
		return nil // Auth disabled
	}

	if len(p.allowedTokens) == 0 {
		return errors.New("no allowed tokens configured")
	}

	if token == "" {
		return errors.New("token required but not provided")
	}

	if !p.allowedTokens[token] {
		return errors.New("invalid token")
	}

	return nil
}
