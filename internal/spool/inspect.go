package spool

import "errors"

// Inspection is what Inspect read of a spool's directory: the log as Open
// would leave it, and what Open would cut to get there.
type Inspection struct {
	// Held is a directory a spool holds. Its holder cut the torn tails when
	// it opened and appends past its last record, so nothing else is read and
	// every count below is zero; the holder's own Stats are the ones to ask.
	Held bool
	// Segments and Unacknowledged are the segments Open would keep and the
	// bytes of whole records in them, none of which a start knows to be
	// acknowledged.
	Segments       int
	Unacknowledged int64
	// QuarantinedRecords and QuarantinedBytes are the quarantine's whole
	// records.
	QuarantinedRecords int
	QuarantinedBytes   int64
	// Torn is the bytes Open would cut: past the last record that checks in
	// the last segment and in the quarantine, and the whole of a last segment
	// that holds no record.
	Torn int64
}

// Inspect reads the spool's directory as Open does and refuses what Open
// refuses, and changes nothing there: it takes no lock that outlives it,
// cuts no tail and creates no file. Where the directory is locked with flock,
// a shared lock keeps a spool from opening it while the read lasts, so the
// read is of one log.
func Inspect(opts Options) (_ Inspection, err error) {
	if err := opts.check(); err != nil {
		return Inspection{}, err
	}
	if opts.openRead == nil {
		opts.openRead = openOSRead
	}
	root, err := openSpoolDir(opts.Dir)
	if err != nil {
		return Inspection{}, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	release, held, err := shareDir(root, opts.Dir)
	if err != nil || held {
		return Inspection{Held: held}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	s := &Spool{opts: opts, root: root}
	sizes, err := s.read()
	if err != nil {
		return Inspection{}, err
	}
	found := Inspection{
		QuarantinedRecords: s.quarantine.records,
		QuarantinedBytes:   s.quarantine.size,
		Torn:               sizes.quarantine - s.quarantine.size,
	}
	segments := s.segments
	if n := len(segments); n > 0 {
		last := segments[n-1]
		found.Torn += sizes.last - last.size
		if last.records == 0 {
			segments = segments[:n-1]
		}
	}
	for _, seg := range segments {
		found.Segments++
		found.Unacknowledged += seg.size
	}
	return found, nil
}
