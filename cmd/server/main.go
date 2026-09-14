package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const serverVersion = "v1.2.0-headless"

func printBanner(version, port, dataDir string) {
	banner := `
   ___         __  _                       _ __         ____                     
  / _ | ___   / /_(_)__ ________ __ _____ (_) /___ __  / __/__ _____  _____ ____ 
 / __ |/ _ \ / __/ / _ '/ __/ _ // // / / // __/ // / _\ \/ -_) __/ |/ / -_) __/ 
/_/ |_/_//_/ \__/_/\_, /_/  \_,_/\_,_/_//_//__/\_, / /___/\__/_/  |___/\__/_/    
                  /___/                        /___/                              
`
	fmt.Print(banner)
	fmt.Printf("=========================================================================\n")
	fmt.Printf(" Antigravity Headless Server (%s)\n", version)
	fmt.Printf(" - Relay Port : %s\n", port)
	fmt.Printf(" - Data Dir   : %s\n", dataDir)
	fmt.Printf(" - API Base   : http://0.0.0.0:%s/v1\n", port)
	fmt.Printf(" - Health URL : http://0.0.0.0:%s/api/health\n", port)
	fmt.Printf("=========================================================================\n")
}

func main() {
	var (
		portFlag        string
		dataDirFlag     string
		enableProxyFlag bool
		versionFlag     bool
	)

	flag.StringVar(&portFlag, "p", "", "Relay server port (default 18444 or env ANTIGRAVITY_PORT)")
	flag.StringVar(&portFlag, "port", "", "Relay server port (default 18444 or env ANTIGRAVITY_PORT)")
	flag.StringVar(&dataDirFlag, "d", "", "User data directory (default ~/.config/antigravity-proxy or env ANTIGRAVITY_DATA_DIR)")
	flag.StringVar(&dataDirFlag, "data", "", "User data directory (default ~/.config/antigravity-proxy or env ANTIGRAVITY_DATA_DIR)")
	flag.StringVar(&dataDirFlag, "data-dir", "", "User data directory (default ~/.config/antigravity-proxy or env ANTIGRAVITY_DATA_DIR)")
	flag.BoolVar(&enableProxyFlag, "proxy", false, "Also start local MITM proxy engine (port 18443)")
	flag.BoolVar(&versionFlag, "v", false, "Print server version and exit")
	flag.BoolVar(&versionFlag, "version", false, "Print server version and exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of Antigravity Headless Server (%s):\n\n", serverVersion)
		flag.PrintDefaults()
	}

	flag.Parse()

	if versionFlag {
		fmt.Printf("Antigravity Headless Server version %s\n", serverVersion)
		os.Exit(0)
	}

	// 环境变量支持
	if portFlag == "" {
		portFlag = os.Getenv("ANTIGRAVITY_PORT")
	}
	if dataDirFlag == "" {
		dataDirFlag = os.Getenv("ANTIGRAVITY_DATA_DIR")
	}
	if dataDirFlag == "" {
		dataDirFlag = ResolveDefaultDataDir()
	}

	cfg := ServerConfig{
		Port:        portFlag,
		DataDir:     dataDirFlag,
		EnableProxy: enableProxyFlag,
	}

	printBanner(serverVersion, cfg.Port, cfg.DataDir)

	inst, err := NewServerInstance(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to initialize server instance: %v\n", err)
		os.Exit(1)
	}

	if err := inst.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to start server: %v\n", err)
		os.Exit(1)
	}

	// 监听终止信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	fmt.Printf("\nReceived signal [%v], shutting down...\n", sig)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := inst.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ Error during shutdown: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Server terminated gracefully.")
}
