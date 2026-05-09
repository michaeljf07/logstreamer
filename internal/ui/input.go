package ui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// PromptLine(scanner, prompt) reads a line of text from input
func PromptLine(scanner *bufio.Scanner, prompt string) (string, error) {
	fmt.Print(prompt)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}

	return strings.TrimSpace(scanner.Text()), nil
}

// PromptCommandCount(scanner) prompts the user for the number of commands to run
func PromptCommandCount(scanner *bufio.Scanner) (int, error) {
	for {
		line, err := PromptLine(scanner, "How many commands do you want to run? ")
		if err != nil {
			return 0, err
		}

		var count int
		if _, err := fmt.Sscanf(line, "%d", &count); err != nil || count < 0 {
			fmt.Println("Enter a valid non-negative integer.")
			continue
		}

		return count, nil
	}
}
