package tray

import _ "embed" // tray icons

// iconPNG is the 32×32 tray icon (accent square, white down arrow), used on
// Linux (StatusNotifierItem) and macOS.
//
//go:embed assets/icon.png
var iconPNG []byte

// iconICO wraps iconPNG in an ICO container for Windows.
//
//go:embed assets/icon.ico
var iconICO []byte
