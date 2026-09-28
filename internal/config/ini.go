// Package config reads and writes llamaenv's INI files.
package config

import (
	"fmt"
	"strings"
)

// File is an INI file that keeps comments and blank lines, so a command that
// changes one value leaves the rest of a hand-edited file as it was.
type File struct {
	// sections[0] holds the keys before the first [section] header.
	sections []*Section
}

// Section is one [name] block. The top section has an empty name.
type Section struct {
	Name  string
	lines []line
}

type line struct {
	raw   string // original text, kept for comments and blank lines
	key   string // empty for comments and blank lines
	value string
}

// ParseINI reads INI text: "[section]" headers and "key = value" lines.
// Lines that start with ";" or "#" are comments.
func ParseINI(text string) (*File, error) {
	f := &File{sections: []*Section{{}}}
	cur := f.sections[0]
	text = strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if text == "" {
		return f, nil
	}
	for i, raw := range strings.Split(text, "\n") {
		next, err := f.parseLine(cur, raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		cur = next
	}
	return f, nil
}

// parseLine adds one line to the current section, or starts a new section,
// and returns the section that the next line belongs to.
func (f *File) parseLine(cur *Section, raw string) (*Section, error) {
	t := strings.TrimSpace(raw)
	switch {
	case t == "" || strings.HasPrefix(t, ";") || strings.HasPrefix(t, "#"):
		cur.lines = append(cur.lines, line{raw: raw})
	case strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]"):
		cur = &Section{Name: strings.TrimSpace(t[1 : len(t)-1])}
		f.sections = append(f.sections, cur)
	default:
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			return nil, fmt.Errorf("expected \"key = value\" or \"[section]\": %q", t)
		}
		cur.lines = append(cur.lines, line{raw: raw, key: strings.TrimSpace(k), value: strings.TrimSpace(v)})
	}
	return cur, nil
}

// String renders the file. Unchanged lines keep their original text.
func (f *File) String() string {
	var b strings.Builder
	for i, s := range f.sections {
		if i > 0 {
			fmt.Fprintf(&b, "[%s]\n", s.Name)
		}
		for _, l := range s.lines {
			b.WriteString(l.raw)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// Sections returns the named sections in file order.
func (f *File) Sections() []*Section { return f.sections[1:] }

// Section returns the section with this name (case-insensitive), or nil.
// The empty name is the top section.
func (f *File) Section(name string) *Section {
	for _, s := range f.sections {
		if strings.EqualFold(s.Name, name) {
			return s
		}
	}
	return nil
}

// Get returns a value, or "" and false.
func (f *File) Get(section, key string) (string, bool) {
	if s := f.Section(section); s != nil {
		return s.Get(key)
	}
	return "", false
}

// Set sets a value, adding the section or key when missing.
func (f *File) Set(section, key, value string) {
	s := f.Section(section)
	if s == nil {
		s = f.addSection(section)
	}
	s.Set(key, value)
}

// DeleteSection removes a named section and reports whether it existed.
func (f *File) DeleteSection(name string) bool {
	for i, s := range f.sections {
		if i > 0 && strings.EqualFold(s.Name, name) {
			f.sections = append(f.sections[:i], f.sections[i+1:]...)
			return true
		}
	}
	return false
}

// Get returns a value, or "" and false.
func (s *Section) Get(key string) (string, bool) {
	for _, l := range s.lines {
		if l.key != "" && strings.EqualFold(l.key, key) {
			return l.value, true
		}
	}
	return "", false
}

// Set replaces the value of a key, or appends the key.
func (s *Section) Set(key, value string) {
	l := line{raw: key + " = " + value, key: key, value: value}
	for i, old := range s.lines {
		if old.key != "" && strings.EqualFold(old.key, key) {
			s.lines[i] = l
			return
		}
	}
	s.insert(l)
}

// insert adds a line before trailing blank lines, so sections stay separated.
func (s *Section) insert(l line) {
	at := len(s.lines)
	for at > 0 && s.lines[at-1].key == "" && strings.TrimSpace(s.lines[at-1].raw) == "" {
		at--
	}
	s.lines = append(s.lines[:at], append([]line{l}, s.lines[at:]...)...)
}

// Delete removes a key and reports whether it existed.
func (s *Section) Delete(key string) bool {
	for i, l := range s.lines {
		if l.key != "" && strings.EqualFold(l.key, key) {
			s.lines = append(s.lines[:i], s.lines[i+1:]...)
			return true
		}
	}
	return false
}

// Keys returns the keys and values in order.
func (s *Section) Keys() [][2]string {
	var kv [][2]string
	for _, l := range s.lines {
		if l.key != "" {
			kv = append(kv, [2]string{l.key, l.value})
		}
	}
	return kv
}
