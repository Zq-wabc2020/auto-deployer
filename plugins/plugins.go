// Package plugins registers all built-in deployment model plugins via blank
// imports, so each plugin's init() (which calls registry.Register) runs at
// program startup. Import this package for its side effects only.
package plugins

import (
	_ "github.com/auto-deployer/auto-deployer/plugins/docker"
	_ "github.com/auto-deployer/auto-deployer/plugins/springboot"
)
