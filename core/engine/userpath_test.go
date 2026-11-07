package engine

import "testing"

func TestPathLists(t *testing.T) {
	dir := `C:\Users\dev\AppData\Local\Programs\Signals`
	cases := []struct {
		in, want string
		changed  bool
	}{
		{"", dir, true},
		{`C:\bin`, `C:\bin;` + dir, true},
		{`C:\bin;`, `C:\bin;` + dir, true},
		{`C:\bin;c:\users\dev\appdata\local\programs\signals\`, `C:\bin;c:\users\dev\appdata\local\programs\signals\`, false},
	}
	for _, c := range cases {
		if got, changed := withDir(c.in, dir, ";", true); got != c.want || changed != c.changed {
			t.Errorf("withDir(%q) = %q, %v; want %q, %v", c.in, got, changed, c.want, c.changed)
		}
	}
	if got, changed := withoutDir(`C:\bin;`+dir+`;D:\tools`, dir, ";", true); got != `C:\bin;D:\tools` || !changed {
		t.Errorf("withoutDir: %q %v", got, changed)
	}
	if _, changed := withoutDir(`C:\bin`, dir, ";", true); changed {
		t.Errorf("withoutDir changed a PATH without the folder")
	}
	if _, changed := withDir("/usr/bin:/Home/x/.local/bin", "/home/x/.local/bin", ":", false); !changed {
		t.Errorf("Linux paths are case-sensitive")
	}
}
