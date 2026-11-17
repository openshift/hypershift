package imageprovider

import (
	supportimageprovider "github.com/openshift/hypershift/support/imageprovider"
)

// ReleaseImageProvider provides the functionality to retrieve OpenShift components' container image from a release image.
type ReleaseImageProvider = supportimageprovider.ReleaseImageProvider

// SimpleReleaseImageProvider is a type alias for backward compatibility.
// New code should import from support/imageprovider directly.
type SimpleReleaseImageProvider = supportimageprovider.SimpleReleaseImageProvider

// New creates a new SimpleReleaseImageProvider from a ReleaseImage.
// This is a re-export for backward compatibility.
var New = supportimageprovider.New

// NewFromImages creates a SimpleReleaseImageProvider from a map of component images.
// This is a re-export for backward compatibility.
var NewFromImages = supportimageprovider.NewFromImages

// NewWithRegistryOverrides creates a SimpleReleaseImageProvider that applies
// registry overrides to all component images.
// This is a re-export for backward compatibility.
var NewWithRegistryOverrides = supportimageprovider.NewWithRegistryOverrides
