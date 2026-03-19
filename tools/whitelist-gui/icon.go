package main

import (
	"bytes"
	_ "embed"
	"image/png"

	"github.com/lxn/walk"
)

// groupIconResourceID is the RT_GROUP_ICON id that rsrc assigns when build.ps1
// passes the manifest and the icon together: the manifest takes id 1, the icon
// group id 2, and the individual images 3 and up.
//
// Verify with:  python tools/mkico/peres.py dist/agent/Guardian.exe
const groupIconResourceID = 2

// guardianMark is the same image guardian.ico is generated from. It backs the
// fallback below, so a plain `go build` and the test binary -- neither of which
// has a resource section -- still get a window icon.
//
//go:embed guardian-mark.png
var guardianMark []byte

// appIcon returns the window icon, or nil if it cannot be produced.
//
// Embedding the icon in the PE resources is what makes Explorer and the taskbar
// show it, but it does not set the icon of the window itself: walk needs to be
// told explicitly, otherwise the title bar keeps the default one.
func appIcon() *walk.Icon {
	if icon, err := walk.NewIconFromResourceId(groupIconResourceID); err == nil {
		return icon
	}

	img, err := png.Decode(bytes.NewReader(guardianMark))
	if err != nil {
		return nil
	}

	icon, err := walk.NewIconFromImageForDPI(img, 96)
	if err != nil {
		return nil
	}
	return icon
}
