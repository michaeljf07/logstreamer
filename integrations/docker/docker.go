package docker

import (
	"fmt"
	"logstreamer/streamer"
)

func AddDockerCommand(s *streamer.Streamer, containerID string) error {
	command := fmt.Sprintf("docker logs -f %s", containerID)
	
	if err := s.AddCommand(command); err != nil {
		return fmt.Errorf("could not start docker command: %w", err)
	}

	return nil
}