package otelgenai

// The OTLP 1.11.1 trace messages in their JSON encoding, lowerCamelCase
// only. A member the importer never uses is still read for its type, so a
// line is refused or taken whole.

type tracesData struct{ resources []*resourceSpans }

// The attributes of scopes, events and links are kept only as the tally of
// the content they held, since the importer copies none of them.
type resourceSpans struct {
	attrs  attrList
	scopes tally
	spans  []*span
}

type scopeSpans struct {
	attrs tally
	spans []*span
}

type span struct {
	traceID, spanID, parentSpanID string
	end                           uint64
	attrs                         attrList
	events, links                 tally
	statusCode                    int64
}

// attrList is what the importer keeps of one attribute list: the values of
// the keys it may copy, and a tally of every content key the list held,
// copied ones included.
type attrList struct {
	kept    []keyValue
	content tally
}

type valueKind uint8

const (
	kindEmpty valueKind = iota
	kindString
	kindBool
	kindInt
	kindDouble
	kindBytes
	kindArray
	kindKVList
)

// anyValue keeps only what the importer reads: a string, and the elements of
// an array or a key-value list where message parts live, which is under a
// messages attribute only.
type anyValue struct {
	kind   valueKind
	str    string
	array  []anyValue
	kvlist []keyValue
}

type keyValue struct {
	key   string
	value anyValue
}

type nothing struct{}

var tracesDataMembers = map[string]member[tracesData]{
	"resourceSpans": func(r *reader, td *tracesData) error {
		return r.array(func() error {
			rs := &resourceSpans{}
			err := decode(r, resourceSpansMembers, rs)
			td.resources = append(td.resources, rs)
			return err
		})
	},
}

var resourceSpansMembers = map[string]member[resourceSpans]{
	"resource": func(r *reader, rs *resourceSpans) error {
		return decode(r, resourceMembers, rs)
	},
	"scopeSpans": func(r *reader, rs *resourceSpans) error {
		return r.array(func() error {
			var ss scopeSpans
			err := decode(r, scopeSpansMembers, &ss)
			rs.spans = append(rs.spans, ss.spans...)
			rs.scopes.add(ss.attrs)
			return err
		})
	},
	"schemaUrl": skipString[resourceSpans],
}

var resourceMembers = map[string]member[resourceSpans]{
	"attributes": func(r *reader, rs *resourceSpans) (err error) {
		rs.attrs, err = r.attributes()
		return err
	},
	"droppedAttributesCount": skipUint32[resourceSpans],
	"entityRefs": func(r *reader, _ *resourceSpans) error {
		return r.array(func() error { return decode(r, entityRefMembers, &nothing{}) })
	},
}

var entityRefMembers = map[string]member[nothing]{
	"schemaUrl":       skipString[nothing],
	"type":            skipString[nothing],
	"idKeys":          skipStrings[nothing],
	"descriptionKeys": skipStrings[nothing],
}

var scopeSpansMembers = map[string]member[scopeSpans]{
	"scope": func(r *reader, ss *scopeSpans) error { return decode(r, scopeMembers, ss) },
	"spans": func(r *reader, ss *scopeSpans) error {
		return r.array(func() error {
			s := &span{}
			err := decode(r, spanMembers, s)
			ss.spans = append(ss.spans, s)
			return err
		})
	},
	"schemaUrl": skipString[scopeSpans],
}

var scopeMembers = map[string]member[scopeSpans]{
	"name":                   skipString[scopeSpans],
	"version":                skipString[scopeSpans],
	"attributes":             func(r *reader, ss *scopeSpans) error { return r.tallied(&ss.attrs) },
	"droppedAttributesCount": skipUint32[scopeSpans],
}

var spanMembers = map[string]member[span]{
	"traceId":           func(r *reader, s *span) (err error) { s.traceID, err = r.str(); return err },
	"spanId":            func(r *reader, s *span) (err error) { s.spanID, err = r.str(); return err },
	"traceState":        skipString[span],
	"parentSpanId":      func(r *reader, s *span) (err error) { s.parentSpanID, err = r.str(); return err },
	"flags":             skipUint32[span],
	"name":              skipString[span],
	"kind":              skipEnum[span],
	"startTimeUnixNano": skipUint64[span],
	"endTimeUnixNano":   func(r *reader, s *span) (err error) { s.end, err = r.uint(64); return err },
	"attributes":        func(r *reader, s *span) (err error) { s.attrs, err = r.attributes(); return err },
	"events": func(r *reader, s *span) error {
		return r.array(func() error { return decode(r, eventMembers, &s.events) })
	},
	"links": func(r *reader, s *span) error {
		return r.array(func() error { return decode(r, linkMembers, &s.links) })
	},
	"status":                 func(r *reader, s *span) error { return decode(r, statusMembers, s) },
	"droppedAttributesCount": skipUint32[span],
	"droppedEventsCount":     skipUint32[span],
	"droppedLinksCount":      skipUint32[span],
}

