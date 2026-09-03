package apns

import (
	"net/http"
	"sort"
)

// APNs answers a failed push with a JSON body carrying a reason string from
// a closed set Apple publishes. The set is transcribed here so a caller
// classifying failures compares against named constants rather than string
// literals, and so a reason may be used as a metric label without the
// unbounded cardinality an arbitrary server string would carry.
//
// Apple pairs each reason with exactly one HTTP status. The client branches
// on the status it actually received, never on the status recorded here;
// Status is what Apple documents, for explaining a failure rather than
// deciding one.
//
// Source: Handling notification responses from APNs.
const (
	ReasonBadCollapseID               = "BadCollapseId"
	ReasonBadDeviceToken              = "BadDeviceToken" // #nosec G101 -- APNs reason string, not a credential
	ReasonBadExpirationDate           = "BadExpirationDate"
	ReasonBadMessageID                = "BadMessageId"
	ReasonBadPriority                 = "BadPriority"
	ReasonBadTopic                    = "BadTopic"
	ReasonDeviceTokenNotForTopic      = "DeviceTokenNotForTopic" // #nosec G101 -- APNs reason string, not a credential
	ReasonDuplicateHeaders            = "DuplicateHeaders"
	ReasonIdleTimeout                 = "IdleTimeout"
	ReasonInvalidPushType             = "InvalidPushType"
	ReasonMissingDeviceToken          = "MissingDeviceToken" // #nosec G101 -- APNs reason string, not a credential
	ReasonMissingTopic                = "MissingTopic"
	ReasonPayloadEmpty                = "PayloadEmpty"
	ReasonTopicDisallowed             = "TopicDisallowed"
	ReasonBadCertificate              = "BadCertificate"
	ReasonBadCertificateEnvironment   = "BadCertificateEnvironment"
	ReasonExpiredProviderToken        = "ExpiredProviderToken" // #nosec G101 -- APNs reason string, not a credential
	ReasonForbidden                   = "Forbidden"
	ReasonInvalidProviderToken        = "InvalidProviderToken"       // #nosec G101 -- APNs reason string, not a credential
	ReasonMissingProviderToken        = "MissingProviderToken"       // #nosec G101 -- APNs reason string, not a credential
	ReasonUnrelatedKeyIDInToken       = "UnrelatedKeyIdInToken"      // #nosec G101 -- APNs reason string, not a credential
	ReasonBadEnvironmentKeyIDInToken  = "BadEnvironmentKeyIdInToken" // #nosec G101 -- APNs reason string, not a credential
	ReasonBadPath                     = "BadPath"
	ReasonMethodNotAllowed            = "MethodNotAllowed"
	ReasonExpiredToken                = "ExpiredToken" // #nosec G101 -- APNs reason string, not a credential
	ReasonUnregistered                = "Unregistered"
	ReasonPayloadTooLarge             = "PayloadTooLarge"
	ReasonTooManyProviderTokenUpdates = "TooManyProviderTokenUpdates" // #nosec G101 -- APNs reason string, not a credential
	ReasonTooManyRequests             = "TooManyRequests"
	ReasonInternalServerError         = "InternalServerError"
	ReasonServiceUnavailable          = "ServiceUnavailable"
	ReasonShutdown                    = "Shutdown"
)

// ReasonInfo is one reason string as Apple documents it.
type ReasonInfo struct {
	// Code is the wire value of the reason field, matching push.Result.Reason.
	Code string
	// Status is the HTTP status Apple pairs the reason with.
	Status int
	// Description is Apple's prose for the failure.
	Description string
}

