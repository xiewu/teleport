package clusters

import (
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/teleterm/api/uri"

)

// WindowsDesktop describes a SAML IdP resource.
type WindowsDesktop struct {
	// URI is the app URI
	URI uri.ResourceURI

	WindowsDesktop types.WindowsDesktop
	Logins         []string
}