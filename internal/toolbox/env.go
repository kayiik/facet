package toolbox

import (
	"sort"

	"github.com/kayiik/facet/internal/facethome"
)

// EnvVars returns the environment variables Facet's tools read while they
// run: provider credentials and endpoints, local overrides, Facet's home
// folder, and the proxy and certificate settings the HTTP clients and the
// Node-based renderers honour.
//
// An agentic CLI may start MCP servers with a filtered environment. Codex
// passes only a short allowlist (PATH, the home and temporary folders) unless
// the server's env_vars names more, so a key the person exported would never
// reach the tool that needs it. Facet's Codex wiring and Codex plugin forward
// exactly this list. TestEnvVarsCoverEveryVariableTheToolsRead keeps it
// complete.
func EnvVars() []string {
	vars := []string{
		// Provider credentials, as facet doctor probes them.
		"OPENAI_API_KEY", "ELEVENLABS_API_KEY", "FAL_KEY", "FLUX_API_KEY",
		"KLING_API_KEY", "PEXELS_API_KEY", "PIXABAY_API_KEY",
		// Provider endpoints.
		"OPENAI_BASE_URL", "FAL_BASE_URL", "FAL_QUEUE_BASE_URL",
		// Local programs and libraries.
		ComposerDirEnv, hyperframesEnv, "FACET_PIPER_MODEL", "MUSIC_LIBRARY_DIR",
		"REMOTION_BROWSER_EXECUTABLE", "PUPPETEER_EXECUTABLE_PATH", "CHROME_PATH",
		// Facet's home folder.
		facethome.EnvVar,
		// Network settings: Go's HTTP client reads both spellings of the
		// proxy variables, and Node reads its own extra certificates.
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	}
	sort.Strings(vars)
	return vars
}
