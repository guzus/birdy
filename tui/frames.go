package tui

import _ "embed"

//go:embed hummingbird.txt
var hummingbirdArt string

// birdFrames contains the ASCII art hummingbird for the splash animation.
// All frames use the same detailed pointillist-style art.
var birdFrames [4]string

func init() {
	birdFrames = [4]string{hummingbirdArt, hummingbirdArt, hummingbirdArt, hummingbirdArt}
}
