package webui

import (
	"io/fs"
	"testing"
)

func TestAssetsContainBootstrapFile(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(assets, "bootstrap.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ConfigHub web assets are replaced by the Vite build.\n" {
		t.Fatalf("unexpected bootstrap asset: %q", data)
	}
}
