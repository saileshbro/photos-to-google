// Command ptg uploads photos and videos to Google Photos at original quality.
package main

import (
	"os"

	"github.com/saileshbro/photos-to-google/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
