package osversion

// Supported version floors: the base major release of each Apple OS with
// guaranteed support. Support is guaranteed for the floor release and every
// later one, so release 27 of each OS is guaranteed. Earlier releases are best
// effort; they may function but are not actively tested. Availability checks
// apply the same per-key floors and limits in both tiers (decision record 0052).
const (
	// MacOSSupportedVersionFloor is macOS 26.
	MacOSSupportedVersionFloor = MacOS26
	// IOSSupportedVersionFloor is iOS and iPadOS 26.
	IOSSupportedVersionFloor = 26
	// TvOSSupportedVersionFloor is tvOS 26.
	TvOSSupportedVersionFloor = 26
	// VisionOSSupportedVersionFloor is visionOS 26.
	VisionOSSupportedVersionFloor = 26
	// WatchOSSupportedVersionFloor is watchOS 26.
	WatchOSSupportedVersionFloor = 26
)
