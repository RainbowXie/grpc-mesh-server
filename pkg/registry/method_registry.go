package registry

import (
	"strings"
	"sync"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/rpc"
)

// MethodRegistry manages method catalogs for all peers
type MethodRegistry struct {
	mu sync.RWMutex
	
	// peer -> methods mapping
	peerMethods map[PeerID]*PeerMethodCatalog
	
	// method -> peers reverse index
	methodPeers map[string][]PeerID
}

// PeerMethodCatalog holds methods for a single peer
type PeerMethodCatalog struct {
	PeerID      PeerID
	Methods     []*rpc.MethodDescriptor
	Version     uint64
	UpdatedAt   time.Time
}

// MethodRegistryStats holds statistics
type MethodRegistryStats struct {
	TotalPeers       int
	TotalMethods     int
	AverageMethods   float64
	LastUpdate       time.Time
}

// NewMethodRegistry creates a new method registry
func NewMethodRegistry() *MethodRegistry {
	return &MethodRegistry{
		peerMethods: make(map[PeerID]*PeerMethodCatalog),
		methodPeers: make(map[string][]PeerID),
	}
}

// UpdateMethods updates methods for a peer
func (mr *MethodRegistry) UpdateMethods(peerID PeerID, methods []*rpc.MethodDescriptor) uint64 {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	
	// Remove old reverse index entries
	if old, exists := mr.peerMethods[peerID]; exists {
		for _, method := range old.Methods {
			mr.removeFromIndex(method.Name, peerID)
		}
	}
	
	// Create new catalog
	now := time.Now()
	version := uint64(now.Unix())
	
	catalog := &PeerMethodCatalog{
		PeerID:    peerID,
		Methods:   methods,
		Version:   version,
		UpdatedAt: now,
	}
	
	mr.peerMethods[peerID] = catalog
	
	// Build reverse index
	for _, method := range methods {
		mr.addToIndex(method.Name, peerID)
	}
	
	return version
}

// GetPeerMethods returns all methods for a peer
func (mr *MethodRegistry) GetPeerMethods(peerID PeerID) ([]*rpc.MethodDescriptor, bool) {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	
	catalog, exists := mr.peerMethods[peerID]
	if !exists {
		return nil, false
	}
	
	// Return a copy
	methods := make([]*rpc.MethodDescriptor, len(catalog.Methods))
	copy(methods, catalog.Methods)
	
	return methods, true
}

// GetMethod returns a specific method for a peer
func (mr *MethodRegistry) GetMethod(peerID PeerID, methodName string) (*rpc.MethodDescriptor, bool) {
	methods, exists := mr.GetPeerMethods(peerID)
	if !exists {
		return nil, false
	}
	
	for _, method := range methods {
		if method.Name == methodName {
			return method, true
		}
	}
	
	return nil, false
}

// FindPeersWithMethod returns all peers that provide a method
func (mr *MethodRegistry) FindPeersWithMethod(methodName string) []PeerID {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	
	peers, exists := mr.methodPeers[methodName]
	if !exists {
		return nil
	}
	
	// Return a copy
	result := make([]PeerID, len(peers))
	copy(result, peers)
	
	return result
}

// FindMethods searches for methods matching a pattern
func (mr *MethodRegistry) FindMethods(pattern string) map[PeerID][]*rpc.MethodDescriptor {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	
	result := make(map[PeerID][]*rpc.MethodDescriptor)
	
	for peerID, catalog := range mr.peerMethods {
		var matched []*rpc.MethodDescriptor
		
		for _, method := range catalog.Methods {
			if matchPattern(method.Name, pattern) {
				matched = append(matched, method)
			}
		}
		
		if len(matched) > 0 {
			result[peerID] = matched
		}
	}
	
	return result
}

// GetCatalog returns the complete method catalog
func (mr *MethodRegistry) GetCatalog() map[PeerID][]*rpc.MethodDescriptor {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	
	result := make(map[PeerID][]*rpc.MethodDescriptor, len(mr.peerMethods))
	
	for peerID, catalog := range mr.peerMethods {
		methods := make([]*rpc.MethodDescriptor, len(catalog.Methods))
		copy(methods, catalog.Methods)
		result[peerID] = methods
	}
	
	return result
}

// RemovePeer removes all methods for a peer
func (mr *MethodRegistry) RemovePeer(peerID PeerID) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	
	catalog, exists := mr.peerMethods[peerID]
	if !exists {
		return
	}
	
	// Remove from reverse index
	for _, method := range catalog.Methods {
		mr.removeFromIndex(method.Name, peerID)
	}
	
	delete(mr.peerMethods, peerID)
}

// Stats returns statistics
func (mr *MethodRegistry) Stats() MethodRegistryStats {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	
	totalMethods := 0
	var lastUpdate time.Time
	
	for _, catalog := range mr.peerMethods {
		totalMethods += len(catalog.Methods)
		if catalog.UpdatedAt.After(lastUpdate) {
			lastUpdate = catalog.UpdatedAt
		}
	}
	
	avg := 0.0
	if len(mr.peerMethods) > 0 {
		avg = float64(totalMethods) / float64(len(mr.peerMethods))
	}
	
	return MethodRegistryStats{
		TotalPeers:     len(mr.peerMethods),
		TotalMethods:   totalMethods,
		AverageMethods: avg,
		LastUpdate:     lastUpdate,
	}
}

func (mr *MethodRegistry) addToIndex(methodName string, peerID PeerID) {
	peers := mr.methodPeers[methodName]
	
	// Check if already exists
	for _, p := range peers {
		if p == peerID {
			return
		}
	}
	
	mr.methodPeers[methodName] = append(peers, peerID)
}

func (mr *MethodRegistry) removeFromIndex(methodName string, peerID PeerID) {
	peers := mr.methodPeers[methodName]
	
	for i, p := range peers {
		if p == peerID {
			mr.methodPeers[methodName] = append(peers[:i], peers[i+1:]...)
			break
		}
	}
	
	// Clean up empty entries
	if len(mr.methodPeers[methodName]) == 0 {
		delete(mr.methodPeers, methodName)
	}
}

func matchPattern(name, pattern string) bool {
	if pattern == "*" {
		return true
	}
	
	if !strings.Contains(pattern, "*") {
		return name == pattern
	}
	
	// Simple wildcard matching
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		substr := strings.Trim(pattern, "*")
		return strings.Contains(name, substr)
	}
	
	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(name, suffix)
	}
	
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(name, prefix)
	}
	
	return false
}
