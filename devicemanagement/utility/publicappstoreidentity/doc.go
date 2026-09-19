// Package publicappstoreidentity discovers public App Store bundle identifiers
// for use when authoring device management configurations.
//
// # Design
//
// Client searches by term or looks up a numeric App Store ID in an explicit
// country storefront and software entity through Apple's iTunes Search API.
// Search can filter the returned listings by developer name. Results preserve
// Apple's platform metadata and the storefront used for the request; callers
// choose among matches. Each operation makes one HTTP request with bounded
// response decoding and a context deadline. The limit applies before local
// filtering. Callers supply caching, rate limiting and any custom HTTP transport.
//
// Listing metadata identifies a public store entry. Native code-signing identity
// belongs to utility/appidentity. Callers use selected bundle identifiers in
// their existing payload types. The client does not query private Apps and
// Books inventory or undocumented external-version endpoints.
//
// # References
//
//   - Utility guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/devicemanagement/utility/README.md
//   - Apple search parameters: https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/Searching.html
//   - Apple ID lookup: https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/LookupExamples.html
package publicappstoreidentity
