package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/fatih/color"

	"logstreamer/integrations/docker"
	"logstreamer/integrations/supabase"
	"logstreamer/streamer"
	"logstreamer/ui"
)

func Run() int {
	color.NoColor = false

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logStreamer := streamer.New()
	inputScanner := bufio.NewScanner(os.Stdin)
	inputScanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	signals := make(chan os.Signal, 1)
	// handle ctrl+c and SIGTERM to shutdown the log streamer
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		fmt.Println()
		cancel()
		logStreamer.Shutdown()
		os.Exit(0)
	}()

	fmt.Println("Logstreamer")
	fmt.Println("Runs multiple shell commands and merges their logs into one view.")
	fmt.Println()

	count, err := ui.PromptCommandCount(inputScanner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read command count: %v\n", err)
		return 1
	}

	for i := 1; i <= count; i++ {
		command, err := ui.PromptLine(inputScanner, fmt.Sprintf("Command %d: ", i))
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read command %d: %v\n", i, err)
			return 1
		}

		if err := logStreamer.AddCommand(command); err != nil {
			fmt.Fprintf(os.Stderr, "could not start command %d: %v\n", i, err)
		}
	}

	fmt.Println()
	fmt.Println("Control commands:")
	fmt.Println("  /add    start another command")
	fmt.Println("  /docker connect running docker container")
	fmt.Println("  /supabase connect remote supabase database")
	fmt.Println("  /quit   exit logstreamer")
	fmt.Println()

	for {
		line, err := ui.PromptLine(inputScanner, "logstreamer> ")
		if err != nil {
			if errors.Is(err, io.EOF) {
				cancel()
				logStreamer.Shutdown()
				return 0
			}

			fmt.Fprintf(os.Stderr, "input error: %v\n", err)
			cancel()
			logStreamer.Shutdown()
			return 1
		}

		switch line {
		case "":
			continue
		case "/quit":
			cancel()
			logStreamer.Shutdown()
			return 0
		case "/add":
			command, err := ui.PromptLine(inputScanner, "New command: ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read new command: %v\n", err)
				continue
			}

			if err := logStreamer.AddCommand(command); err != nil {
				fmt.Fprintf(os.Stderr, "could not start command: %v\n", err)
			}
		case "/docker":
			containerID, err := ui.PromptLine(inputScanner, "Container ID: ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read container ID: %v\n", err)
				continue
			}

			if err := docker.AddDockerCommand(logStreamer, containerID); err != nil {
				fmt.Fprintf(os.Stderr, "could not connect to docker container: %v\n", err)
			}
		case "/supabase":
			supabaseURL, err := ui.PromptLine(inputScanner, "Supabase project URL or ref: ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read supabase URL: %v\n", err)
				continue
			}
			supabaseKey, err := ui.PromptLine(inputScanner, "Supabase personal access token: ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read supabase key: %v\n", err)
				continue
			}
			if err := supabase.AddSupabaseCommand(ctx, logStreamer, supabaseURL, supabaseKey); err != nil {
				fmt.Fprintf(os.Stderr, "could not connect to supabase: %v\n", err)
			}
		default:
			fmt.Println("Unknown control command. Use /add, /docker, /supabase, or /quit.")
		}
	}
}