// Reasons maps every reason string Apple documents to its meaning. A reason
// absent from the map is one APNs is not documented to send, which makes the
// map the bound on the vocabulary for a caller labelling by reason.
var Reasons = map[string]ReasonInfo{
	ReasonBadCollapseID:               {ReasonBadCollapseID, http.StatusBadRequest, "The collapse identifier exceeds the maximum allowed size."},
	ReasonBadDeviceToken:              {ReasonBadDeviceToken, http.StatusBadRequest, "The specified device token is invalid. Verify that the request contains a valid token and that the token matches the environment."},
	ReasonBadExpirationDate:           {ReasonBadExpirationDate, http.StatusBadRequest, "The apns-expiration value is invalid."},
	ReasonBadMessageID:                {ReasonBadMessageID, http.StatusBadRequest, "The apns-id value is invalid."},
	ReasonBadPriority:                 {ReasonBadPriority, http.StatusBadRequest, "The apns-priority value is invalid."},
	ReasonBadTopic:                    {ReasonBadTopic, http.StatusBadRequest, "The apns-topic value is invalid."},
	ReasonDeviceTokenNotForTopic:      {ReasonDeviceTokenNotForTopic, http.StatusBadRequest, "The device token doesn't match the specified topic."},
	ReasonDuplicateHeaders:            {ReasonDuplicateHeaders, http.StatusBadRequest, "One or more headers are repeated."},
	ReasonIdleTimeout:                 {ReasonIdleTimeout, http.StatusBadRequest, "Idle timeout."},
	ReasonInvalidPushType:             {ReasonInvalidPushType, http.StatusBadRequest, "The apns-push-type value is invalid."},
	ReasonMissingDeviceToken:          {ReasonMissingDeviceToken, http.StatusBadRequest, "The device token isn't specified in the request :path. Verify that the :path header contains the device token."},
	ReasonMissingTopic:                {ReasonMissingTopic, http.StatusBadRequest, "The apns-topic header of the request isn't specified and is required."},
	ReasonPayloadEmpty:                {ReasonPayloadEmpty, http.StatusBadRequest, "The message payload is empty."},
	ReasonTopicDisallowed:             {ReasonTopicDisallowed, http.StatusBadRequest, "Pushing to this topic is not allowed."},
	ReasonBadCertificate:              {ReasonBadCertificate, http.StatusForbidden, "The certificate is invalid."},
	ReasonBadCertificateEnvironment:   {ReasonBadCertificateEnvironment, http.StatusForbidden, "The client certificate doesn't match the environment."},
	ReasonExpiredProviderToken:        {ReasonExpiredProviderToken, http.StatusForbidden, "The provider token is stale and a new token should be generated."},
	ReasonForbidden:                   {ReasonForbidden, http.StatusForbidden, "The specified action is not allowed."},
	ReasonInvalidProviderToken:        {ReasonInvalidProviderToken, http.StatusForbidden, "The provider token is not valid, or the token signature can't be verified."},
	ReasonMissingProviderToken:        {ReasonMissingProviderToken, http.StatusForbidden, "No provider certificate was used to connect to APNs, and the authorization header is missing or no provider token is specified."},
	ReasonUnrelatedKeyIDInToken:       {ReasonUnrelatedKeyIDInToken, http.StatusForbidden, "The key ID in the provider token isn't related to the key ID of the token used in the first push of this connection. To use this token, open a new connection."},
	ReasonBadEnvironmentKeyIDInToken:  {ReasonBadEnvironmentKeyIDInToken, http.StatusForbidden, "The key ID in the provider token doesn't match the environment."},
	ReasonBadPath:                     {ReasonBadPath, http.StatusNotFound, "The request contained an invalid :path value."},
	ReasonMethodNotAllowed:            {ReasonMethodNotAllowed, http.StatusMethodNotAllowed, "The specified :method value isn't POST."},
	ReasonExpiredToken:                {ReasonExpiredToken, http.StatusGone, "The device token has expired."},
	ReasonUnregistered:                {ReasonUnregistered, http.StatusGone, "The device token is inactive for the specified topic."},
	ReasonPayloadTooLarge:             {ReasonPayloadTooLarge, http.StatusRequestEntityTooLarge, "The message payload is too large."},
	ReasonTooManyProviderTokenUpdates: {ReasonTooManyProviderTokenUpdates, http.StatusTooManyRequests, "The provider's authentication token is being updated too often. Update the authentication token no more than once every 20 minutes."},
	ReasonTooManyRequests:             {ReasonTooManyRequests, http.StatusTooManyRequests, "Too many requests were made consecutively to the same device token."},
	ReasonInternalServerError:         {ReasonInternalServerError, http.StatusInternalServerError, "An internal server error occurred."},
	ReasonServiceUnavailable:          {ReasonServiceUnavailable, http.StatusServiceUnavailable, "The service is unavailable."},
	ReasonShutdown:                    {ReasonShutdown, http.StatusServiceUnavailable, "The APNs server is shutting down."},
}

// ReasonCodes returns the documented reason strings in sorted order.
func ReasonCodes() []string {
	out := make([]string, 0, len(Reasons))
	for code := range Reasons {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}
