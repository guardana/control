// Package trust is the trust roster port: a roster signed under one pinned
// root key names each key a plane trusts, its algorithm, its role, its
// subject and its grants, and expires. The default kind reads one roster file
// and keeps its serial on an authority floor, and holds the last roster it
// verified until that roster's own expiry, then answers unavailable.
//
// # The roster
//
// A roster is a DSSE envelope of PayloadType, one signature by the root,
// whose body is the canonical JSON object
//
//	{"kind":"trust-roster/v1alpha1","rosterId":<id>,"serial":<n>,
//	 "issuedAt":<t>,"expiresAt":<t>,"keys":[<key>...]}
//
// with every member required and no other. A key is {"keyId","alg","role",
// "subject","publicKey","grants"}: alg is "ed25519", role is "operator",
// keyId is the key's own id, and grants are one to MaxGrants
// {"tenant","project","level"} with level read, operate or admin. A tenant
// or project is never empty and never a pattern. The lifetime is at most
// MaxLifetime. No key is named twice, and none is the root or a key the
// plane pins for another authority.
//
// Nothing here decides what a key may do: the operators package does, from
// the grants a Key carries.
package trust
