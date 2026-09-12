package contentcache

import "encoding/json/jsontext"

// Report carries Apple content-cache metrics. Scalar pointers distinguish omitted
// properties from explicit zero, false, or empty values. Extra retains extensions.
//
//nolint:tagliatelle // property names follow Apple OpenAPI exactly
type Report struct {
	Version                      *int64                    `json:"version,omitzero"`
	ReportDate                   *string                   `json:"reportDate,omitzero"`
	CreationDate                 *string                   `json:"creationDate,omitzero"`
	Hostname                     *string                   `json:"hostname,omitzero"`
	Hardware                     *string                   `json:"hardware,omitzero"`
	MemorySize                   *int64                    `json:"memorySize,omitzero"`
	HardwareUUID                 *string                   `json:"hardwareUUID,omitzero"`
	SerialNumber                 *string                   `json:"serialNumber,omitzero"`
	BuildVersion                 *string                   `json:"buildVersion,omitzero"`
	ConnectedClients             *int64                    `json:"connectedClients,omitzero"`
	UniqueClients                *int64                    `json:"uniqueClients,omitzero"`
	PeakClients                  *int64                    `json:"peakClients,omitzero"`
	NumberOfCacheEntries         *int64                    `json:"numberOfCacheEntries,omitzero"`
	Transactions                 *int64                    `json:"transactions,omitzero"`
	RecentErrors                 *string                   `json:"recentErrors,omitzero"`
	CPULoad                      *float64                  `json:"cpuLoad,omitzero"`
	ReportPeriod                 *int64                    `json:"reportPeriod,omitzero"`
	AssetsLessThan10M            *int64                    `json:"assetsLessThan10M,omitzero"`
	AssetsFrom10to100M           *int64                    `json:"assetsFrom10to100M,omitzero"`
	AssetsFrom100Mto1G           *int64                    `json:"assetsFrom100Mto1G,omitzero"`
	AssetsMoreThan1G             *int64                    `json:"assetsMoreThan1G,omitzero"`
	AverageAssetSizeInM          *int64                    `json:"averageAssetSizeInM,omitzero"`
	ServerGUID                   *string                   `json:"serverGUID,omitzero"`
	RegistrationState            *int64                    `json:"registrationState,omitzero"`
	RegistrationStarted          *string                   `json:"registrationStarted,omitzero"`
	RegistrationError            *int64                    `json:"registrationError,omitzero"`
	ActualCacheUsed              *int64                    `json:"actualCacheUsed,omitzero"`
	StartupStatus                *bool                     `json:"startupStatus,omitzero"`
	RestrictedMedia              *bool                     `json:"restrictedMedia,omitzero"`
	TetheratorStatus             *int64                    `json:"tetheratorStatus,omitzero"`
	Active                       *bool                     `json:"active,omitzero"`
	Activated                    *bool                     `json:"activated,omitzero"`
	CacheDetails                 *string                   `json:"cacheDetails,omitzero"`
	CacheFree                    *int64                    `json:"cacheFree,omitzero"`
	CacheLimit                   *int64                    `json:"cacheLimit,omitzero"`
	CacheUsed                    *int64                    `json:"cacheUsed,omitzero"`
	PublicAddress                *string                   `json:"publicAddress,omitzero"`
	Port                         *int64                    `json:"port,omitzero"`
	PrivateAddresses             *string                   `json:"privateAddresses,omitzero"`
	Parents                      []Parent                  `json:"parents,omitzero"`
	Peers                        []Peer                    `json:"peers,omitzero"`
	PersonalCacheFree            *int64                    `json:"personalCacheFree,omitzero"`
	PersonalCacheUsed            *int64                    `json:"personalCacheUsed,omitzero"`
	PersonalCacheLimit           *int64                    `json:"personalCacheLimit,omitzero"`
	AllowPersonalCaching         *bool                     `json:"allowPersonalCaching,omitzero"`
	AllowSharedCaching           *bool                     `json:"allowSharedCaching,omitzero"`
	AllowTetheredCaching         *bool                     `json:"allowTetheredCaching,omitzero"`
	ListenRangesOnly             *bool                     `json:"listenRangesOnly,omitzero"`
	LocalSubnetsOnly             *bool                     `json:"localSubnetsOnly,omitzero"`
	PeerLocalSubnetsOnly         *bool                     `json:"peerLocalSubnetsOnly,omitzero"`
	ListenRanges                 *string                   `json:"listenRanges,omitzero"`
	ParentSelectionPolicy        *string                   `json:"parentSelectionPolicy,omitzero"`
	Period                       *int64                    `json:"period,omitzero"`
	BytesDropped                 *int64                    `json:"bytesDropped,omitzero"`
	BytesFromCacheToChild        *int64                    `json:"bytesFromCacheToChild,omitzero"`
	BytesFromCacheToClient       *int64                    `json:"bytesFromCacheToClient,omitzero"`
	BytesFromCacheToPeer         *int64                    `json:"bytesFromCacheToPeer,omitzero"`
	BytesFromOriginToChild       *int64                    `json:"bytesFromOriginToChild,omitzero"`
	BytesFromOriginToClient      *int64                    `json:"bytesFromOriginToClient,omitzero"`
	BytesFromOriginToPeer        *int64                    `json:"bytesFromOriginToPeer,omitzero"`
	BytesFromParentToChild       *int64                    `json:"bytesFromParentToChild,omitzero"`
	BytesFromParentToClient      *int64                    `json:"bytesFromParentToClient,omitzero"`
	BytesFromParentToPeer        *int64                    `json:"bytesFromParentToPeer,omitzero"`
	BytesFromPeerToChild         *int64                    `json:"bytesFromPeerToChild,omitzero"`
	BytesFromPeerToClient        *int64                    `json:"bytesFromPeerToClient,omitzero"`
	BytesImportedByHTTP          *int64                    `json:"bytesImportedByHTTP,omitzero"`
	BytesImportedByXPC           *int64                    `json:"bytesImportedByXPC,omitzero"`
	BytesPurgedTotal             *int64                    `json:"bytesPurgedTotal,omitzero"`
	BytesPurgedYoungerThan1Day   *int64                    `json:"bytesPurgedYoungerThan1Day,omitzero"`
	BytesPurgedYoungerThan7Days  *int64                    `json:"bytesPurgedYoungerThan7Days,omitzero"`
	BytesPurgedYoungerThan30Days *int64                    `json:"bytesPurgedYoungerThan30Days,omitzero"`
	ImportsByHTTP                *int64                    `json:"importsByHTTP,omitzero"`
	ImportsByXPC                 *int64                    `json:"importsByXPC,omitzero"`
	RepliesFromCacheToChild      *int64                    `json:"repliesFromCacheToChild,omitzero"`
	RepliesFromCacheToClient     *int64                    `json:"repliesFromCacheToClient,omitzero"`
	RepliesFromCacheToPeer       *int64                    `json:"repliesFromCacheToPeer,omitzero"`
	RepliesFromOriginToChild     *int64                    `json:"repliesFromOriginToChild,omitzero"`
	RepliesFromOriginToClient    *int64                    `json:"repliesFromOriginToClient,omitzero"`
	RepliesFromOriginToPeer      *int64                    `json:"repliesFromOriginToPeer,omitzero"`
	RepliesFromParentToChild     *int64                    `json:"repliesFromParentToChild,omitzero"`
	RepliesFromParentToClient    *int64                    `json:"repliesFromParentToClient,omitzero"`
	RepliesFromParentToPeer      *int64                    `json:"repliesFromParentToPeer,omitzero"`
	RepliesFromPeerToChild       *int64                    `json:"repliesFromPeerToChild,omitzero"`
	RepliesFromPeerToClient      *int64                    `json:"repliesFromPeerToClient,omitzero"`
	RequestsFromChild            *int64                    `json:"requestsFromChild,omitzero"`
	RequestsFromClient           *int64                    `json:"requestsFromClient,omitzero"`
	RequestsFromPeer             *int64                    `json:"requestsFromPeer,omitzero"`
	RequestsRejectedForNoSpace   *int64                    `json:"requestsRejectedForNoSpace,omitzero"`
	Extra                        map[string]jsontext.Value `json:",embed"`
}

