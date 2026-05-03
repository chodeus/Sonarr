// Package configfile is the Go port of NzbDrone.Core.Configuration.
// ConfigFileProvider — the host-level XML settings file at
// <dataDir>/config.xml that holds port, SSL, API key, branch,
// Postgres connection details, log levels, and friends.
//
// Two providers ship in the C# tree: ConfigFileProvider (XML, host
// settings) and ConfigService (Config table, user settings). They
// are independent, share no cache, and are written separately. The
// settings UI saves to BOTH in the same request. The DB-backed
// ConfigService is already covered by `internal/config` (shipped
// earlier this Phase 1 batch); this package handles the XML half.
//
// # Precedence
//
// For each key the effective value is the first non-empty source in
// this chain (matches `_serverOptions.X ?? GetValue(...)` in the C#
// tree):
//
//  1. Override — typically env var or CLI flag, parsed by the host
//     binding layer and provided to the Provider as a struct.
//  2. XML — the value persisted in the config file (if present).
//  3. Default — the per-key default coded into the accessor.
//
// When the XML file has no value for a key and the accessor's
// `persist` flag is true (the C# default), the default is written to
// the XML on first read. `persist=false` keeps the file untouched —
// this is how the C# code avoids polluting the XML with values that
// only ever come from env vars (Postgres credentials, log overrides).
//
// # Concurrency
//
// One process-wide mutex guards every read/write so concurrent reads
// of the same key never race the auto-persist write. This matches the
// C# `static readonly object Mutex = new object()` pattern. The
// mutex is held only across file I/O — accessors take and release it
// per call.
//
// # Atomic save
//
// CFG-Q9 in analysis/questions.md flags the C# SaveConfigDictionary
// as non-transactional: a partial write on power loss can leave the
// file in a corrupt half-state. The Go port writes via a temp file +
// rename so the file is either the old contents or the new contents,
// never an interrupted blend.
package configfile

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrInvalidConfigFile is returned by Load when the XML cannot be
// parsed, is empty, is all control characters, or doesn't have
// exactly one <Config> root element. Mirrors C#
// InvalidConfigFileException. Callers should typically present this
// as "delete the config file and Sonarr will recreate it."
var ErrInvalidConfigFile = errors.New("configfile: invalid")

// ConfigElementName is the XML root element. The C# tree exposes
// this as a public const so a few diagnostic paths can read it; we
// preserve the same name for clarity.
const ConfigElementName = "Config"

// xmlDoc carries the in-memory shape of one config file. Stored in
// canonical order — child element name lowered; insertion order
// preserved — so a fresh write of an unchanged file produces
// byte-equal output to the previous read (helpful for diff tooling).
type xmlDoc struct {
	// keyOrder preserves the sequence elements appeared in. Set
	// operations append to the end; existing keys keep their slot.
	keyOrder []string
	// values maps key → trimmed text content. The C# code calls
	// `.Value.Trim()` on every read, so we trim once at parse time and
	// store the trimmed form.
	values map[string]string
}

func newXMLDoc() *xmlDoc {
	return &xmlDoc{values: map[string]string{}}
}

func (d *xmlDoc) get(key string) (string, bool) {
	v, ok := d.values[key]
	return v, ok
}

func (d *xmlDoc) set(key, value string) (changed bool) {
	prev, existed := d.values[key]
	if existed && prev == value {
		return false
	}
	d.values[key] = value
	if !existed {
		d.keyOrder = append(d.keyOrder, key)
	}
	return true
}

