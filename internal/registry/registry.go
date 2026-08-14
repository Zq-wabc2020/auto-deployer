// Package registry provides a plugin registry for deployment models.
//
// Each deployment model plugin registers itself (via init()) under one or more
// type names. Callers (CLI, webhook) resolve a service's type to a fresh
// Deployer instance with Get, instead of scattering switch statements.
package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/auto-deployer/auto-deployer/internal/deploy"
)

// Factory creates a fresh Deployer instance for a service type.
type Factory func() deploy.Deployer

var (
	mu      sync.Mutex
	plugins = map[string]Factory{}
)

// Register a deployment model under name. Called from each plugin's init().
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	plugins[name] = f
}

// Get returns a new Deployer for the given type name.
func Get(name string) (deploy.Deployer, error) {
	mu.Lock()
	f, ok := plugins[name]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown service type %q (supported: %v)", name, Types())
	}
	return f(), nil
}

// Types returns the sorted list of registered type names.
func Types() []string {
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, 0, len(plugins))
	for n := range plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
