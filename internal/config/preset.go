package config

// OverlayPreset lays the llama.cpp preset "top" over the preset "base" and
// returns one preset. A key in top replaces the same key in the same section
// of base; everything else is kept as it is. Section and key names must match
// exactly, as llama.cpp compares them. The text is only copied: no key is
// checked, renamed, or added.
func OverlayPreset(base, top string) (string, error) {
	out, err := ParseINI(base)
	if err != nil {
		return "", err
	}
	over, err := ParseINI(top)
	if err != nil {
		return "", err
	}
	for i, s := range over.sections {
		dst := out.exactSection(s.Name)
		if dst == nil {
			if i == 0 {
				dst = out.sections[0]
			} else {
				dst = out.addSection(s.Name)
			}
		}
		for _, kv := range s.Keys() {
			dst.setExact(kv[0], kv[1])
		}
	}
	return out.String(), nil
}

func (f *File) exactSection(name string) *Section {
	for i, s := range f.sections {
		if s.Name == name && (i == 0) == (name == "") {
			return s
		}
	}
	return nil
}

func (f *File) addSection(name string) *Section {
	s := &Section{Name: name}
	if last := f.sections[len(f.sections)-1]; len(last.lines) > 0 && last.lines[len(last.lines)-1].raw != "" {
		last.lines = append(last.lines, line{})
	}
	f.sections = append(f.sections, s)
	return s
}

func (s *Section) setExact(key, value string) {
	for i, l := range s.lines {
		if l.key == key {
			s.lines[i] = line{raw: key + " = " + value, key: key, value: value}
			return
		}
	}
	s.insert(line{raw: key + " = " + value, key: key, value: value})
}
