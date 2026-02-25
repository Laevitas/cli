package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const savedFileName = "saved.json"

// SavedQuery represents a bookmarked command with an optional variable template.
type SavedQuery struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// SavedQueries holds the on-disk collection of saved queries.
type SavedQueries struct {
	Queries []SavedQuery `json:"queries"`
}

// savedPath returns the full path to saved.json.
func savedPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, savedFileName), nil
}

// LoadSaved reads saved queries from disk.
// Returns an empty collection if the file doesn't exist yet.
func LoadSaved() (*SavedQueries, error) {
	sq := &SavedQueries{}

	path, err := savedPath()
	if err != nil {
		return sq, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// File doesn't exist yet — that's fine
		return sq, nil
	}

	if err := json.Unmarshal(data, sq); err != nil {
		return sq, fmt.Errorf("parsing saved queries: %w", err)
	}
	return sq, nil
}

// SaveQueries writes the saved queries collection to disk.
func SaveQueries(sq *SavedQueries) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create config directory: %w", err)
	}

	data, err := json.MarshalIndent(sq, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(dir, savedFileName)
	return os.WriteFile(path, data, 0600)
}

// Get finds a saved query by name (case-insensitive).
func (sq *SavedQueries) Get(name string) *SavedQuery {
	nameLower := strings.ToLower(name)
	for i := range sq.Queries {
		if strings.ToLower(sq.Queries[i].Name) == nameLower {
			return &sq.Queries[i]
		}
	}
	return nil
}

// Add adds or updates a saved query.
func (sq *SavedQueries) Add(name, command string) {
	nameLower := strings.ToLower(name)
	for i := range sq.Queries {
		if strings.ToLower(sq.Queries[i].Name) == nameLower {
			sq.Queries[i].Command = command
			return
		}
	}
	sq.Queries = append(sq.Queries, SavedQuery{Name: name, Command: command})
}

// Remove deletes a saved query by name. Returns true if found.
func (sq *SavedQueries) Remove(name string) bool {
	nameLower := strings.ToLower(name)
	for i := range sq.Queries {
		if strings.ToLower(sq.Queries[i].Name) == nameLower {
			sq.Queries = append(sq.Queries[:i], sq.Queries[i+1:]...)
			return true
		}
	}
	return false
}

// Names returns all saved query names, sorted alphabetically.
func (sq *SavedQueries) Names() []string {
	names := make([]string, len(sq.Queries))
	for i, q := range sq.Queries {
		names[i] = q.Name
	}
	sort.Strings(names)
	return names
}

// Expand substitutes {variable} placeholders in a saved command with
// positional arguments. Variables are replaced left-to-right.
// Example: "perps funding {instrument} -r 1d" with args ["BTC-PERPETUAL"]
// becomes "perps funding BTC-PERPETUAL -r 1d".
func Expand(command string, args []string) string {
	result := command
	argIdx := 0
	for argIdx < len(args) {
		// Find next {placeholder}
		start := strings.Index(result, "{")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], "}")
		if end < 0 {
			break
		}
		end += start

		result = result[:start] + args[argIdx] + result[end+1:]
		argIdx++
	}
	return result
}

// CountPlaceholders returns the number of {variable} placeholders in a command.
func CountPlaceholders(command string) int {
	count := 0
	s := command
	for {
		start := strings.Index(s, "{")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			break
		}
		count++
		s = s[start+end+1:]
	}
	return count
}
