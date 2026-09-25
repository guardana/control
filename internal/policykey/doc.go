// Package policykey holds a policy authority's key on disk and signs with it:
// the private key file and the one reader that judges it, the public key line
// and its one parser, the key id derived from the public half, and the signed
// bundle file. ADR-0018 fixes each format; the approver's binary makes keys and
// bundles through it, and the gateway reads only the public line.
package policykey