func (d *xmlDoc) keys() []string {
	out := make([]string, 0, len(d.values))
	for _, k := range d.keyOrder {
		if _, ok := d.values[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// Loader owns one config file path. Goroutine-safe.
type Loader struct {
	mu   sync.Mutex
	path string
	doc  *xmlDoc
}

// NewLoader binds a Loader to the given absolute file path. The
// file does not need to exist yet; Provider.EnsureDefault will
// create it when called.
func NewLoader(path string) *Loader {
	return &Loader{path: path, doc: newXMLDoc()}
}

// Path returns the bound config file path.
func (l *Loader) Path() string { return l.path }

// Load reads the XML file from disk into the in-memory doc. Returns
// ErrInvalidConfigFile for empty / control-char-only / unparseable /
// missing-root content, matching C# InvalidConfigFileException
// behaviour. A missing file is NOT an error — Load on a missing
// file leaves the doc empty so the caller can EnsureDefault.
func (l *Loader) Load(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loadLocked(ctx)
}

func (l *Loader) loadLocked(_ context.Context) error {
	contents, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		l.doc = newXMLDoc()
		return nil
	}
	if err != nil {
		return fmt.Errorf("configfile: read %s: %w", l.path, err)
	}
	doc, err := parseXML(contents)
	if err != nil {
		return err
	}
	l.doc = doc
	return nil
}

// Save writes the in-memory doc to disk atomically (temp file +
// rename). Caller must hold whatever higher-level invariant they
// need — Save itself takes the loader mutex so concurrent Save
// calls serialize.
func (l *Loader) Save(_ context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.saveLocked()
}

func (l *Loader) saveLocked() error {
	out, err := encodeXML(l.doc)
	if err != nil {
		return fmt.Errorf("configfile: encode: %w", err)
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("configfile: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config.xml.*.tmp")
	if err != nil {
		return fmt.Errorf("configfile: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("configfile: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("configfile: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("configfile: close temp: %w", err)
	}
	if err := os.Rename(tmpName, l.path); err != nil {
		cleanup()
		return fmt.Errorf("configfile: rename %s → %s: %w", tmpName, l.path, err)
	}
	return nil
}

// parseXML decodes contents into an xmlDoc, applying the C# parity
// invariants: contents must not be empty, must not be all control
// characters, must contain exactly one <Config> root, every child of
// <Config> is treated as a single string-valued setting, text
// content is trimmed.
func parseXML(contents []byte) (*xmlDoc, error) {
	if len(contents) == 0 {
		return nil, fmt.Errorf("%w: empty", ErrInvalidConfigFile)
	}
	if isAllControl(contents) {
		return nil, fmt.Errorf("%w: control characters only", ErrInvalidConfigFile)
	}
	dec := xml.NewDecoder(strings.NewReader(string(contents)))
	doc := newXMLDoc()
	rootSeen := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidConfigFile, err.Error())
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != ConfigElementName {
			return nil, fmt.Errorf("%w: unexpected root %q", ErrInvalidConfigFile, start.Name.Local)
		}
		if rootSeen {
			return nil, fmt.Errorf("%w: more than one <Config> root", ErrInvalidConfigFile)
		}
		rootSeen = true
		if err := decodeConfigBody(dec, doc); err != nil {
			return nil, err
		}
	}
	if !rootSeen {
		return nil, fmt.Errorf("%w: missing <Config> root", ErrInvalidConfigFile)
	}
	return doc, nil
}

func decodeConfigBody(dec *xml.Decoder, doc *xmlDoc) error {
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: unexpected EOF inside <Config>", ErrInvalidConfigFile)
		}
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidConfigFile, err.Error())
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == ConfigElementName {
				return nil
			}
		case xml.StartElement:
			var inner struct {
				Body string `xml:",chardata"`
			}
			if err := dec.DecodeElement(&inner, &t); err != nil {
				return fmt.Errorf("%w: decode <%s>: %s", ErrInvalidConfigFile, t.Name.Local, err.Error())
			}
			doc.set(t.Name.Local, strings.TrimSpace(inner.Body))
		}
	}
}

// encodeXML emits the doc as UTF-8 XML in a shape compatible with
// the C# XDocument.ToString() output: declaration line, root
// <Config>, children sorted by insertion order, two-space indent,
// trailing newline. Children with empty values still emit (the C#
// path also emits empty <X></X>).
func encodeXML(doc *xmlDoc) ([]byte, error) {
	var b strings.Builder
	// XDocument writes `<?xml version="1.0" encoding="utf-8"?>` by
	// default; standalone="yes" only appears when XDeclaration sets it.
	// Match the C# ConfigFileProvider.LoadConfigFile fresh-doc shape.
	b.WriteString(`<?xml version="1.0" encoding="utf-8" standalone="yes"?>` + "\n")
	b.WriteString("<" + ConfigElementName + ">\n")
	for _, k := range doc.keys() {
		v, _ := doc.get(k)
		b.WriteString("  <")
		b.WriteString(k)
		b.WriteString(">")
		if err := xmlEscape(&b, v); err != nil {
			return nil, err
		}
		b.WriteString("</")
		b.WriteString(k)
		b.WriteString(">\n")
	}
	b.WriteString("</" + ConfigElementName + ">\n")
	return []byte(b.String()), nil
}

// xmlEscape mirrors xml.EscapeText but writes into the strings.Builder
// directly. Standard XML escapes: & < > ' " plus the control-char
// numeric entities encoding/xml emits.
func xmlEscape(b *strings.Builder, s string) error {
	return xml.EscapeText(stringBuilderWriter{b}, []byte(s))
}

type stringBuilderWriter struct{ b *strings.Builder }

func (w stringBuilderWriter) Write(p []byte) (int, error) {
	return w.b.Write(p)
}

// isAllControl reports whether every byte in p is an ASCII control
// character (0x00–0x1F or 0x7F). Mirrors `contents.All(char.IsControl)`
// in C#. We treat as control: 0x00..0x1F and 0x7F. Tab/LF/CR are
// included — the C# `char.IsControl` returns true for those too, so
// a file consisting only of whitespace control characters fails
// validation.
func isAllControl(p []byte) bool {
	for _, b := range p {
		if b > 0x1F && b != 0x7F {
			return false
		}
	}
	return true
}
