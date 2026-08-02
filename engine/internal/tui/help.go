package tui

import _ "embed"

// helpText is the `?` pop-up. Plain text rather than markdown: the engine has
// no markdown renderer, and shipping one for a single help screen is not a
// trade worth making.
//
//go:embed help.txt
var helpText string
