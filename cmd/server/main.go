package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/config"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/server"
)

var (
	// Version information (set via ldflags during build)
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"

	configPath  = flag.String("config", "", "Path to the server configuration file (YAML).")
	showVersion = flag.Bool("version", false, "Show version information and exit.")
	showHelp    = flag.Bool("help", false, "Show help message and exit.")
)

func main() {
	flag.Parse()

	// Handle --help flag
	if *showHelp {
		fmt.Fprintf(os.Stdout, "grpc-mesh-server - Control plane for wa-emu system\n\n")
		fmt.Fprintf(os.Stdout, "Usage:\n")
		fmt.Fprintf(os.Stdout, "  grpc-mesh-server [flags]\n\n")
		fmt.Fprintf(os.Stdout, "Flags:\n")
		flag.PrintDefaults()
		os.Exit(0)
	}

	// Handle --version flag
	if *showVersion {
		fmt.Fprintf(os.Stdout, "grpc-mesh-server\n")
		fmt.Fprintf(os.Stdout, "  Version:    %s\n", Version)
		fmt.Fprintf(os.Stdout, "  Build Time: %s\n", BuildTime)
		fmt.Fprintf(os.Stdout, "  Git Commit: %s\n", GitCommit)
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	srv, err := server.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to construct server: %v\n", err)
		os.Exit(1)
	}

	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start server: %v\n", err)
		os.Exit(1)
	}

	// Handle shutdown signals.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	srv.Stop()
}
