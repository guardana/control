package main

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
)

// printableLog wraps a handler so that the message and every value of a log
// line reach it as oneLine gives them. What the plane logs quotes files,
// requests and errors, any of which may hold key text.
func printableLog(h slog.Handler) slog.Handler { return printableHandler{h} }

type printableHandler struct{ next slog.Handler }

func (h printableHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h printableHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, oneLine(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(printableAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h printableHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return printableHandler{h.next.WithAttrs(printableAttrs(attrs))}
}

func (h printableHandler) WithGroup(name string) slog.Handler {
	return printableHandler{h.next.WithGroup(oneLine(name))}
}

func printableAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = printableAttr(a)
	}
	return out
}

// printableAttr passes the key, a string, a byte slice or array as the text
// it holds, and any other value as the text handler would format it, an
// error or a []string among them, through oneLine. A number, a time or a
// duration holds no text and stays as it is.
func printableAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		v = slog.StringValue(oneLine(v.String()))
	case slog.KindGroup:
		v = slog.GroupValue(printableAttrs(v.Group())...)
	case slog.KindAny:
		v = slog.StringValue(oneLine(anyText(v.Any())))
	}
	return slog.Attr{Key: oneLine(a.Key), Value: v}
}

// anyText is the bytes of a byte slice or array as text, and any other value
// as %+v formats it. A byte type that formats itself keeps its own text,
// which %+v gives; formatted by kind alone its bytes would come out as
// decimal numbers, which no key text check can read.
func anyText(x any) string {
	switch x.(type) {
	case fmt.Formatter, fmt.Stringer, error:
		return fmt.Sprintf("%+v", x)
	}
	r := reflect.ValueOf(x)
	if (r.Kind() == reflect.Slice || r.Kind() == reflect.Array) && r.Type().Elem() == reflect.TypeFor[byte]() {
		b := make([]byte, r.Len())
		reflect.Copy(reflect.ValueOf(b), r)
		return string(b)
	}
	return fmt.Sprintf("%+v", x)
}
