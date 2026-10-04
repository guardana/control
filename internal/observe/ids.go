package observe

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"google.golang.org/protobuf/proto"
)

// observationIDPrefix and runIDPrefix are the fixed heads of the two ids.
const (
	observationIDPrefix = "obs-"
	runIDPrefix         = "run-"
)

// idDomain separates this hash from every other SHA-256 the product takes.
// It names no product and no package version, so neither a rename nor a
// later version of the contract changes an id a log already holds.
const idDomain = "observation-id:1"

// ObservationID derives an observation's id from its key: "obs-" and the first
// 32 lowercase hex digits of SHA-256 over the domain and the five fields, each
// preceded by its length as eight big-endian bytes, so no byte can move from
// one field into the next without changing the id.
func ObservationID(tenant, project, sourceID, traceID, spanID string) string {
	h := sha256.New()
	var n [8]byte
	for _, s := range []string{idDomain, tenant, project, sourceID, traceID, spanID} {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return observationIDPrefix + hex.EncodeToString(h.Sum(nil)[:16])
}

// ValidTraceID reports 32 lowercase hex digits, not all zeros, as the
// gateway's envelope spells a trace id.
func ValidTraceID(s string) bool { return nonZeroHex(s, 32) }

// ValidSpanID reports 16 lowercase hex digits, not all zeros.
func ValidSpanID(s string) bool { return nonZeroHex(s, 16) }

// ValidRunID reports the form of an opened run's id: "run-" and 32 lowercase
// hex digits. It says nothing about whether that run exists.
func ValidRunID(s string) bool {
	rest, ok := strings.CutPrefix(s, runIDPrefix)
	return ok && lowerHex(rest, 32)
}

func validObservationID(s string) bool {
	rest, ok := strings.CutPrefix(s, observationIDPrefix)
	return ok && lowerHex(rest, 32)
}

func nonZeroHex(s string, n int) bool {
	return lowerHex(s, n) && strings.Trim(s, "0") != ""
}

func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ContentDigest is the lowercase hex SHA-256 of o's deterministic Protobuf
// encoding with received_time and source.descriptor_sha256 cleared: two
// observations with one digest are the same testimony read twice. It returns
// "" for a nil observation and for one that does not encode, and "" is never a
// digest, so a caller comparing digests must refuse it rather than match it.
func ContentDigest(o *observev1.Observation) string {
	if o == nil {
		return ""
	}
	c, ok := proto.Clone(o).(*observev1.Observation)
	if !ok {
		return ""
	}
	c.ReceivedTime = nil
	if c.Source != nil {
		c.Source.DescriptorSha256 = ""
	}
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(c)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
