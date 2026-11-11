// Package metadata contains the metadata of this plugin
package metadata

import "github.com/cloudnative-pg/cnpg-i/pkg/identity"

// PluginName is the name users reference in the Cluster's spec.plugins list
const PluginName = "cnpg-i-pganalyze.pganalyze.com"

// Version is the version of the plugin, set at build time via ldflags
var Version = "dev"

// Data is the metadata of this plugin
var Data = identity.GetPluginMetadataResponse{
	Name:          PluginName,
	Version:       Version,
	DisplayName:   "Monitor CloudNativePG clusters with pganalyze",
	ProjectUrl:    "https://github.com/pganalyze/cnpg-i-pganalyze",
	RepositoryUrl: "https://github.com/pganalyze/cnpg-i-pganalyze",
	License:       "Apache-2.0",
	LicenseUrl:    "https://github.com/pganalyze/cnpg-i-pganalyze/blob/main/LICENSE",
	Maturity:      "alpha",
}
