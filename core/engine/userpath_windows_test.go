//go:build windows

package engine

import (
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// The real registry, in a throwaway key (not the user's Environment).
func TestUserPathRegistry(t *testing.T) {
	key := fmt.Sprintf(`Software\SignalsTest-%d`, time.Now().UnixNano())
	defer registry.DeleteKey(registry.CURRENT_USER, key)
	dir := `C:\Programs\Signals`
	if changed, err := setPathIn(key, dir, true); err != nil || !changed {
		t.Fatalf("add to a missing Path: %v %v", changed, err)
	}
	if changed, _ := setPathIn(key, dir, true); changed {
		t.Errorf("adding twice changed the Path")
	}
	k, _ := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE|registry.SET_VALUE)
	v, kind, _ := k.GetStringValue("Path")
	if v != dir || kind != registry.EXPAND_SZ {
		t.Errorf("Path = %q (type %d)", v, kind)
	}
	k.SetExpandStringValue("Path", `%USERPROFILE%\bin;`+dir)
	if changed, err := setPathIn(key, dir, false); err != nil || !changed {
		t.Fatalf("remove: %v %v", changed, err)
	}
	v, kind, _ = k.GetStringValue("Path")
	k.Close()
	if v != `%USERPROFILE%\bin` || kind != registry.EXPAND_SZ {
		t.Errorf("after removing: %q (type %d); the rest, and its %%variables%%, are kept", v, kind)
	}
}
