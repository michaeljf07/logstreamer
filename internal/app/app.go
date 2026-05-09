package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/fatih/color"

	"logstreamer/internal/streamer"
	"logstreamer/internal/ui"
)

func Run() int {
	color.NoColor = false

	logStreamer := streamer.New()
	inputScanner := bufio.NewScanner(os.Stdin)
	inputScanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	signals := make(chan os.Signal, 1)
	// handle ctrl+c and SIGTERM to shutdown the log streamer
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		fmt.Println()
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
	fmt.Println("  /add   start another command")
	fmt.Println("  /quit  exit logstreamer")
	fmt.Println()

	for {
		line, err := ui.PromptLine(inputScanner, "logstreamer> ")
		if err != nil {
			if errors.Is(err, io.EOF) {
				logStreamer.Shutdown()
				return 0
			}

			fmt.Fprintf(os.Stderr, "input error: %v\n", err)
			logStreamer.Shutdown()
			return 1
		}

		switch line {
		case "":
			continue
		case "/quit":
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
		default:
			fmt.Println("Unknown control command. Use /add or /quit.")
		}
	}
}
