package version

// APIVersion is the major version of the HTTP API *contract* (request/response
// shapes, route semantics) — distinct from Version, which is the build/app
// version. Bump it only on a breaking change to the contract.
//
// Clients and the server ship separately (a mobile app in the store can't be
// force-updated in lock-step with the server), so version skew is negotiated:
// the server advertises APIVersion (response header + GET /version) and refuses
// clients older than MinClientAPIVersion.
const APIVersion = 1

// MinClientAPIVersion is the oldest client API version this server still
// accepts. A client advertising an older X-Hygur-API is asked to upgrade (426).
// Keep == APIVersion until a deprecation window genuinely requires widening it.
const MinClientAPIVersion = 1
