package engine

import "strings"

// withDir adds dir to a PATH-style list, unless it's already there.
func withDir(list, dir, sep string, fold bool) (string, bool) {
	for _, p := range strings.Split(list, sep) {
		if samePath(p, dir, fold) {
			return list, false
		}
	}
	if strings.TrimSpace(list) == "" {
		return dir, true
	}
	return strings.TrimRight(list, sep) + sep + dir, true
}

// withoutDir removes dir from a PATH-style list.
func withoutDir(list, dir, sep string, fold bool) (string, bool) {
	var keep []string
	changed := false
	for _, p := range strings.Split(list, sep) {
		if samePath(p, dir, fold) {
			changed = true
			continue
		}
		keep = append(keep, p)
	}
	return strings.Join(keep, sep), changed
}

func samePath(a, b string, fold bool) bool {
	trim := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), `/\`) }
	a, b = trim(a), trim(b)
	if a == "" {
		return false
	}
	if fold {
		return strings.EqualFold(a, b)
	}
	return a == b
}
