package main

import (
	"embed"
	"flag"
	"fmt"
	"os"

	"durusql/internal/config"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is set at package time: -ldflags "-X main.version=1.2.3"
var version = "dev"

// updateSource is the default release channel source: a GitHub repo ("https://github.com/owner/durusql")
// or a base URL hosting stable.json / beta.json. Set at package time: -X main.updateSource=…
var updateSource = ""

func main() {
	importDG := flag.Bool("import-datagrip", false, "import DataGrip data sources into ~/.config/rowdy and exit")
	flag.Parse()
	if *importDG {
		os.Exit(runImportDataGrip())
	}

	app := NewApp()
	err := wails.Run(&options.App{
		Title:            "DuruSQL",
		Width:            1400,
		Height:           900,
		MinWidth:         900,
		MinHeight:        600,
		Frameless:        true, // the app draws its own title bar (DataGrip-like), see App.svelte
		BackgroundColour: &options.RGBA{R: 0x1e, G: 0x1f, B: 0x22, A: 0xff},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
	})
	if err != nil {
		panic(err)
	}
}

func runImportDataGrip() int {
	store, err := config.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	res, err := importDataGrip(store)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	for _, n := range res.Names {
		fmt.Println("imported:", n)
	}
	for _, w := range res.Warnings {
		fmt.Println("warning:", w)
	}
	fmt.Printf("%d connections imported\n", res.Imported)
	return 0
}