var eventMembers = map[string]member[tally]{
	"timeUnixNano":           skipUint64[tally],
	"name":                   skipString[tally],
	"attributes":             (*reader).tallied,
	"droppedAttributesCount": skipUint32[tally],
}

var linkMembers = map[string]member[tally]{
	"traceId":                skipString[tally],
	"spanId":                 skipString[tally],
	"traceState":             skipString[tally],
	"attributes":             (*reader).tallied,
	"droppedAttributesCount": skipUint32[tally],
	"flags":                  skipUint32[tally],
}

var statusMembers = map[string]member[span]{
	"message": skipString[span],
	"code":    func(r *reader, s *span) (err error) { s.statusCode, err = r.enum(); return err },
}

// attributes reads one attribute list and keeps of it what attrList says;
// a messages value is parsed for its reasoning parts as soon as it is read.
func (r *reader) attributes() (attrList, error) {
	var l attrList
	var seen names
	err := r.array(func() error {
		kv, err := r.keyValue(0, true)
		if err != nil {
			return err
		}
		if !seen.add(kv.key) {
			return errKey
		}
		if copied(kv.key, r.runKey) {
			l.kept = append(l.kept, kv)
		}
		l.content.count(&kv, r.parts)
		return nil
	})
	return l, err
}

// tallied reads an attribute list of which only the content tally is kept,
// adding it to t.
func (r *reader) tallied(t *tally) error {
	l, err := r.attributes()
	t.add(l.content)
	return err
}

// keyValues reads the key-values of a value nested in an attribute and
// refuses a key it holds twice; they are kept only when keep says so.
func (r *reader) keyValues(depth int, keep bool) ([]keyValue, error) {
	var out []keyValue
	var seen names
	err := r.array(func() error {
		kv, err := r.keyValue(depth, keep)
		if err != nil {
			return err
		}
		if !seen.add(kv.key) {
			return errKey
		}
		if keep {
			out = append(out, kv)
		}
		return nil
	})
	return out, err
}

// keyValue reads one key-value. At depth 0 the key decides whether the
// value's children are kept; a value read before its key is kept until the
// key is known.
func (r *reader) keyValue(depth int, keep bool) (keyValue, error) {
	var kv keyValue
	named := false
	err := r.object(func(name string) (err error) {
		switch name {
		case "key":
			kv.key, err = r.str()
			named = true
		case "value":
			kv.value, err = r.anyValue(depth+1, keep && (depth > 0 || !named || messagesKey(kv.key)))
		default:
			err = errMember
		}
		return err
	})
	if depth == 0 && !messagesKey(kv.key) {
		kv.value.array, kv.value.kvlist = nil, nil
	}
	return kv, err
}

func (r *reader) anyValue(depth int, keep bool) (anyValue, error) {
	var v anyValue
	if depth > maxValueDepth {
		return v, errDepth
	}
	err := r.object(func(name string) error {
		if v.kind != kindEmpty {
			return errTwoValues
		}
		return r.valueMember(name, &v, depth, keep)
	})
	return v, err
}

func (r *reader) valueMember(name string, v *anyValue, depth int, keep bool) (err error) {
	switch name {
	case "stringValue":
		v.kind = kindString
		v.str, err = r.str()
	case "boolValue":
		v.kind = kindBool
		err = r.boolean()
	case "intValue":
		v.kind = kindInt
		_, err = r.int(64, false)
	case "doubleValue":
		v.kind = kindDouble
		err = r.double()
	case "bytesValue":
		v.kind = kindBytes
		err = r.base64()
	case "arrayValue":
		v.kind = kindArray
		err = r.object(r.valuesOf(func() error { return r.arrayValues(v, depth, keep) }))
	case "kvlistValue":
		v.kind = kindKVList
		err = r.object(r.valuesOf(func() error { return r.kvlistValues(v, depth, keep) }))
	default:
		err = errMember
	}
	return err
}

func (r *reader) arrayValues(v *anyValue, depth int, keep bool) error {
	return r.array(func() error {
		e, err := r.anyValue(depth+1, keep)
		if keep {
			v.array = append(v.array, e)
		}
		return err
	})
}

func (r *reader) kvlistValues(v *anyValue, depth int, keep bool) error {
	kvs, err := r.keyValues(depth, keep)
	v.kvlist = kvs
	return err
}

// valuesOf reads the one member of ArrayValue and KeyValueList.
func (r *reader) valuesOf(read func() error) func(name string) error {
	return func(name string) error {
		if name != "values" {
			return errMember
		}
		return read()
	}
}
