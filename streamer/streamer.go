package streamer

import (
	"bufio"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/fatih/color"
)

type commandProcess struct {
	id    int
	label string
	cmd   *exec.Cmd
}

type Streamer struct {
	mu        sync.Mutex
	nextID    int
	palette   []*color.Color
	processes map[int]*commandProcess
	wg        sync.WaitGroup
}

func New() *Streamer {
	return &Streamer{
		nextID: 1,
		palette: []*color.Color{
			color.New(color.FgCyan, color.Bold),
			color.New(color.FgGreen, color.Bold),
			color.New(color.FgYellow, color.Bold),
			color.New(color.FgBlue, color.Bold),
			color.New(color.FgMagenta, color.Bold),
			color.New(color.FgHiCyan, color.Bold),
			color.New(color.FgHiGreen, color.Bold),
			color.New(color.FgHiYellow, color.Bold),
		},
		processes: make(map[int]*commandProcess),
	}
}

func (s *Streamer) AddCommand(raw string) error {
	command := strings.TrimSpace(raw)
	if command == "" {
		return errors.New("command cannot be empty")
	}

	cmd := exec.Command("sh", "-c", command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	s.mu.Lock()
	id := s.nextID
	s.nextID++
	process := &commandProcess{
		id:    id,
		label: fmt.Sprintf("cmd-%02d", id),
		cmd:   cmd,
	}
	s.processes[id] = process
	s.mu.Unlock()

	if err := cmd.Start(); err != nil {
		s.mu.Lock()
		delete(s.processes, id)
		s.mu.Unlock()
		return fmt.Errorf("start %q: %w", command, err)
	}

	s.PrintSystemMessage(process.label, fmt.Sprintf("started: %s", command))

	s.wg.Add(3)
	// start the goroutines to stream output from stdout and stderr
	go s.streamOutput(process, stdout, "stdout")
	go s.streamOutput(process, stderr, "stderr")
	go s.waitForExit(process)

	return nil
}

func (s *Streamer) Shutdown() {
	s.stopAll()
	s.wg.Wait()
}

// streamOutput(process, pipe, streamName) collects the output from a command and prints it to the console
func (s *Streamer) streamOutput(process *commandProcess, pipe io.ReadCloser, streamName string) {
	defer s.wg.Done()

	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		s.printLogLine(process, streamName, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		s.PrintSystemMessage(process.label, fmt.Sprintf("%s stream error: %v", streamName, err))
	}
}

func (s *Streamer) waitForExit(process *commandProcess) {
	defer s.wg.Done()

	err := process.cmd.Wait()

	s.mu.Lock()
	delete(s.processes, process.id)
	s.mu.Unlock()

	if err == nil {
		s.PrintSystemMessage(process.label, "completed successfully")
		return
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		s.PrintSystemMessage(process.label, fmt.Sprintf("exited with status %d", exitErr.ExitCode()))
		return
	}

	s.PrintSystemMessage(process.label, fmt.Sprintf("finished with error: %v", err))
}

// printLogLine(process, streamName, line) prints a log line to the console prefixed with the process label and the stream name
func (s *Streamer) printLogLine(process *commandProcess, streamName string, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	labelColor := s.palette[(process.id - 1) % len(s.palette)]
	label := labelColor.Sprintf("[%s]", strings.ToUpper(process.label))

	if streamName == "stderr" {
		fmt.Printf("%s %s %s\n", label, color.New(color.FgRed).Sprint("[stderr]"), line)
		return
	}

	fmt.Printf("%s %s\n", label, line)
}

// printSystemMessage(label, message) prints a system message to the console prefixed with [system]
func (s *Streamer) PrintSystemMessage(label string, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	systemPrefix := color.New(color.FgWhite, color.BgBlack, color.Bold).Sprint("[system]")
	coloredLabel := color.New(color.FgHiWhite, color.Bold).Sprintf("[%s]", strings.ToUpper(label))
	fmt.Printf("%s %s %s\n", systemPrefix, coloredLabel, message)
}

// Prints a line as if from a named integration source (not a shell command)
func (s *Streamer) PrintIntegrationLog(label string, streamName string, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Use a hash function to get a consistent color for the label
	h := fnv.New32a()
	_, _ = h.Write([]byte(label))
	idx := int(h.Sum32()) % len(s.palette)
	labelColor := s.palette[idx]
	
	displayLabel := labelColor.Sprintf("[%s]", strings.ToUpper(label))

	if streamName == "stderr" {
		fmt.Printf("%s %s %s\n", displayLabel, color.New(color.FgRed).Sprint("[stderr]"), line)
		return
	}

	fmt.Printf("%s %s\n", displayLabel, line)
}

// stopAll(s) stops all running commands
func (s *Streamer) stopAll() {
	s.mu.Lock()
	processes := make([]*commandProcess, 0, len(s.processes))
	for _, process := range s.processes {
		processes = append(processes, process)
	}
	s.mu.Unlock()

	for _, process := range processes {
		if process.cmd.Process != nil {
			_ = process.cmd.Process.Signal(syscall.SIGTERM)
		}
	}
}
