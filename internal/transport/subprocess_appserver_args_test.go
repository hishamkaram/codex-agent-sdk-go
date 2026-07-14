package transport

import (
	"reflect"
	"testing"
)

func TestAppServerArgsPlacesGlobalArgsBeforeSubcommand(t *testing.T) {
	t.Parallel()

	got := appServerArgs(AppServerConfig{
		GlobalArgs: []string{"--dangerously-bypass-hook-trust"},
		ExtraArgs:  []string{"--enable", "hooks"},
	})
	want := []string{"--dangerously-bypass-hook-trust", "app-server", "--enable", "hooks"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}
