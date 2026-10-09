package toolbox

// productVersion is the Facet release version recorded in result provenance.
// The command entry point sets it from its single build-time version variable.
var productVersion = "dev"

// SetProductVersion records the Facet release version for result provenance.
func SetProductVersion(v string) {
	if v != "" {
		productVersion = v
	}
}

// ProductVersion returns the recorded Facet release version.
func ProductVersion() string { return productVersion }