// Parent carries Apple content-cache metrics. Scalar pointers distinguish omitted
// properties from explicit zero, false, or empty values. Extra retains extensions.
//
//nolint:tagliatelle // property names follow Apple OpenAPI exactly
type Parent struct {
	Address *string                   `json:"address,omitzero"`
	Port    *int64                    `json:"port,omitzero"`
	GUID    *string                   `json:"guid,omitzero"`
	Healthy *bool                     `json:"healthy,omitzero"`
	Version *string                   `json:"version,omitzero"`
	Details *string                   `json:"details,omitzero"`
	Extra   map[string]jsontext.Value `json:",embed"`
}

// Peer carries Apple content-cache metrics. Scalar pointers distinguish omitted
// properties from explicit zero, false, or empty values. Extra retains extensions.
//
//nolint:tagliatelle // property names follow Apple OpenAPI exactly
type Peer struct {
	Address  *string                   `json:"address,omitzero"`
	Port     *int64                    `json:"port,omitzero"`
	GUID     *string                   `json:"guid,omitzero"`
	Healthy  *bool                     `json:"healthy,omitzero"`
	Friendly *bool                     `json:"friendly,omitzero"`
	Version  *string                   `json:"version,omitzero"`
	Details  *string                   `json:"details,omitzero"`
	Extra    map[string]jsontext.Value `json:",embed"`
}
