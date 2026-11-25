package main

import (
	"log"
	"os"

	sdk "github.com/WishingSkin/WishingSkin/pkg/plugin"
)

func main() {
	if err := sdk.ServeRuntimeV2(handleRequest); err != nil {
		log.SetOutput(os.Stderr)
		log.Fatalf("skin-overlays runtime stopped: %v", err)
	}
}
