// Package api defines data types representing core collectd data types.
package api // import "collectd.org/api"

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"collectd.org/meta"
	"go.uber.org/multierr"
)

// Value represents either a Gauge or a Derive. It is Go's equivalent to the C
// union value_t. If a function accepts a Value, you may pass in either a Gauge
// or a Derive. Passing in any other type may or may not panic.
type Value interface {
	Type() string
}

// Gauge represents a gauge metric value, such as a temperature.
// This is Go's equivalent to the C type "gauge_t".
type Gauge float64

// Type returns "gauge".
func (v Gauge) Type() string { return "gauge" }

// Derive represents a counter metric value, such as bytes sent over the
// network. When the counter wraps around (overflows) or is reset, this is
// interpreted as a (huge) negative rate, which is discarded.
// This is Go's equivalent to the C type "derive_t".
type Derive int64

// Type returns "derive".
func (v Derive) Type() string { return "derive" }

// Counter represents a counter metric value, such as bytes sent over the
// network. When a counter value is smaller than the previous value, a wrap
// around (overflow) is assumed. This causes huge spikes in case a counter is
// reset. Only use Counter for very specific cases. If in doubt, use Derive
// instead.
// This is Go's equivalent to the C type "counter_t".
type Counter uint64

// Type returns "counter".
func (v Counter) Type() string { return "counter" }

// Identifier identifies one metric.
type Identifier struct {
	Host                   string
	Plugin, PluginInstance string
	Type, TypeInstance     string
}

// ParseIdentifier parses the identifier encoded in s and returns it.
func ParseIdentifier(s string) (Identifier, error) {
	fields := strings.Split(s, "/")
	if len(fields) != 3 {
		return Identifier{}, fmt.Errorf("not a valid identifier: %q", s)
	}

	id := Identifier{
		Host:   fields[0],
		Plugin: fields[1],
		Type:   fields[2],
	}

	if i := strings.Index(id.Plugin, "-"); i != -1 {
		id.PluginInstance = id.Plugin[i+1:]
		id.Plugin = id.Plugin[:i]
	}

	if i := strings.Index(id.Type, "-"); i != -1 {
		id.TypeInstance = id.Type[i+1:]
		id.Type = id.Type[:i]
	}

	return id, nil
}

// ValueList represents one (set of) data point(s) of one metric. It is Go's
// equivalent of the C type value_list_t.
type ValueList struct {
	Identifier
	Time     time.Time
	Interval time.Duration
	Values   []Value
	DSNames  []string
	Meta     meta.Data
}

// DSName returns the name of the data source at the given index. If vl.DSNames
// is nil, returns "value" if there is a single value and a string
// representation of index otherwise.
func (vl *ValueList) DSName(index int) string {
	if vl.DSNames != nil {
		return vl.DSNames[index]
	} else if len(vl.Values) != 1 {
		return strconv.FormatInt(int64(index), 10)
	}

	return "value"
}

// Clone returns a copy of vl.
// Unfortunately, many functions expect a pointer to a value list. If the
// original value list must not be modified, it may be necessary to create and
// pass a copy. This is what this method helps to do.
func (vl *ValueList) Clone() *ValueList {
	if vl == nil {
		return nil
	}

	vlCopy := *vl

	vlCopy.Values = make([]Value, len(vl.Values))
	copy(vlCopy.Values, vl.Values)

	vlCopy.DSNames = make([]string, len(vl.DSNames))
	copy(vlCopy.DSNames, vl.DSNames)

	vlCopy.Meta = vl.Meta.Clone()

	return &vlCopy
}

// Writer are objects accepting a ValueList for writing, for example to the
// network.
type Writer interface {
	Write(context.Context, *ValueList) error
}

// WriterFunc implements the Writer interface based on a wrapped function.
type WriterFunc func(context.Context, *ValueList) error

// Write calls the wrapped function.
func (f WriterFunc) Write(ctx context.Context, vl *ValueList) error {
	return f(ctx, vl)
}

// String returns a string representation of the Identifier.
func (id Identifier) String() string {
	str := id.Host + "/" + id.Plugin
	if id.PluginInstance != "" {
		str += "-" + id.PluginInstance
	}
	str += "/" + id.Type
	if id.TypeInstance != "" {
		str += "-" + id.TypeInstance
	}
	return str
}

// Fanout implements a multiplexer for Writer, i.e. each ValueList written to
// it is copied and written to each Writer.
type Fanout []Writer

// Write writes the value list to each writer. Each writer receives a copy of
// the value list to avoid writers interfering with one another. Writers are
// executed concurrently. Write blocks until all writers have returned and
// returns an error containing all errors returned by writers.
//
// If the context is canceled, Write returns an error immediately. Since it may
// return before all writers have finished, the returned error may not contain
// the error of all writers.
func (f Fanout) Write(ctx context.Context, vl *ValueList) error {
	var (
		ch = make(chan error)
		wg sync.WaitGroup
	)

	for _, w := range f {
		wg.Add(1)
		go func(w Writer) {
			defer wg.Done()

			if err := w.Write(ctx, vl.Clone()); err != nil {
				// block until the error is read, or until the
				// context is canceled.
				select {
				case ch <- fmt.Errorf("%T.Write(): %w", w, err):
				case <-ctx.Done():
				}
			}
		}(w)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var errs error
	for {
		select {
		case err, ok := <-ch:
			if !ok {
				// channel closed, all goroutines done
				return errs
			}
			errs = multierr.Append(errs, err)
		case <-ctx.Done():
			return multierr.Append(errs, ctx.Err())
		}
	}
}
