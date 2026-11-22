package seeder

import (
	"fmt"
	"io"
)

func (s *Seeder) SetStdout(w io.Writer) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	s.stdout = w
}
func (s *Seeder) SetStderr(w io.Writer) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	s.stderr = w
}
func (s *Seeder) Stdout() io.Writer {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	return s.stdout
}
func (s *Seeder) Stderr() io.Writer {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	return s.stderr
}

func (s *Seeder) Print(format string, args ...any) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	fmt.Fprintf(s.stdout, format, args...)
}

func (s *Seeder) PrintWarning(format string, args ...any) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	fmt.Fprintf(s.stderr, format, args...)
}

func (s *Seeder) PrintError(format string, args ...any) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	fmt.Fprintf(s.stderr, format, args...)
}
