// Package correlation explicitly attaches Apple organization-device evidence to
// authenticated native Device Enrollment reports by serial, without persistence.
//
// Correlate consumes an actual collection.Resource and caller-authenticated
// successful native reports. It never authenticates a report: the host must bind
// the reported serial and enrollment method to its authenticated protocol exchange.
// User Enrollment cannot receive serial-based hardware enrichment. Opaque Apple
// IDs and enrollment/channel IDs only scope provenance, never physical matching.
//
// Resolve is optional. Callers specify every source field that has equivalent
// meaning and units; the package supplies no semantic aliases or unit conversions.
// The latest successful native field wins even when Apple refreshed more recently.
// Reports are retained independently, so omission retains earlier evidence, while
// null, false, zero and empty arrays are explicit values. Results include original
// bytes and provenance, including disagreements. Callers own storage, retention,
// authentication and policy; no inventory repository or server is required.
//
// # References
//
// Apple organization-device attributes:
// https://developer.apple.com/documentation/applebusinessapi/orgdevice/attributes-data.dictionary
package correlation
