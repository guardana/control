// Package operators declares the two ports that turn a signed statement into
// an operator's authority: the authenticator says which roster key signed it
// and who that key belongs to, and the authorizer says whether that key's
// grants let it do the act over one tenant's one project. The default kind of
// each reads the trust roster port; an authorizer may be followed by a veto,
// which can only take a permission away.
//
// # The statement
//
// An operator signs a DSSE envelope whose payload type is the act's own, one
// signature, whose body is a canonical JSON object holding at least "alg" and
// "keyId": the algorithm and the id of the key that signed it. Both are
// compared with the roster's key after the signature verifies under the
// algorithm that key carries, so a statement naming another algorithm or key
// is refused and never selects how it is verified. The other members are the
// consumer's to read.
package operators
