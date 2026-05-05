package marketplace

// VerifyConnector verifies the authenticity of a connector archive.
//
// V1: built-in connectors are always trusted (IsBuiltIn == true); this
// function is a no-op stub.
//
// V2 (future): Ed25519 signature verification of external Wasm archives
// using an embedded public key. The signature covers SHA-256(manifest||wasm).
func VerifyConnector(listing *ConnectorListing, _ []byte) error {
	if listing.IsBuiltIn {
		return nil
	}
	// TODO v2: ed25519.Verify(marketplacePubKey, sha256(archive), sig)
	return nil
}
