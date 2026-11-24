package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// IniFile represents the parsed INI configuration.
// It's a map where keys are section names (or an empty string for the global section),
// and values are maps of key-value pairs within that section.
type IniFile map[string]map[string]string

// ParseIni reads an INI file from the given io.Reader and returns an IniFile.
func ParseIni(reader io.Reader) (IniFile, error) {
	ini := make(IniFile)
	scanner := bufio.NewScanner(reader)
	currentSection := "" // Default to global section

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if len(line) == 0 || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for section header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			if _, exists := ini[currentSection]; !exists {
				ini[currentSection] = make(map[string]string)
			}
			continue
		}

		// Parse key-value pair
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		var value string
		if len(parts) > 1 {
			value = strings.TrimSpace(parts[1])
		}

		if _, exists := ini[currentSection]; !exists {
			ini[currentSection] = make(map[string]string)
		}
		ini[currentSection][key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading INI file: %w", err)
	}

	return ini, nil
}

// LoadConfig loads an INI file from the given path.
func LoadConfig(path string) (IniFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return ParseIni(file)
}
